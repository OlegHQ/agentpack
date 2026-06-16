import { gunzipSync } from "node:zlib";
import http2 from "node:http2";
import { mkdir, readFile, stat, writeFile } from "node:fs/promises";
import { dirname, isAbsolute, join } from "node:path";
import { cursorBaseUrl, cursorClientVersion } from "../../config.ts";
import type { RequestContext, TrafficCapture } from "../types.ts";
import type { Logger } from "../../log.ts";
import type { CursorProto } from "./proto-loader.ts";
import { loadCursorProto } from "./proto-loader.ts";
import type { CursorAuth } from "./auth/token-store.ts";
import type { CursorSelectedImage } from "./translate/request.ts";

export interface CursorRunOptions {
  prompt: string;
  mode: CursorAgentMode;
  conversationId: string;
  model: CursorModelRequest;
  selectedImages?: CursorSelectedImage[];
  auth: CursorAuth;
  ctx: RequestContext;
  proto?: CursorProto;
  openRunStream?: CursorRunStreamFactory;
  readHandler?: CursorReadHandler;
  shellStreamHandler?: CursorShellStreamHandler;
  writeHandler?: CursorWriteHandler;
}

export type CursorRunStreamFactory = (opts: {
  requestId: string;
  accessToken: string;
  ctx: RequestContext;
}) => Promise<CursorRunStream>;

export interface CursorRunStream {
  readable: ReadableStream<Uint8Array>;
  status: Promise<{ status: number; detail?: string }>;
  closed?: Promise<void>;
  write(frame: Uint8Array): Promise<void>;
  close(): void;
}

export interface CursorShellStreamExec {
  id?: number;
  execId?: string;
  message?: { case?: string; value?: unknown };
}

export interface CursorShellStreamResult {
  stdout?: string;
  stderr?: string;
  exitCode: number;
  cwd?: string;
  aborted?: boolean;
  abortReason?: string;
  localExecutionTimeMs?: number;
}

export type CursorAppendMessage = (messageJson: unknown) => Promise<void>;

export interface CursorReadExec {
  id?: number;
  execId?: string;
  message?: { case?: string; value?: unknown };
}

export interface CursorReadToolResult {
  success: boolean;
  error?: string;
}

export type CursorReadHandler = (
  exec: CursorReadExec,
  append: CursorAppendMessage,
) => Promise<void>;

export type CursorShellStreamHandler = (
  exec: CursorShellStreamExec,
  append: CursorAppendMessage,
) => Promise<void>;

export interface CursorWriteExec {
  id?: number;
  execId?: string;
  message?: { case?: string; value?: unknown };
}

export interface CursorWriteResult {
  success: boolean;
  error?: string;
}

export type CursorWriteHandler = (
  exec: CursorWriteExec,
  append: CursorAppendMessage,
) => Promise<void>;

export type CursorAgentMode = "AGENT_MODE_AGENT" | "AGENT_MODE_PLAN" | "AGENT_MODE_ASK";

export interface CursorModelRequest {
  modelId: string;
  parameters?: Array<{ id: string; value: string }>;
}

export interface CursorUsage {
  inputTokens: string;
  outputTokens: string;
  cacheReadTokens?: string;
  cacheWriteTokens?: string;
}

export type CursorStreamEvent =
  | { type: "session"; sessionId: string }
  | { type: "thinking_delta"; text: string }
  | { type: "text_delta"; text: string }
  | { type: "usage"; usage: CursorUsage }
  | { type: "end" };

export class CursorError extends Error {
  constructor(
    public status: number,
    message: string,
    public detail?: string,
  ) {
    super(message);
    this.name = "CursorError";
  }
}

const HEARTBEAT_INTERVAL_MS = 5_000;
const OUTPUT_IDLE_TIMEOUT_MS = 30_000;

