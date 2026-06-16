import WebSocket from "ws";
import { headersToRecord } from "../../traffic.ts";
import type { RequestContext } from "../types.ts";
import type { ResponsesWebSocketRequest } from "./translate/request.ts";

export const WEBSOCKET_PROTOCOL_HEADER = "responses_websockets=2026-02-06";

export class CodexWebSocketSetupError extends Error {
  constructor(
    message: string,
    public status?: number,
    public code?: string,
    public retryAfter?: string,
    public requestSent = false,
  ) {
    super(message);
    this.name = "CodexWebSocketSetupError";
  }
}

export interface CodexWebSocketOptions {
  url: string;
  headers: Headers;
  body: ResponsesWebSocketRequest;
  ctx: RequestContext;
  connectTimeoutMs: number;
  idleTimeoutMs: number;
  poolKey?: string;
}

export type CodexWebSocketInvalidateReason = "cancel" | "error" | "close";

interface ActiveRequest {
  controller?: ReadableStreamDefaultController<Uint8Array>;
  pendingChunks: Uint8Array[];
  resolve: (stream: ReadableStream<Uint8Array>) => void;
  reject: (err: Error) => void;
  settled: boolean;
  done: boolean;
  requestSent: boolean;
  chunksExposed: boolean;
  idleTimer?: ReturnType<typeof setTimeout>;
  stream: ReadableStream<Uint8Array>;
  ctx: RequestContext;
  releaseQueue: () => void;
}

interface PoolEntry {
  connection: CodexWebSocketConnection;
  lastUsed: number;
}

class CodexWebSocketConnection {
  private socket?: WebSocket;
  private active?: ActiveRequest;
  private activeAbort?: () => void;
  private connectPromise?: Promise<void>;
  private connectTimer?: ReturnType<typeof setTimeout>;
  private closed = false;
  private requestHeaders: Record<string, string>;
  private queue: Promise<void> = Promise.resolve();

  constructor(
    private readonly wsUrl: string,
    headers: Headers,
    private readonly connectTimeoutMs: number,
    private readonly idleTimeoutMs: number,
    private readonly keepAlive: boolean,
    private readonly onInvalidate?: (reason: CodexWebSocketInvalidateReason) => void,
  ) {
    this.requestHeaders = codexWebSocketHeaders(headers);
  }

  updateHeaders(headers: Headers): void {
    this.requestHeaders = codexWebSocketHeaders(headers);
  }

  isClosed(): boolean {
    return this.closed || !this.socket || this.socket.readyState === WebSocket.CLOSED;
  }

  hasActiveRequest(): boolean {
    return !!this.active;
  }

  dispose(): void {
    this.closed = true;
    this.clearConnectTimer();
    this.failActive(new Error("Codex WebSocket connection closed"));
    this.socket?.on("error", () => {});
    this.socket?.terminate();
  }

  async request(opts: CodexWebSocketOptions): Promise<ReadableStream<Uint8Array>> {
    const previous = this.queue.catch(() => {});
    let releaseQueue: () => void = () => {};
    const queued = new Promise<void>((resolve) => {
      releaseQueue = resolve;
    });
    this.queue = previous.then(() => queued);
    await previous;
    if (this.closed) {
      releaseQueue();
      throw new Error("Codex WebSocket connection closed");
    }
    if (opts.ctx.signal.aborted) {
      releaseQueue();
      throw new DOMException("Aborted", "AbortError");
    }
    try {
      return await this.runRequest(opts, releaseQueue);
    } catch (err) {
      releaseQueue();
      throw err;
    }
  }

