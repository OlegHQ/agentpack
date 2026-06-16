import { CODEX_API_ENDPOINT, ORIGINATOR as ORIGINATOR_DEFAULT } from "./auth/constants.ts";
import {
  codexBaseUrl,
  codexOriginator,
  codexPreviousResponseId,
  codexTransport,
  codexUserAgent,
} from "../../config.ts";
declare const BUILD_VERSION: string | undefined;
const PROXY_VERSION = typeof BUILD_VERSION === "string" ? BUILD_VERSION : "dev";
import { forceRefresh, getAuth } from "./auth/manager.ts";
import type { Logger } from "../../log.ts";
import type { RequestContext } from "../types.ts";
import { toWebSocketRequest, type ResponsesRequest } from "./translate/request.ts";
import { retryOn429, sleep } from "../retry.ts";
import { headersToRecord } from "../../traffic.ts";
import { summarizeCodexRequestSize } from "./request-summary.ts";
import {
  CodexWebSocketSetupError,
  codexWebSocketHeaders,
  codexWebSocketRequest,
  invalidateCodexWebSocketPoolKey,
  isPreviousResponseMissingError,
} from "./websocket.ts";
import { clearContinuation, type ContinuationCandidate } from "./continuation.ts";

const FETCH_WATCHDOG_INTERVAL_MS = 30_000;
let fetchHeaderTimeoutMs = 60_000;
let fetchHeaderTimeoutRetries = 1;

export function setCodexHeaderTimeoutForTests(timeoutMs: number, retries: number): void {
  fetchHeaderTimeoutMs = timeoutMs;
  fetchHeaderTimeoutRetries = retries;
}

export interface CodexResponse {
  body: ReadableStream<Uint8Array>;
  status: number;
  headers: Headers;
}

export interface PostCodexOptions {
  continuation?: ContinuationCandidate;
  poolKey?: string;
}

export async function postCodex(
  body: ResponsesRequest,
  ctx: RequestContext,
  opts: PostCodexOptions = {},
): Promise<CodexResponse> {
  const log = ctx.childLogger("codex.client");
  return retryHeaderTimeouts(
    () =>
      retryOn429(() => attemptPostCodex(body, ctx, log, opts), {
        log,
        signal: ctx.signal,
        classify: (err) =>
          err instanceof CodexError && err.status === 429
            ? { retryAfter: err.meta?.retryAfter }
            : undefined,
      }),
    log,
    ctx.signal,
    body,
  );
}

async function retryHeaderTimeouts(
  run: () => Promise<CodexResponse>,
  log: Logger,
  signal: AbortSignal | undefined,
  body: ResponsesRequest,
): Promise<CodexResponse> {
  for (let attempt = 0; ; attempt++) {
    try {
      return await run();
    } catch (err) {
      if (!(err instanceof CodexHeaderTimeoutError) || attempt >= fetchHeaderTimeoutRetries)
        throw err;
      const waitMs = fetchHeaderTimeoutMs <= 10 ? 0 : 500 + Math.round(Math.random() * 1000);
      log.warn("codex response headers timed out, retrying", {
        attempt: attempt + 1,
        maxRetries: fetchHeaderTimeoutRetries,
        waitMs,
        timeoutMs: fetchHeaderTimeoutMs,
        model: body.model,
        inputCount: body.input.length,
        toolCount: body.tools?.length ?? 0,
        requestSize: summarizeCodexRequestSize(body),
      });
      await sleep(waitMs, signal);
    }
  }
}

async function attemptPostCodex(
  body: ResponsesRequest,
  ctx: RequestContext,
  log: Logger,
  opts: PostCodexOptions,
): Promise<CodexResponse> {
  let auth = await getAuth();
  let resp: Response;
  try {
    resp = await doFetch(auth.access, auth.accountId, body, ctx, log, opts);
  } catch (err) {
    if (!(err instanceof CodexWebSocketSetupError)) throw err;
    if (err.status === 429) {
      throw new CodexError(429, "Rate limited", err.message, { retryAfter: err.retryAfter });
    }
    if (err.requestSent) throw err;
    if (err.status !== 401 && err.status !== 403) throw err;
    log.warn("codex websocket auth failed, refreshing token", { status: err.status });
    auth = await forceRefresh();
    resp = await doFetch(auth.access, auth.accountId, body, ctx, log, opts);
  }

  if (resp.status === 401) {
    log.warn("got 401, refreshing token", {});
    try {
      auth = await forceRefresh();
      resp = await doFetch(auth.access, auth.accountId, body, ctx, log, opts);
    } catch (err) {
      log.error("refresh after 401 failed", { err: String(err) });
    }
  }

  if (resp.status === 403) {
    const text = await safeText(resp);
    ctx.traffic?.writeText("031-upstream-error-body", text);
    log.error("403 from upstream (non-refreshable)", { body: text });
    throw new CodexError(403, "Forbidden", text);
  }

  if (resp.status === 429) {
    const retryAfter = resp.headers.get("retry-after") || undefined;
    const text = await safeText(resp);
    ctx.traffic?.writeText("031-upstream-error-body", text);
    throw new CodexError(429, "Rate limited", text, { retryAfter });
  }

  if (!resp.ok) {
    const text = await safeText(resp);
    ctx.traffic?.writeText("031-upstream-error-body", text);
    throw new CodexError(resp.status, "Upstream error", text);
  }

  if (!resp.body) throw new CodexError(500, "Upstream returned no body");

  return { body: resp.body, status: resp.status, headers: resp.headers };
}