export async function runCursorAgent(opts: CursorRunOptions): Promise<ReadableStream<Uint8Array>> {
  const proto = opts.proto ?? loadCursorProto();
  const requestId = crypto.randomUUID();
  const openRunStream = opts.openRunStream ?? openHttp2RunStream;
  const runUrl = `${cursorBaseUrl().replace(/\/$/, "")}/agent.v1.AgentService/Run`;

  opts.ctx.traffic?.writeJson("020-cursor-run-request", {
    url: runUrl,
    requestId,
    conversationId: opts.conversationId,
    mode: opts.mode,
    model: opts.model,
  });

  const runStream = await openRunStream({
    requestId,
    accessToken: opts.auth.accessToken,
    ctx: opts.ctx,
  });

  let appendQueue = Promise.resolve();
  const append = async (messageJson: unknown) => {
    appendQueue = appendQueue.then(async () => {
      const messageBytes = proto.AgentClientMessage.fromJson(messageJson).toBinary();
      const frame = encodeConnectFrame(messageBytes);
      opts.ctx.traffic?.writeBytes("021-cursor-run-frame", frame);
      await runStream.write(frame);
    });
    await appendQueue;
  };

  await append({
    runRequest: {
      conversationState: {},
      action: {
        userMessageAction: {
          userMessage: {
            text: opts.prompt,
            messageId: crypto.randomUUID(),
            selectedContext: selectedContext(opts.selectedImages),
            mode: opts.mode,
          },
        },
      },
      mcpTools: {},
      conversationId: opts.conversationId,
      requestedModel: opts.model,
      excludeWorkspaceContext: false,
      selectedSubagentModels: selectedSubagentModels(opts.model),
      conversationGroupId: opts.conversationId,
      clientSupportsInlineImages: true,
    },
  });

  const heartbeat = setInterval(() => {
    append({ clientHeartbeat: {} }).catch((err) => {
      opts.ctx.childLogger("cursor.client").warn("cursor heartbeat failed", {
        err: String(err),
      });
    });
  }, HEARTBEAT_INTERVAL_MS);

  let cleanedUp = false;
  const cleanup = () => {
    if (cleanedUp) return;
    cleanedUp = true;
    clearInterval(heartbeat);
    opts.ctx.signal.removeEventListener("abort", cleanup);
    runStream.close();
  };
  opts.ctx.signal.addEventListener("abort", cleanup, { once: true });
  void runStream.closed?.then(cleanup, cleanup);

  const response = await runStream.status;
  if (response.status < 200 || response.status >= 300) {
    cleanup();
    throw new CursorError(response.status, `Cursor AgentService/Run failed with HTTP ${response.status}`, response.detail);
  }

  const transformed = runStream.readable.pipeThrough(
    new TransformStream<Uint8Array, Uint8Array>({
      async transform(chunk, controller) {
        opts.ctx.traffic?.writeBytes("030-cursor-run-response-chunk", chunk);
        controller.enqueue(chunk);
        await processServerControlFrames(
          chunk,
          proto,
          append,
          opts.ctx,
          opts.readHandler,
          opts.shellStreamHandler,
          opts.writeHandler,
          (text) => {
            if (!text || controller.desiredSize === null) return;
            try {
              opts.ctx.traffic?.writeJsonEvent("038-cursor-tool-trace", { text });
              const messageBytes = proto.AgentServerMessage.fromJson({
                interactionUpdate: { textDelta: { text } },
              }).toBinary();
              controller.enqueue(encodeConnectFrame(messageBytes));
            } catch {
              // Downstream cancellation can close the transformed stream while a Cursor exec is still finishing.
            }
          },
        );
      },
      flush() {
        cleanup();
      },
    }),
  );
  return readableWithCleanup(transformed, cleanup);
}

function readableWithCleanup(stream: ReadableStream<Uint8Array>, cleanup: () => void): ReadableStream<Uint8Array> {
  const reader = stream.getReader();
  return new ReadableStream<Uint8Array>({
    async pull(controller) {
      const { done, value } = await reader.read();
      if (done) {
        cleanup();
        controller.close();
        return;
      }
      if (value) controller.enqueue(value);
    },
    async cancel(reason) {
      cleanup();
      await reader.cancel(reason);
    },
  });
}