  private async runRequest(
    opts: CodexWebSocketOptions,
    releaseQueue: () => void,
  ): Promise<ReadableStream<Uint8Array>> {
    this.updateHeaders(opts.headers);
    return new Promise((resolve, reject) => {
      const pendingChunks: Uint8Array[] = [];
      let controller: ReadableStreamDefaultController<Uint8Array> | undefined;
      let active: ActiveRequest;
      const stream = new ReadableStream<Uint8Array>({
        start: (next) => {
          controller = next;
          for (const chunk of pendingChunks.splice(0)) {
            if (chunk.byteLength === 0) next.close();
            else next.enqueue(chunk);
          }
        },
        cancel: () => {
          active.done = true;
          this.clearIdle(active);
          opts.ctx.signal.removeEventListener("abort", onAbort);
          this.activeAbort = undefined;
          this.active = undefined;
          this.closed = true;
          this.socket?.on("error", () => {});
          this.socket?.terminate();
          this.onInvalidate?.("cancel");
          active.releaseQueue();
        },
      });
      active = {
        controller,
        pendingChunks,
        resolve,
        reject,
        settled: false,
        done: false,
        requestSent: false,
        chunksExposed: false,
        ctx: opts.ctx,
        releaseQueue,
        stream,
      };
      const onAbort = () => this.failActive(new DOMException("Aborted", "AbortError"));
      opts.ctx.signal.addEventListener("abort", onAbort, { once: true });
      this.active = active;
      this.activeAbort = onAbort;
      this.connect(opts.ctx)
        .then(() => {
          if (active.done) return;
          this.resetIdle(active);
          const { stream: _stream, ...payload } = opts.body;
          if (!this.socket || this.socket.readyState !== WebSocket.OPEN) {
            throw new Error("Codex WebSocket is not open");
          }
          active.requestSent = true;
          this.socket.send(JSON.stringify({ type: "response.create", ...payload }), (err) => {
            if (err) this.failActive(err instanceof Error ? err : new Error(String(err)));
          });
        })
        .catch((err) => this.failActive(err instanceof Error ? err : new Error(String(err))));
    });
  }

  private connect(ctx: RequestContext): Promise<void> {
    if (this.socket?.readyState === WebSocket.OPEN) return Promise.resolve();
    if (this.connectPromise) return this.connectPromise;
    this.closed = false;
    this.connectPromise = new Promise((resolve, reject) => {
      const socket = new WebSocket(this.wsUrl, { headers: this.requestHeaders });
      this.socket = socket;
      const cleanup = () => {
        this.clearConnectTimer();
        ctx.signal.removeEventListener("abort", onAbort);
        this.connectPromise = undefined;
      };
      const failConnect = (err: Error) => {
        this.closed = true;
        cleanup();
        socket.on("error", () => {});
        socket.terminate();
        reject(err);
      };
      const onAbort = () => failConnect(new DOMException("Aborted", "AbortError"));
      this.connectTimer = setTimeout(
        () => failConnect(new Error("Timed out connecting to Codex WebSocket")),
        this.connectTimeoutMs,
      );
      ctx.signal.addEventListener("abort", onAbort, { once: true });
      socket.on("open", () => {
        cleanup();
        resolve();
      });
      socket.on("message", (data, isBinary) => {
        this.onMessage(data, isBinary);
      });
      socket.on("error", (err) => {
        if (this.connectPromise) failConnect(err instanceof Error ? err : new Error(String(err)));
        else this.failActive(err instanceof Error ? err : new Error(String(err)));
      });
      socket.on("close", (code, reason) => {
        this.closed = true;
        this.onInvalidate?.("close");
        if (this.connectPromise) {
          failConnect(new Error(`Codex WebSocket closed during setup: ${code} ${reason}`));
          return;
        }
        if (this.active) {
          this.failActive(
            new CodexWebSocketSetupError(
              `Codex WebSocket closed before terminal event: ${code} ${reason}`,
              undefined,
              undefined,
              undefined,
              this.active.requestSent,
            ),
          );
        }
      });
    });
    return this.connectPromise;
  }

  private onMessage(data: WebSocket.RawData, isBinary: boolean): void {
    const active = this.active;
    if (!active) return;
    if (isBinary) {
      this.failActive(new Error("Unexpected binary Codex WebSocket frame"));
      return;
    }
    this.resetIdle(active);
    const text = data.toString();
    let event: {
      type?: string;
      status?: number;
      status_code?: number;
      error?: { code?: string; message?: string };
      headers?: Record<string, string>;
    };
    try {
      event = JSON.parse(text);
    } catch (err) {
      this.failActive(err instanceof Error ? err : new Error(String(err)));
      return;
    }
    if (!active.settled && event.type === "error" && isSetupErrorEvent(event)) {
      this.failActive(setupErrorFromEvent(event, active.requestSent));
      return;
    }
    if (isPreviousResponseMissingEvent(event)) {
      this.failActive(setupErrorFromEvent(event, active.requestSent));
      return;
    }
    this.enqueue(active, new TextEncoder().encode(encodeSse(text)));
    if (!active.settled && shouldExposeStream(event)) {
      active.settled = true;
      active.chunksExposed = true;
      active.resolve(active.stream);
    }
    if (isTerminalEvent(event.type)) {
      if (!active.settled) {
        active.settled = true;
        active.chunksExposed = true;
        active.resolve(active.stream);
      }
      this.finishActive();
    }
  }

