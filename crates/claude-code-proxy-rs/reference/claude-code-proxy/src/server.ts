import { createLogger, logDir, REDACT_KEYS } from "./log.ts";
import { createTrafficCapture, headersToRecord } from "./traffic.ts";
import { jsonError, jsonResponse, streamResponse } from "./anthropic/response.ts";

import type { AnthropicRequest } from "./anthropic/schema.ts";
import type { AliasProvider } from "./config.ts";
import type { Provider, RequestContext } from "./providers/types.ts";
import {
  groupSupportedModelsByProvider,
  normalizeIncomingModel,
  providerForModel,
} from "./providers/registry.ts";
import {
  existingSession,
  recordSessionRequest,
  type SessionState,
} from "./server/session-affinity.ts";

export { normalizeIncomingModel };

const rootLog = createLogger("server");

export interface ServeOptions {
  port: number;
}

export function startServer(opts: ServeOptions): { stop: () => void; port: number } {
  const server = Bun.serve({
    hostname: "127.0.0.1",
    port: opts.port,
    idleTimeout: 255,
    async fetch(req) {
      const url = new URL(req.url);
      const start = Date.now();
      const reqId = crypto.randomUUID();
      rootLog.info("request", {
        reqId,
        method: req.method,
        path: url.pathname,
        ...(url.search ? { query: redactedQuery(url) } : {}),
      });
      try {
        const resp = await route(req, url, reqId);
        const ms = Date.now() - start;
        rootLog.info("response", { reqId, status: resp.status, ms });
        if (!resp.body) return resp;
        return wrapStreamResponse(resp, reqId, start, rootLog);
      } catch (err) {
        if (isAbortError(err)) {
          rootLog.info("client disconnected", { reqId, ms: Date.now() - start });
          return new Response(null, { status: 499 });
        }
        rootLog.error("handler error", { reqId, err: String(err), stack: (err as Error)?.stack });
        return jsonError(500, "internal_error", String(err));
      }
    },
  });
  rootLog.info("server listening", { port: server.port, logDir: logDir() });
  return {
    port: Number(server.port),
    stop: () => server.stop(),
  };
}

async function route(req: Request, url: URL, reqId: string): Promise<Response> {
  if (url.pathname === "/healthz") {
    return jsonResponse({ ok: true });
  }

  if (req.method === "POST" && url.pathname === "/v1/messages/count_tokens") {
    return routeAnthropicPost(req, url, reqId, "count_tokens");
  }

  if (req.method === "POST" && url.pathname === "/v1/messages") {
    return routeAnthropicPost(req, url, reqId, "messages");
  }

  return jsonError(404, "not_found", `No route for ${req.method} ${url.pathname}`);
}

async function routeAnthropicPost(
  req: Request,
  url: URL,
  reqId: string,
  kind: "messages" | "count_tokens",
): Promise<Response> {
  const body = await parseJsonBody(req);
  if (body instanceof Response) return body;
  const sessionId = req.headers.get("x-claude-code-session-id") || undefined;
  const session = existingSession(sessionId);
  const provider = routeProvider(body, reqId, session?.affinityProvider);
  if (provider instanceof Response) return provider;
  const current = recordSessionRequest(sessionId, session, provider.name, body.model);
  const ctx = buildCtx(req, reqId, provider.name, sessionId, current);
  captureInboundTraffic(ctx, req, url, body, kind, provider.name);
  ctx.childLogger("server").info("dispatch", { model: body.model });
  return kind === "messages"
    ? provider.handleMessages(body, ctx)
    : provider.handleCountTokens(body, ctx);
}

function buildCtx(
  req: Request,
  reqId: string,
  providerName: string,
  sessionId: string | undefined,
  session: SessionState | undefined,
): RequestContext {
  const sessionSeq = session?.seq;
  const bindings = { reqId, sessionId, sessionSeq, provider: providerName };
  return {
    reqId,
    sessionId,
    sessionSeq,
    signal: req.signal,
    traffic: createTrafficCapture({ reqId, sessionId, sessionSeq, provider: providerName }),
    childLogger: (service) => createLogger(service, bindings),
  };
}