export async function openHttp2RunStream(opts: {
  requestId: string;
  accessToken: string;
  ctx: RequestContext;
}): Promise<CursorRunStream> {
  const base = new URL(cursorBaseUrl());
  const session = http2.connect(base.origin);
  await new Promise<void>((resolve, reject) => {
    const cleanup = () => {
      session.off("connect", onConnect);
      session.off("error", onError);
    };
    const onConnect = () => {
      cleanup();
      resolve();
    };
    const onError = (err: Error) => {
      cleanup();
      reject(err);
    };
    session.once("connect", onConnect);
    session.once("error", onError);
  });

  let closed = false;
  let responseStatus = 0;
  let errorDetail = "";
  let statusSettled = false;
  let resolveClosed!: () => void;
  let resolveStatus!: (value: { status: number; detail?: string }) => void;
  let rejectStatus!: (err: Error) => void;
  const closedSignal = new Promise<void>((resolve) => {
    resolveClosed = resolve;
  });
  const status = new Promise<{ status: number; detail?: string }>((resolve, reject) => {
    resolveStatus = resolve;
    rejectStatus = reject;
  });
  const settleStatus = (value: { status: number; detail?: string }) => {
    if (statusSettled) return;
    statusSettled = true;
    resolveStatus(value);
  };
  const failStatus = (err: unknown) => {
    if (statusSettled) return;
    statusSettled = true;
    rejectStatus(err instanceof Error ? err : new Error(String(err)));
  };

  const stream = session.request({
    ":method": "POST",
    ":path": `${base.pathname.replace(/\/$/, "")}/agent.v1.AgentService/Run`,
    "authorization": `Bearer ${opts.accessToken}`,
    "content-type": "application/connect+proto",
    "connect-protocol-version": "1",
    "connect-accept-encoding": "gzip,br",
    "user-agent": "connect-es/1.6.1",
    "x-cursor-client-type": "cli",
    "x-cursor-client-version": cursorClientVersion(),
    "x-ghost-mode": "true",
    "x-request-id": opts.requestId,
    "x-original-request-id": opts.requestId,
    "x-cursor-streaming": "true",
    "te": "trailers",
  });

  stream.once("response", (headers) => {
    responseStatus = Number(headers[":status"] ?? 0);
    if (responseStatus >= 200 && responseStatus < 300) {
      settleStatus({ status: responseStatus });
    }
  });

  const readable = new ReadableStream<Uint8Array>({
    start(controller) {
      let readableClosed = false;
      const markClosed = () => {
        closed = true;
        resolveClosed();
      };
      const safeCloseReadable = () => {
        if (readableClosed) return;
        readableClosed = true;
        try {
          controller.close();
        } catch {
          // The downstream consumer may have canceled the ReadableStream first.
        }
      };
      const safeErrorReadable = (err: unknown) => {
        if (readableClosed) return;
        readableClosed = true;
        try {
          controller.error(err);
        } catch {
          // The downstream consumer may have canceled the ReadableStream first.
        }
      };
      stream.on("data", (chunk: Buffer) => {
        if (responseStatus >= 400) {
          errorDetail += chunk.toString("utf8");
          return;
        }
        if (readableClosed) return;
        controller.enqueue(new Uint8Array(chunk));
      });
      stream.once("end", () => {
        if (responseStatus >= 400) {
          settleStatus({ status: responseStatus, detail: errorDetail || undefined });
        }
        safeCloseReadable();
        markClosed();
        session.close();
      });
      stream.once("error", (err) => {
        failStatus(err);
        safeErrorReadable(err);
        markClosed();
        session.destroy();
      });
      stream.once("close", () => {
        if (responseStatus === 0) failStatus(new Error("Cursor HTTP/2 Run stream closed before response headers"));
        safeCloseReadable();
        markClosed();
        session.close();
      });
      session.once("error", (err) => {
        failStatus(err);
        safeErrorReadable(err);
        markClosed();
      });
      session.once("close", markClosed);
    },
    cancel() {
      close();
    },
  });

  const close = () => {
    if (closed) return;
    closed = true;
    resolveClosed();
    stream.close();
    session.close();
  };

  return {
    readable,
    status,
    closed: closedSignal,
    write(frame: Uint8Array) {
      if (closed || stream.destroyed) {
        return Promise.reject(new Error("Cursor HTTP/2 Run stream is closed"));
      }
      return new Promise<void>((resolve, reject) => {
        stream.write(Buffer.from(frame), (err) => {
          if (err) reject(err);
          else resolve();
        });
      });
    },
    close,
  };
}

async function processServerControlFrames(
  chunk: Uint8Array,
  proto: CursorProto,
  append: (messageJson: unknown) => Promise<void>,
  ctx?: RequestContext,
  readHandler?: CursorReadHandler,
  shellStreamHandler?: CursorShellStreamHandler,
  writeHandler?: CursorWriteHandler,
  emitTrace?: (text: string) => void,
): Promise<void> {
  const state = controlFrameState.get(append) ?? {
    buffer: Buffer.alloc(0),
    execHeartbeatSent: false,
    requestContextAcked: false,
  };
  controlFrameState.set(append, state);
  state.buffer = Buffer.concat([state.buffer, Buffer.from(chunk)]);
  while (state.buffer.byteLength >= 5) {
    const flags = state.buffer[0]!;
    const len = state.buffer.readUInt32BE(1);
    if (state.buffer.byteLength < 5 + len) break;
    let payload = state.buffer.subarray(5, 5 + len);
    state.buffer = state.buffer.subarray(5 + len);
    if (flags & 1) payload = gunzipSync(payload);
    if (flags & 2) continue;
    const message = proto.AgentServerMessage.fromBinary(payload) as unknown as CursorOneofMessage;
    const summary = summarizeCursorOneofMessage(message);
    ctx?.traffic?.writeJsonEvent("035-cursor-server-message", summary);
    ctx?.childLogger("cursor.client").debug("cursor server message", summary);
    const oneof = message.message;
    if (oneof?.case === "execServerMessage") {
      if (!state.execHeartbeatSent) {
        state.execHeartbeatSent = true;
        await append({ execClientControlMessage: { heartbeat: {} } });
      }
      if (oneof.value?.message?.case === "requestContextArgs" && !state.requestContextAcked) {
        state.requestContextAcked = true;
        await append({ execClientMessage: buildRequestContextResult(oneof.value) });
        await append({ execClientControlMessage: { streamClose: {} } });
      } else if (oneof.value?.message?.case === "readArgs") {
        if (readHandler) {
          await readHandler(oneof.value, append);
        } else {
          await append({ execClientMessage: await buildReadResult(oneof.value) });
          await append({ execClientControlMessage: { streamClose: { id: oneof.value.id } } });
        }
      } else if (oneof.value?.message?.case === "writeArgs") {
        if (writeHandler) {
          await writeHandler(oneof.value, append);
        } else {
          await append({ execClientMessage: await buildWriteResult(oneof.value) });
          await append({ execClientControlMessage: { streamClose: { id: oneof.value.id } } });
        }
      } else if (oneof.value?.message?.case === "grepArgs") {
        await append({ execClientMessage: await buildGrepResult(oneof.value) });
        await append({ execClientControlMessage: { streamClose: { id: oneof.value.id } } });
      } else if (oneof.value?.message?.case === "shellStreamArgs") {
        if (shellStreamHandler) {
          await shellStreamHandler(oneof.value, append);
        } else {
          await runShellStream(oneof.value, append, emitTrace);
        }
      }
    } else if (oneof?.case === "kvServerMessage") {
      const kv = oneof.value;
      if (kv?.message?.case === "setBlobArgs") {
        const msg: Record<string, unknown> = { setBlobResult: {} };
        if (typeof kv.id === "number" && kv.id !== 0) msg.id = kv.id;
        await append({ kvClientMessage: msg });
      } else if (kv?.message?.case === "getBlobArgs") {
        const msg: Record<string, unknown> = { getBlobResult: {} };
        if (typeof kv.id === "number" && kv.id !== 0) msg.id = kv.id;
        await append({ kvClientMessage: msg });
      }
    } else if (oneof?.case === "interactionQuery") {
      const response = buildInteractionResponse(oneof.value);
      if (response) await append({ interactionResponse: response });
    }
  }
}