  private resetIdle(active: ActiveRequest): void {
    this.clearIdle(active);
    active.idleTimer = setTimeout(
      () => this.failActive(new Error("Timed out waiting for Codex WebSocket event")),
      this.idleTimeoutMs,
    );
  }

  private clearIdle(active: ActiveRequest): void {
    if (active.idleTimer) clearTimeout(active.idleTimer);
    active.idleTimer = undefined;
  }

  private clearConnectTimer(): void {
    if (this.connectTimer) clearTimeout(this.connectTimer);
    this.connectTimer = undefined;
  }

  private enqueue(active: ActiveRequest, chunk: Uint8Array): void {
    if (active.controller) active.controller.enqueue(chunk);
    else active.pendingChunks.push(chunk);
  }

  private tearDownActive(active: ActiveRequest | undefined): ActiveRequest | undefined {
    if (!active || active.done) return;
    active.done = true;
    this.clearIdle(active);
    if (this.activeAbort) active.ctx.signal.removeEventListener("abort", this.activeAbort);
    this.activeAbort = undefined;
    this.active = undefined;
    active.releaseQueue();
    return active;
  }

  private finishActive(): void {
    const active = this.tearDownActive(this.active);
    if (!active) return;
    if (!this.keepAlive) this.socket?.close();
    if (active.controller) active.controller.close();
    else active.pendingChunks.push(new Uint8Array());
  }

  private failActive(err: Error): void {
    const active = this.tearDownActive(this.active);
    if (!active) return;
    if (err instanceof CodexWebSocketSetupError) err.requestSent = active.requestSent;
    else if (active.requestSent) err = new CodexWebSocketSetupError(err.message, undefined, undefined, undefined, true);
    if (active.requestSent) {
      this.closed = true;
      this.socket?.on("error", () => {});
      this.socket?.terminate();
      this.onInvalidate?.("error");
    }
    if (!active.settled) active.reject(err);
    else active.controller?.error(err);
  }

}

const POOL_IDLE_TTL_MS = 30 * 60 * 1000;
const MAX_POOL_ENTRIES = 10_000;
const pool = new Map<string, PoolEntry>();

export function clearCodexWebSocketPoolForTests(): void {
  for (const entry of pool.values()) entry.connection.dispose();
  pool.clear();
}

export function invalidateCodexWebSocketPoolKey(poolKey: string | undefined): void {
  if (!poolKey) return;
  const entry = pool.get(poolKey);
  if (!entry) return;
  entry.connection.dispose();
  pool.delete(poolKey);
}

function evictIdlePoolEntries(now = Date.now()): void {
  for (const [key, entry] of pool) {
    if (
      (entry.connection.hasActiveRequest() || now - entry.lastUsed <= POOL_IDLE_TTL_MS) &&
      !entry.connection.isClosed()
    ) {
      continue;
    }
    entry.connection.dispose();
    pool.delete(key);
  }
  while (pool.size > MAX_POOL_ENTRIES) {
    const key = pool.keys().next().value;
    if (!key) return;
    invalidateCodexWebSocketPoolKey(key);
  }
}

export function toWebSocketUrl(url: string): string {
  const parsed = new URL(url);
  if (parsed.protocol === "http:") {
    parsed.protocol = "ws:";
    return parsed.toString();
  }
  if (parsed.protocol === "https:") {
    parsed.protocol = "wss:";
    return parsed.toString();
  }
  throw new Error(`Unsupported Codex WebSocket URL scheme: ${parsed.protocol}`);
}

export function codexWebSocketHeaders(headers: Headers): Record<string, string> {
  const requestHeaders = headersToRecord(headers);
  requestHeaders["openai-beta"] = WEBSOCKET_PROTOCOL_HEADER;
  delete requestHeaders["content-length"];
  return requestHeaders;
}