async function doFetch(
  accessToken: string,
  accountId: string | undefined,
  body: ResponsesRequest,
  ctx: RequestContext,
  log: Logger,
  opts: PostCodexOptions,
): Promise<Response> {
  const mode = codexTransport();
  const continuationEnabled = codexPreviousResponseId();
  const poolKey = continuationEnabled ? ctx.sessionId : undefined;
  if (shouldResetWebSocketPool(opts.continuation)) invalidateCodexWebSocketPoolKey(poolKey);
  const websocketOpts = {
    ...opts,
    poolKey,
  };
  if (mode === "websocket") {
    return doFetchWebSocket(accessToken, accountId, body, ctx, log, websocketOpts);
  }
  if (mode === "auto") {
    try {
      return await doFetchWebSocket(accessToken, accountId, body, ctx, log, websocketOpts);
    } catch (err) {
      if (err instanceof CodexWebSocketSetupError && err.requestSent) throw err;
      log.warn("codex websocket failed before response, falling back to http", {
        err: String(err),
      });
    }
  }
  return doFetchHttp(accessToken, accountId, body, ctx, log);
}

function codexHeaders(
  accessToken: string,
  accountId: string | undefined,
  ctx: RequestContext,
): Headers {
  const headers = new Headers({
    "Content-Type": "application/json",
    accept: "text/event-stream",
    authorization: `Bearer ${accessToken}`,
    originator: codexOriginator(ORIGINATOR_DEFAULT),
    "openai-beta": "responses=experimental",
  });
  const userAgent = codexUserAgent(`claude-code-proxy/${PROXY_VERSION}`);
  if (userAgent) headers.set("User-Agent", userAgent);
  if (accountId) headers.set("ChatGPT-Account-Id", accountId);
  if (ctx.sessionId) {
    headers.set("session_id", ctx.sessionId);
    headers.set("x-client-request-id", ctx.sessionId);
    headers.set("x-codex-window-id", `${ctx.sessionId}:0`);
  }
  return headers;
}

async function doFetchWebSocket(
  accessToken: string,
  accountId: string | undefined,
  body: ResponsesRequest,
  ctx: RequestContext,
  log: Logger,
  opts: PostCodexOptions,
): Promise<Response> {
  const headers = codexHeaders(accessToken, accountId, ctx);
  const requestHeaders = codexWebSocketHeaders(headers);
  const codexUrl = codexBaseUrl(CODEX_API_ENDPOINT);
  const continuation = opts.continuation;
  const wsBody = toWebSocketRequest(body, {
    previousResponseId: continuation?.previousResponseId,
    input: continuation?.inputDelta,
  });
  const bodyJson = JSON.stringify(wsBody);
  const size = summarizeCodexRequestSize(wsBody, bodyJson);
  ctx.traffic?.writeJson("020-upstream-request", wsBody);
  ctx.traffic?.writeJson("021-upstream-request-metadata", {
    provider: "codex",
    transport: "websocket",
    url: codexUrl,
    method: "GET",
    headers: requestHeaders,
    size,
    continuation: {
      previousResponseId: continuation?.previousResponseId,
      inputDeltaCount: continuation?.inputDeltaCount ?? body.input.length,
      disabledReason: continuation?.disabledReason,
    },
  });
  log.debug("posting to codex websocket", {
    url: codexUrl,
    model: body.model,
    inputCount: wsBody.input.length,
    toolCount: body.tools?.length ?? 0,
    serviceTier: body.service_tier,
    reasoningEffort: body.reasoning?.effort,
    promptCacheKey: body.prompt_cache_key,
    continuation: {
      previousResponseId: continuation?.previousResponseId,
      inputDeltaCount: continuation?.inputDeltaCount,
      disabledReason: continuation?.disabledReason,
    },
    size,
  });
  const startedAt = Date.now();
  let stream: ReadableStream<Uint8Array>;
  try {
    stream = await codexWebSocketRequest({
      url: codexUrl,
      headers,
      body: wsBody,
      ctx,
      connectTimeoutMs: 15_000,
      idleTimeoutMs: 300_000,
      poolKey: opts.poolKey,
    });
  } catch (err) {
    if (!isSafeFullWebSocketRetry(err, continuation)) throw err;
    clearContinuation(ctx.sessionId);
    invalidateCodexWebSocketPoolKey(opts.poolKey);
    log.warn("codex previous response missing, retrying full websocket request", {
      previousResponseId: continuation?.previousResponseId,
    });
    stream = await codexWebSocketRequest({
      url: codexUrl,
      headers,
      body: toWebSocketRequest(body),
      ctx,
      connectTimeoutMs: 15_000,
      idleTimeoutMs: 300_000,
      poolKey: opts.poolKey,
    });
  }
  const elapsedMs = Date.now() - startedAt;
  const responseHeaders = new Headers({ "content-type": "text/event-stream" });
  ctx.traffic?.writeJson("030-upstream-response-headers", {
    status: 200,
    statusText: "OK",
    elapsedMs,
    headers: headersToRecord(responseHeaders),
  });
  return new Response(stream, { status: 200, headers: responseHeaders });
}