export function cursorShellStreamArgs(exec: CursorShellStreamExec): {
  command: string;
  workingDirectory: string;
  timeoutMs: number;
} {
  const args = asRecord(exec.message?.value);
  const command = typeof args?.command === "string" ? args.command : "";
  const workingDirectory = typeof args?.workingDirectory === "string" && args.workingDirectory
    ? args.workingDirectory
    : process.cwd();
  const timeoutMs = typeof args?.timeout === "number" && args.timeout > 0 ? args.timeout : 30_000;
  return { command, workingDirectory, timeoutMs };
}

export function cursorReadArgs(exec: CursorReadExec): {
  path: string;
} {
  const args = asRecord(exec.message?.value);
  const path = typeof args?.path === "string" ? args.path : "";
  return { path };
}

export function cursorWriteArgs(exec: CursorWriteExec): {
  path: string;
  content: string;
  returnFileContentAfterWrite: boolean;
} {
  const args = asRecord(exec.message?.value);
  const path = typeof args?.path === "string" ? args.path : "";
  const rawContent = writeContentFromArgs(args);
  const content = typeof rawContent === "string" ? rawContent : rawContent.toString("utf8");
  const returnFileContentAfterWrite = Boolean(args?.returnFileContentAfterWrite);
  return { path, content, returnFileContentAfterWrite };
}

async function buildReadResult(exec: {
  id?: number;
  execId?: string;
  message?: { case?: string; value?: unknown };
}): Promise<Record<string, unknown>> {
  const args = asRecord(exec.message?.value);
  const requestedPath = typeof args?.path === "string" ? args.path : "";
  let content = "";
  let fileSize = "0";
  let totalLines = 0;
  try {
    content = await readFile(requestedPath, "utf8");
    const fileStat = await stat(requestedPath);
    fileSize = String(fileStat.size);
    totalLines = content.length === 0 ? 0 : content.split("\n").length;
  } catch (err) {
    content = `Error reading ${requestedPath}: ${err instanceof Error ? err.message : String(err)}`;
    fileSize = String(Buffer.byteLength(content, "utf8"));
    totalLines = 1;
  }
  return withExecIds(exec, {
    readResult: {
      success: {
        path: requestedPath,
        content,
        totalLines,
        fileSize,
      },
    },
  });
}

async function buildWriteResult(exec: {
  id?: number;
  execId?: string;
  message?: { case?: string; value?: unknown };
}): Promise<Record<string, unknown>> {
  const args = asRecord(exec.message?.value);
  const requestedPath = typeof args?.path === "string" ? args.path : "";
  const returnContent = Boolean(args?.returnFileContentAfterWrite);
  try {
    if (!requestedPath) throw new Error("write path is required");
    const content = writeContentFromArgs(args);
    await mkdir(dirname(requestedPath), { recursive: true });
    await writeFile(requestedPath, content);
    const textContent = typeof content === "string" ? content : content.toString("utf8");
    const success: Record<string, unknown> = {
      path: requestedPath,
      linesCreated: lineCount(textContent),
      fileSize: Buffer.byteLength(content),
    };
    if (returnContent) success.fileContentAfterWrite = await readFile(requestedPath, "utf8");
    return withExecIds(exec, {
      writeResult: { success },
    });
  } catch (err) {
    return withExecIds(exec, {
      writeResult: {
        error: {
          path: requestedPath,
          error: err instanceof Error ? err.message : String(err),
        },
      },
    });
  }
}

function writeContentFromArgs(args: Record<string, unknown> | undefined): string | Buffer {
  if (typeof args?.fileText === "string") return args.fileText;
  if (typeof args?.file_text === "string") return args.file_text;
  if (typeof args?.fileBytes === "string") return Buffer.from(args.fileBytes, "base64");
  if (typeof args?.file_bytes === "string") return Buffer.from(args.file_bytes, "base64");
  if (args?.fileBytes instanceof Uint8Array) return Buffer.from(args.fileBytes);
  if (args?.file_bytes instanceof Uint8Array) return Buffer.from(args.file_bytes);
  return "";
}

interface CursorNativeExec {
  id?: number;
  execId?: string;
}