export async function codexWebSocketRequest(
  opts: CodexWebSocketOptions,
): Promise<ReadableStream<Uint8Array>> {
  const wsUrl = toWebSocketUrl(opts.url);
  const requestHeaders = codexWebSocketHeaders(opts.headers);

  opts.ctx.traffic?.writeJson("022-upstream-websocket-metadata", {
    provider: "codex",
    url: wsUrl,
    headers: requestHeaders,
    poolKey: opts.poolKey,
  });

  if (!opts.poolKey) {
    const connection = new CodexWebSocketConnection(
      wsUrl,
      opts.headers,
      opts.connectTimeoutMs,
      opts.idleTimeoutMs,
      false,
    );
    try {
      return await connection.request(opts);
    } catch (err) {
      connection.dispose();
      throw err;
    }
  }

  evictIdlePoolEntries();
  const existing = pool.get(opts.poolKey);
  if (existing && !existing.connection.isClosed()) {
    existing.lastUsed = Date.now();
    existing.connection.updateHeaders(opts.headers);
    try {
      return await existing.connection.request(opts);
    } catch (err) {
      if (err instanceof CodexWebSocketSetupError || isConnectionInvalidatingError(err)) {
        invalidateCodexWebSocketPoolKey(opts.poolKey);
      }
      throw err;
    }
  }

  if (existing) invalidateCodexWebSocketPoolKey(opts.poolKey);
  const connection = new CodexWebSocketConnection(
    wsUrl,
    opts.headers,
    opts.connectTimeoutMs,
    opts.idleTimeoutMs,
    true,
    () => pool.delete(opts.poolKey!),
  );
  pool.set(opts.poolKey, { connection, lastUsed: Date.now() });
  try {
    return await connection.request(opts);
  } catch (err) {
    if (err instanceof CodexWebSocketSetupError || isConnectionInvalidatingError(err)) {
      invalidateCodexWebSocketPoolKey(opts.poolKey);
    }
    throw err;
  }
}

export function isPreviousResponseMissingError(err: unknown): boolean {
  if (!(err instanceof CodexWebSocketSetupError)) return false;
  return isPreviousResponseMissingMessage(err.message) || err.code === "previous_response_not_found";
}

function shouldExposeStream(event: { type?: string }): boolean {
  const type = event.type;
  return (
    type === "response.output_text.delta" ||
    type === "response.function_call_arguments.delta" ||
    type === "response.completed" ||
    type === "response.incomplete" ||
    type === "response.done" ||
    type === "error"
  );
}

function isSetupErrorEvent(event: {
  status?: number;
  status_code?: number;
  error?: { code?: string; message?: string };
}): boolean {
  const status = event.status ?? event.status_code;
  return status === 401 || status === 403 || status === 429;
}

function isPreviousResponseMissingEvent(event: {
  error?: { code?: string; message?: string };
}): boolean {
  return (
    event.error?.code === "previous_response_not_found" ||
    isPreviousResponseMissingMessage(event.error?.message)
  );
}

function isPreviousResponseMissingMessage(message: string | undefined): boolean {
  if (!message) return false;
  const lower = message.toLowerCase();
  return lower.includes("previous response") && lower.includes("not found");
}

function setupErrorFromEvent(
  event: {
    status?: number;
    status_code?: number;
    error?: { code?: string; message?: string };
    headers?: Record<string, string>;
  },
  requestSent: boolean,
): CodexWebSocketSetupError {
  return new CodexWebSocketSetupError(
    event.error?.message ?? event.error?.code ?? "Codex WebSocket setup error",
    event.status ?? event.status_code,
    event.error?.code,
    event.headers?.["retry-after"],
    requestSent,
  );
}

function isConnectionInvalidatingError(err: unknown): boolean {
  if (!(err instanceof Error)) return false;
  return err.message.includes("closed") || err.message.includes("Timed out");
}

function isTerminalEvent(type: string | undefined): boolean {
  return (
    type === "response.completed" ||
    type === "response.failed" ||
    type === "response.incomplete" ||
    type === "response.done" ||
    type === "error"
  );
}

function encodeSse(text: string): string {
  return `${text
    .split(/\r?\n/)
    .map((line) => `data: ${line}`)
    .join("\n")}\n\n`;
}