async function doFetchHttp(
  accessToken: string,
  accountId: string | undefined,
  body: ResponsesRequest,
  ctx: RequestContext,
  log: Logger,
): Promise<Response> {
  const headers = codexHeaders(accessToken, accountId, ctx);
  const codexUrl = codexBaseUrl(CODEX_API_ENDPOINT);

  const bodyJson = JSON.stringify(body);
  const size = summarizeCodexRequestSize(body, bodyJson);
  ctx.traffic?.writeJson("020-upstream-request", body);
  ctx.traffic?.writeJson("021-upstream-request-metadata", {
    provider: "codex",
    transport: "http",
    url: codexUrl,
    method: "POST",
    headers: headersToRecord(headers),
    size,
  });

  log.debug("posting to codex", {
    url: codexUrl,
    model: body.model,
    inputCount: body.input.length,
    toolCount: body.tools?.length ?? 0,
    serviceTier: body.service_tier,
    reasoningEffort: body.reasoning?.effort,
    promptCacheKey: body.prompt_cache_key,
    size,
  });

  const startedAt = Date.now();
  const watchdog = setInterval(() => {
    log.info("waiting for codex response headers", {
      elapsedMs: Date.now() - startedAt,
      model: body.model,
      inputCount: body.input.length,
      toolCount: body.tools?.length ?? 0,
    });
  }, FETCH_WATCHDOG_INTERVAL_MS);
  const headerTimeout = new AbortController();
  const timeout = setTimeout(() => {
    headerTimeout.abort(new CodexHeaderTimeoutError(fetchHeaderTimeoutMs));
  }, fetchHeaderTimeoutMs);
  const onAbort = () => headerTimeout.abort(ctx.signal.reason);
  ctx.signal.addEventListener("abort", onAbort, { once: true });
  try {
    const resp = await fetch(codexUrl, {
      method: "POST",
      headers,
      body: bodyJson,
      signal: headerTimeout.signal,
    });
    const elapsedMs = Date.now() - startedAt;
    ctx.traffic?.writeJson("030-upstream-response-headers", {
      status: resp.status,
      statusText: resp.statusText,
      elapsedMs,
      headers: headersToRecord(resp.headers),
    });
    log.debug("received codex response headers", {
      status: resp.status,
      elapsedMs,
    });
    return resp;
  } catch (err) {
    if (headerTimeout.signal.reason instanceof CodexHeaderTimeoutError) {
      throw headerTimeout.signal.reason;
    }
    throw err;
  } finally {
    ctx.signal.removeEventListener("abort", onAbort);
    clearTimeout(timeout);
    clearInterval(watchdog);
  }
}

async function safeText(resp: Response): Promise<string> {
  try {
    return await resp.text();
  } catch {
    return "";
  }
}

function isSafeFullWebSocketRetry(
  err: unknown,
  continuation: ContinuationCandidate | undefined,
): boolean {
  if (!continuation?.previousResponseId) return false;
  return isPreviousResponseMissingError(err);
}

function shouldResetWebSocketPool(continuation: ContinuationCandidate | undefined): boolean {
  if (!continuation?.disabledReason) return false;
  return continuation.disabledReason !== "missing_state" && continuation.disabledReason !== "disabled";
}

export class CodexHeaderTimeoutError extends Error {
  constructor(public timeoutMs: number) {
    super(`Timed out waiting ${timeoutMs}ms for Codex response headers`);
    this.name = "CodexHeaderTimeoutError";
  }
}

export class CodexError extends Error {
  constructor(
    public status: number,
    message: string,
    public detail?: string,
    public meta?: { retryAfter?: string },
  ) {
    super(message);
    this.name = "CodexError";
  }
}