function withExecIds(
  exec: CursorNativeExec,
  payload: Record<string, unknown>,
): Record<string, unknown> {
  return {
    ...(typeof exec.id === "number" ? { id: exec.id } : {}),
    ...(typeof exec.execId === "string" ? { execId: exec.execId } : {}),
    ...payload,
  };
}

function lineCount(content: string): number {
  return content.length === 0 ? 0 : content.split("\n").length;
}

export interface CursorNativeToolResult {
  content: string;
  isError: boolean;
}

export function buildCursorReadResultFromNativeToolResult(
  result: CursorNativeToolResult,
): CursorReadToolResult {
  return {
    success: !result.isError,
    error: result.isError ? result.content : undefined,
  };
}

export function buildCursorWriteResultFromNativeToolResult(
  result: CursorNativeToolResult,
): CursorWriteResult {
  if (!result.isError) return { success: true };
  return {
    success: false,
    error: result.content || "Write tool failed",
  };
}

export function buildCursorShellStreamResultFromNativeToolResult(
  result: CursorNativeToolResult,
  startedAt: number,
  cwd: string,
): CursorShellStreamResult {
  return {
    stdout: result.isError ? undefined : result.content,
    stderr: result.isError ? result.content : undefined,
    exitCode: result.isError ? 1 : 0,
    cwd,
    localExecutionTimeMs: Date.now() - startedAt,
  };
}

export async function appendCursorReadResult(
  exec: CursorReadExec,
  result: CursorReadToolResult,
  append: CursorAppendMessage,
): Promise<void> {
  await append({ execClientMessage: await buildReadResultFromTool(exec, result) });
  await append({ execClientControlMessage: { streamClose: { id: exec.id } } });
}

async function buildReadResultFromTool(
  exec: CursorReadExec,
  result: CursorReadToolResult,
): Promise<Record<string, unknown>> {
  if (result.success) return buildReadResult(exec);

  const { path } = cursorReadArgs(exec);
  const content = result.error || `Error reading ${path}`;
  return withExecIds(exec, {
    readResult: {
      success: {
        path,
        content,
        totalLines: lineCount(content),
        fileSize: String(Buffer.byteLength(content, "utf8")),
      },
    },
  });
}

export async function appendCursorWriteResult(
  exec: CursorWriteExec,
  result: CursorWriteResult,
  append: CursorAppendMessage,
): Promise<void> {
  await append({ execClientMessage: await buildWriteResultFromTool(exec, result) });
  await append({ execClientControlMessage: { streamClose: { id: exec.id } } });
}

async function buildWriteResultFromTool(
  exec: CursorWriteExec,
  result: CursorWriteResult,
): Promise<Record<string, unknown>> {
  const { path, returnFileContentAfterWrite } = cursorWriteArgs(exec);
  if (!result.success) {
    return withExecIds(exec, {
      writeResult: {
        error: {
          path,
          error: result.error || "Write tool failed",
        },
      },
    });
  }

  const content = await readFile(path, "utf8");
  const success: Record<string, unknown> = {
    path,
    linesCreated: lineCount(content),
    fileSize: Buffer.byteLength(content),
  };
  if (returnFileContentAfterWrite) success.fileContentAfterWrite = content;
  return withExecIds(exec, {
    writeResult: { success },
  });
}

async function runShellStream(
  exec: {
    id?: number;
    execId?: string;
    message?: { case?: string; value?: unknown };
  },
  append: (messageJson: unknown) => Promise<void>,
  emitTrace?: (text: string) => void,
): Promise<void> {
  const { command, workingDirectory, timeoutMs } = cursorShellStreamArgs(exec);
  const started = Date.now();
  let outputStarted = false;
  const emitOutputTrace = (data: string) => {
    if (!emitTrace || !data) return;
    if (!outputStarted) {
      outputStarted = true;
      emitTrace("\n  \u23bf  ");
    }
    emitTrace(data);
  };
  if (emitTrace && command) {
    emitTrace(`\n\n\u23fa Bash(${command})\n`);
  }
  await append({ execClientMessage: buildShellStream(exec, { start: {} }) });

  let timedOut = false;
  const proc = Bun.spawn(["/bin/sh", "-lc", command], {
    cwd: workingDirectory,
    stdout: "pipe",
    stderr: "pipe",
  });
  const timeout = setTimeout(() => {
    timedOut = true;
    proc.kill("SIGTERM");
  }, timeoutMs);
  try {
    const [exitCode] = await Promise.all([
      proc.exited,
      appendShellOutput(exec, proc.stdout, "stdout", append, emitOutputTrace),
      appendShellOutput(exec, proc.stderr, "stderr", append, emitOutputTrace),
    ]);
    await append({
      execClientMessage: buildShellStream(exec, {
        exit: {
          code: exitCode,
          cwd: workingDirectory,
          aborted: timedOut,
          ...(timedOut ? { abortReason: "ABORT_REASON_TIMEOUT" } : {}),
          localExecutionTimeMs: Date.now() - started,
        },
      }),
    });
  } catch (err) {
    await append({
      execClientMessage: buildShellStream(exec, {
        stderr: {
          data: `Shell execution failed: ${err instanceof Error ? err.message : String(err)}`,
        },
      }),
    });
    emitOutputTrace(`Shell execution failed: ${err instanceof Error ? err.message : String(err)}`);
    await append({
      execClientMessage: buildShellStream(exec, {
        exit: {
          code: 1,
          cwd: workingDirectory,
          localExecutionTimeMs: Date.now() - started,
        },
      }),
    });
  } finally {
    clearTimeout(timeout);
    await append({ execClientControlMessage: { streamClose: { id: exec.id } } });
  }
}