function captureInboundTraffic(
  ctx: RequestContext,
  req: Request,
  url: URL,
  body: AnthropicRequest,
  kind: "messages" | "count_tokens",
  provider: string,
): void {
  if (!ctx.traffic) return;
  ctx.traffic.writeJson("000-metadata", {
    reqId: ctx.reqId,
    sessionId: ctx.sessionId,
    sessionSeq: ctx.sessionSeq,
    kind,
    provider,
    model: body.model,
    method: req.method,
    path: url.pathname,
    query: redactedQuery(url),
    headers: headersToRecord(req.headers),
  });
  ctx.traffic.writeJson("010-anthropic-request", body);
}

function routeProvider(
  body: AnthropicRequest,
  reqId: string,
  sessionAliasProvider?: AliasProvider,
): Provider | Response {
  if (!body.model) {
    return jsonError(
      400,
      "invalid_request_error",
      `Missing "model" in request body. ${knownModelsMessage()}`,
    );
  }
  body.model = normalizeIncomingModel(body.model);
  const provider = providerForModel(body.model, sessionAliasProvider);
  if (!provider) {
    rootLog.warn("unknown model", { reqId, model: body.model });
    return jsonError(
      400,
      "invalid_request_error",
      `Unknown model "${body.model}". ${knownModelsMessage()}`,
    );
  }
  return provider;
}

function knownModelsMessage(): string {
  const parts: string[] = [];
  for (const [provider, models] of groupSupportedModelsByProvider()) {
    parts.push(`${provider}: ${models.join(", ")}`);
  }
  return `Supported: ${parts.join("; ")}.`;
}

async function parseJsonBody(req: Request): Promise<AnthropicRequest | Response> {
  try {
    return (await req.json()) as AnthropicRequest;
  } catch (err) {
    return jsonError(400, "invalid_request_error", `Invalid JSON: ${String(err)}`);
  }
}

function isAbortError(err: unknown): boolean {
  return err instanceof Error && err.name === "AbortError";
}

function isClosedControllerError(err: unknown): boolean {
  return err instanceof TypeError && err.message.includes("Controller is already closed");
}

export function wrapStreamResponse(
  resp: Response,
  reqId: string,
  start: number,
  log: ReturnType<typeof createLogger>,
): Response {
  const body = resp.body!;
  const reader = body.getReader();
  let closed = false;
  const stream = new ReadableStream<Uint8Array>({
    async pull(controller) {
      if (closed) return;
      const safeClose = () => {
        if (closed) return;
        closed = true;
        try {
          controller.close();
        } catch {}
      };
      const safeError = (err: unknown) => {
        if (closed) return;
        closed = true;
        try {
          controller.error(err);
        } catch {}
      };
      try {
        const { done, value } = await reader.read();
        if (closed) return;
        if (done) {
          log.info("request_completed", { reqId, status: resp.status, ms: Date.now() - start });
          safeClose();
          return;
        }
        try {
          controller.enqueue(value);
        } catch (err) {
          if (!isClosedControllerError(err)) {
            log.error("stream error", { reqId, err: String(err) });
            reader.cancel().catch(() => {});
            safeError(err);
          } else {
            closed = true;
          }
        }
      } catch (err) {
        if (isAbortError(err) || isClosedControllerError(err)) {
          log.info("client disconnected", { reqId, ms: Date.now() - start });
        } else {
          log.error("stream error", { reqId, err: String(err) });
        }
        safeError(err);
      }
    },
    cancel() {
      closed = true;
      reader.cancel().catch(() => {});
    },
  });
  return streamResponse(resp, stream);
}

function redactedQuery(url: URL): Record<string, string> {
  const out: Record<string, string> = {};
  for (const [k, v] of url.searchParams) {
    out[k] = REDACT_KEYS.has(k.toLowerCase()) ? `[redacted len=${v.length}]` : v;
  }
  return out;
}