export async function appendCursorShellStreamResult(
  exec: CursorShellStreamExec,
  result: CursorShellStreamResult,
  append: CursorAppendMessage,
): Promise<void> {
  const cwd = result.cwd || cursorShellStreamArgs(exec).workingDirectory;
  await append({ execClientMessage: buildShellStream(exec, { start: {} }) });
  if (result.stdout) {
    await append({ execClientMessage: buildShellStream(exec, { stdout: { data: result.stdout } }) });
  }
  if (result.stderr) {
    await append({ execClientMessage: buildShellStream(exec, { stderr: { data: result.stderr } }) });
  }
  await append({
    execClientMessage: buildShellStream(exec, {
      exit: {
        code: result.exitCode,
        cwd,
        aborted: Boolean(result.aborted),
        ...(result.abortReason ? { abortReason: result.abortReason } : {}),
        localExecutionTimeMs: result.localExecutionTimeMs ?? 0,
      },
    }),
  });
  await append({ execClientControlMessage: { streamClose: { id: exec.id } } });
}

async function appendShellOutput(
  exec: { id?: number; execId?: string },
  stream: ReadableStream<Uint8Array> | null,
  streamName: "stdout" | "stderr",
  append: (messageJson: unknown) => Promise<void>,
  emitTrace?: (data: string) => void,
): Promise<void> {
  if (!stream) return;
  const reader = stream.getReader();
  const decoder = new TextDecoder();
  try {
    while (true) {
      const { value, done } = await reader.read();
      if (done) break;
      if (!value?.byteLength) continue;
      const data = decoder.decode(value, { stream: true });
      emitTrace?.(data);
      if (data) await append({ execClientMessage: buildShellStream(exec, { [streamName]: { data } }) });
    }
    const rest = decoder.decode();
    emitTrace?.(rest);
    if (rest) await append({ execClientMessage: buildShellStream(exec, { [streamName]: { data: rest } }) });
  } finally {
    reader.releaseLock();
  }
}

function buildShellStream(
  exec: { id?: number; execId?: string },
  shellStream: Record<string, unknown>,
): Record<string, unknown> {
  return withExecIds(exec, {
    shellStream,
  });
}

async function buildGrepResult(exec: {
  id?: number;
  execId?: string;
  message?: { case?: string; value?: unknown };
}): Promise<Record<string, unknown>> {
  const args = asRecord(exec.message?.value);
  const path = typeof args?.path === "string" && args.path ? args.path : process.cwd();
  const pattern = typeof args?.pattern === "string" ? args.pattern : "";
  const glob = typeof args?.glob === "string" ? args.glob : "";
  const outputMode = typeof args?.outputMode === "string" && args.outputMode ? args.outputMode : "files_with_matches";
  const resultPattern = pattern || glob;
  let files: string[] = [];
  try {
    if (glob) {
      const globber = new Bun.Glob(glob);
      files = Array.from(globber.scanSync(path)).sort();
    } else if (pattern) {
      files = await grepFilesWithRg(pattern, path);
    }
  } catch (err) {
    return withExecIds(exec, {
      grepResult: {
        error: {
          error: err instanceof Error ? err.message : String(err),
        },
      },
    });
  }
  return withExecIds(exec, {
    grepResult: {
      success: {
        pattern: resultPattern,
        path,
        outputMode,
        workspaceResults: {
          [path]: {
            files: {
              files,
              totalFiles: files.length,
            },
          },
        },
      },
    },
  });
}

async function grepFilesWithRg(pattern: string, path: string): Promise<string[]> {
  const proc = Bun.spawn(["rg", "-l", pattern, path], {
    stdout: "pipe",
    stderr: "ignore",
  });
  const stdout = await new Response(proc.stdout).text();
  const code = await proc.exited;
  if (code !== 0 && code !== 1) {
    throw new Error(`rg exited with status ${code}`);
  }
  return stdout
    .split("\n")
    .map((file) => (isAbsolute(file) ? file : join(path, file)))
    .filter((file) => file.length > 0)
    .sort();
}

function buildRequestContextResult(exec: {
  id?: number;
  execId?: string;
  message?: { case?: string; value?: unknown };
}): Record<string, unknown> {
  return withExecIds(exec, {
    requestContextResult: {
      success: {
        requestContext: {
          env: {
            osVersion: `${process.platform} ${process.arch}`,
            workspacePaths: [process.cwd()],
            shell: process.env.SHELL || "",
            sandboxEnabled: false,
            projectFolder: process.cwd(),
            processWorkingDirectory: process.cwd(),
          },
          webSearchEnabled: true,
          webFetchEnabled: true,
          repositoryInfoComplete: true,
          rulesInfoComplete: true,
          envInfoComplete: true,
          customSubagentsInfoComplete: true,
          mcpFileSystemInfoComplete: true,
          mcpInfoComplete: true,
          gitStatusInfoComplete: true,
          agentSkillsInfoComplete: true,
        },
      },
    },
  });
}

function buildInteractionResponse(query: {
  id?: number;
  query?: { case?: string; value?: unknown };
} | undefined): Record<string, unknown> | undefined {
  if (!query || typeof query.id !== "number") return undefined;
  switch (query.query?.case) {
    case "webSearchRequestQuery":
      return { id: query.id, webSearchRequestResponse: { approved: {} } };
    case "webFetchRequestQuery":
      return { id: query.id, webFetchRequestResponse: { approved: {} } };
    default:
      return undefined;
  }
}

const controlFrameState = new WeakMap<
  (messageJson: unknown) => Promise<void>,
  { buffer: Buffer; execHeartbeatSent: boolean; requestContextAcked: boolean }
>();

interface CursorOneofMessage {
  message?: {
    case?: string;
    value?: {
      id?: number;
      execId?: string;
      message?: { case?: string; value?: unknown };
      query?: { case?: string; value?: unknown };
    };
  };
}

function summarizeCursorOneofMessage(message: CursorOneofMessage): Record<string, unknown> {
  const oneof = message.message;
  const value = oneof?.value;
  return {
    case: oneof?.case ?? "unknown",
    innerCase: value?.message?.case,
    queryCase: value?.query?.case,
    id: value?.id,
    execId: value?.execId,
  };
}

function summarizeCursorServerJson(json: unknown): Record<string, unknown> {
  if (!isRecord(json)) return { type: typeof json };
  const interaction = asRecord(json.interactionUpdate);
  const exec = asRecord(json.execServerMessage);
  const kv = asRecord(json.kvServerMessage);
  return {
    keys: Object.keys(json),
    interactionKeys: interaction ? Object.keys(interaction) : undefined,
    execKeys: exec ? Object.keys(exec) : undefined,
    kvKeys: kv ? Object.keys(kv) : undefined,
  };
}

export async function* decodeCursorStream(
  body: ReadableStream<Uint8Array>,
  proto: CursorProto = loadCursorProto(),
  opts: DecodeCursorStreamOptions = {},
): AsyncGenerator<CursorStreamEvent> {
  let buffer = Buffer.alloc(0);
  const reader = body.getReader();
  let outputSeen = false;
  try {
    while (true) {
      const idleMs = opts.outputIdleTimeoutMs ?? OUTPUT_IDLE_TIMEOUT_MS;
      const read = await readWithOutputIdleTimeout(reader, outputSeen ? idleMs : undefined);
      if (read === "idle") {
        opts.log?.warn("cursor stream idle after output", { idleMs });
        opts.traffic?.writeJsonEvent("040-cursor-event", {
          type: "end",
          reason: "output_idle_timeout",
          idleMs,
        });
        yield { type: "end" };
        await reader.cancel("Cursor output idle timeout");
        return;
      }
      const { value, done } = read;
      if (done) break;
      if (!value?.byteLength) continue;
      buffer = Buffer.concat([buffer, Buffer.from(value)]);
      while (buffer.byteLength >= 5) {
        const flags = buffer[0]!;
        const len = buffer.readUInt32BE(1);
        if (buffer.byteLength < 5 + len) break;
        let payload = buffer.subarray(5, 5 + len);
        buffer = buffer.subarray(5 + len);
        if (flags & 1) payload = gunzipSync(payload);
        if (flags & 2) {
          const connectError = cursorConnectEndError(payload);
          if (connectError) {
            opts.log?.warn("cursor connect end error", {
              status: connectError.status,
              message: connectError.message,
            });
            opts.traffic?.writeJsonEvent("040-cursor-event", {
              type: "error",
              status: connectError.status,
              message: connectError.message,
              detail: connectError.detail,
            });
            throw connectError;
          }
          yield { type: "end" };
          await reader.cancel("Cursor Connect end frame");
          return;
        }
        const decoded = safeToJson(proto.AgentServerMessage.fromBinary(payload));
        if (!decoded) continue;
        opts.traffic?.writeJsonEvent("039-cursor-server-message", summarizeCursorServerJson(decoded));
        for (const event of eventsFromServerMessage(decoded)) {
          if (event.type === "thinking_delta" || event.type === "text_delta" || event.type === "usage") {
            outputSeen = true;
          }
          yield event;
          if (event.type === "end") {
            await reader.cancel("Cursor turnEnded frame");
            return;
          }
        }
      }
    }
  } finally {
    reader.releaseLock();
  }
}

export interface DecodeCursorStreamOptions {
  outputIdleTimeoutMs?: number;
  traffic?: TrafficCapture;
  log?: Logger;
}

async function readWithOutputIdleTimeout(
  reader: { read(): Promise<CursorReadResult> },
  idleMs: number | undefined,
): Promise<CursorReadResult | "idle"> {
  if (!idleMs || idleMs <= 0) return reader.read();
  let timeout: ReturnType<typeof setTimeout> | undefined;
  try {
    return await Promise.race([
      reader.read(),
      new Promise<"idle">((resolve) => {
        timeout = setTimeout(() => resolve("idle"), idleMs);
      }),
    ]);
  } finally {
    if (timeout) clearTimeout(timeout);
  }
}

type CursorReadResult = { done: false; value: Uint8Array } | { done: true; value?: Uint8Array };

function cursorConnectEndError(payload: Buffer): CursorError | undefined {
  if (payload.byteLength === 0) return undefined;
  let parsed: unknown;
  try {
    parsed = JSON.parse(payload.toString("utf8"));
  } catch {
    return undefined;
  }
  const error = asRecord(asRecord(parsed)?.error);
  if (!error) return undefined;
  const code = typeof error.code === "string" ? error.code : "unknown";
  const message = typeof error.message === "string" ? error.message : "Cursor Connect error";
  const details = Array.isArray(error.details) ? error.details : [];
  const debugDetails = details
    .map((detail) => asRecord(asRecord(detail)?.debug))
    .map((debug) => asRecord(debug?.details))
    .find((detail) => detail);
  const additionalInfo = asRecord(debugDetails?.additionalInfo);
  const friendly = stringValue(additionalInfo?.chatMessage) ||
    stringValue(debugDetails?.title) ||
    stringValue(debugDetails?.detail) ||
    message;
  const status = code === "resource_exhausted" ? 429 : 502;
  return new CursorError(status, `Cursor Run failed: ${code}: ${friendly}`, JSON.stringify(parsed));
}

function* eventsFromServerMessage(json: unknown): Generator<CursorStreamEvent> {
  if (!isRecord(json)) return;
  const exec = asRecord(json.execServerMessage);
  const requestContextArgs = asRecord(exec?.requestContextArgs);
  const notesSessionId = requestContextArgs?.notesSessionId;
  if (typeof notesSessionId === "string" && notesSessionId) {
    yield { type: "session", sessionId: notesSessionId };
  }

  const interaction = asRecord(json.interactionUpdate);
  const thinkingDelta = asRecord(interaction?.thinkingDelta)?.text;
  if (typeof thinkingDelta === "string" && thinkingDelta) {
    yield { type: "thinking_delta", text: thinkingDelta };
  }
  const textDelta = asRecord(interaction?.textDelta)?.text;
  if (typeof textDelta === "string" && textDelta) {
    yield { type: "text_delta", text: textDelta };
  }
  const turnEnded = asRecord(interaction?.turnEnded);
  if (turnEnded) {
    yield {
      type: "usage",
      usage: {
        inputTokens: stringToken(turnEnded.inputTokens),
        outputTokens: stringToken(turnEnded.outputTokens),
        cacheReadTokens: stringToken(turnEnded.cacheReadTokens),
        cacheWriteTokens: stringToken(turnEnded.cacheWriteTokens),
      },
    };
    yield { type: "end" };
  }
}

export function encodeConnectFrame(payload: Uint8Array, flags = 0): Uint8Array {
  const out = new Uint8Array(5 + payload.byteLength);
  const view = new DataView(out.buffer);
  view.setUint8(0, flags);
  view.setUint32(1, payload.byteLength, false);
  out.set(payload, 5);
  return out;
}

function selectedContext(images: CursorSelectedImage[] | undefined): Record<string, unknown> {
  if (!images?.length) return {};
  return {
    selectedImages: images.map((image) => ({
      data: image.data,
      uuid: image.uuid,
      path: image.path,
      mimeType: image.mimeType,
    })),
  };
}

function selectedSubagentModels(model: CursorModelRequest): CursorModelRequest[] {
  return [
    { modelId: "default" },
    model,
    {
      modelId: "claude-opus-4-8",
      parameters: [
        { id: "thinking", value: "true" },
        { id: "context", value: "300k" },
        { id: "effort", value: "high" },
        { id: "fast", value: "false" },
      ],
    },
    {
      modelId: "gpt-5.5",
      parameters: [
        { id: "context", value: "272k" },
        { id: "reasoning", value: "extra-high" },
        { id: "fast", value: "false" },
      ],
    },
    {
      modelId: "claude-sonnet-4-6",
      parameters: [
        { id: "thinking", value: "true" },
        { id: "context", value: "200k" },
        { id: "effort", value: "medium" },
      ],
    },
    {
      modelId: "gpt-5.3-codex",
      parameters: [
        { id: "reasoning", value: "medium" },
        { id: "fast", value: "false" },
      ],
    },
  ];
}

function safeToJson(message: { toJson(options?: unknown): unknown }): unknown | undefined {
  try {
    return message.toJson({ emitDefaultValues: false });
  } catch {
    return undefined;
  }
}

function stringToken(value: unknown): string {
  if (typeof value === "string") return value;
  if (typeof value === "number") return String(value);
  return "0";
}

function stringValue(value: unknown): string | undefined {
  return typeof value === "string" && value ? value : undefined;
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return !!value && typeof value === "object" && !Array.isArray(value);
}

function asRecord(value: unknown): Record<string, unknown> | undefined {
  return isRecord(value) ? value : undefined;
}
