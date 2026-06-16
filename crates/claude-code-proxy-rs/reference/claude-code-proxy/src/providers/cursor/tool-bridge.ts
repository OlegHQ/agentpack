import { encodeSseEvent } from "../../sse.ts";
import type { AnthropicRequest, AnthropicToolResultBlock } from "../../anthropic/schema.ts";
import type { Logger } from "../../log.ts";
import type { RequestContext } from "../types.ts";
import {
  appendCursorReadResult,
  appendCursorShellStreamResult,
  appendCursorWriteResult,
  buildCursorReadResultFromNativeToolResult,
  buildCursorShellStreamResultFromNativeToolResult,
  buildCursorWriteResultFromNativeToolResult,
  cursorReadArgs,
  cursorShellStreamArgs,
  cursorWriteArgs,
  type CursorNativeToolResult,
  decodeCursorStream,
  type CursorAppendMessage,
  type CursorReadExec,
  type CursorShellStreamExec,
  type CursorStreamEvent,
  type CursorWriteExec,
} from "./client.ts";
import type { CursorProto } from "./proto-loader.ts";
import { cursorUsageToAnthropic } from "./translate/response.ts";
import { createCursorSseFramer } from "./sse-framing.ts";

interface PendingToolBase {
  toolUseId: string;
  startedAt: number;
  append: CursorAppendMessage;
  resolve(result: CursorNativeToolResult): void;
  result: Promise<CursorNativeToolResult>;
}

interface PendingReadTool extends PendingToolBase {
  kind: "Read";
  exec: CursorReadExec;
  path: string;
}

interface PendingShellTool extends PendingToolBase {
  kind: "Bash";
  exec: CursorShellStreamExec;
  command: string;
  workingDirectory: string;
  timeoutMs: number;
}

interface PendingWriteTool extends PendingToolBase {
  kind: "Write";
  exec: CursorWriteExec;
  path: string;
  content: string;
}

type PendingNativeTool = PendingReadTool | PendingShellTool | PendingWriteTool;

interface CursorBridgeState {
  sessionId: string;
  messageId: string;
  model: string;
  iterator: AsyncGenerator<CursorStreamEvent>;
  pendingNext?: Promise<IteratorResult<CursorStreamEvent>>;
  pendingTool?: PendingNativeTool;
  waiters: Array<(tool: PendingNativeTool) => void>;
  log: Logger;
  traffic?: RequestContext["traffic"];
  onSession?: (sessionId: string) => void;
}

const bridgeStates = new Map<string, CursorBridgeState>();

export function canBridgeCursorNativeTools(body: AnthropicRequest, ctx: RequestContext): boolean {
  return Boolean(
    ctx.sessionId && body.stream && body.tools?.some((tool) =>
      tool.name === "Read" || tool.name === "Bash" || tool.name === "Write"
    ),
  );
}

export function canBridgeCursorReadTool(body: AnthropicRequest): boolean {
  return Boolean(body.tools?.some((tool) => tool.name === "Read"));
}

export function canBridgeCursorBashTool(body: AnthropicRequest): boolean {
  return Boolean(body.tools?.some((tool) => tool.name === "Bash"));
}

export function canBridgeCursorWriteTool(body: AnthropicRequest): boolean {
  return Boolean(body.tools?.some((tool) => tool.name === "Write"));
}

export async function denyCursorReadTool(exec: CursorReadExec, append: CursorAppendMessage): Promise<void> {
  await appendCursorReadResult(
    exec,
    {
      success: false,
      error: "Cursor requested Read, but Claude did not advertise the Read tool",
    },
    append,
  );
}

export async function denyCursorBashTool(exec: CursorShellStreamExec, append: CursorAppendMessage): Promise<void> {
  await appendCursorShellStreamResult(
    exec,
    {
      stderr: "Cursor requested Bash, but Claude did not advertise the Bash tool",
      exitCode: 1,
      cwd: cursorShellStreamArgs(exec).workingDirectory,
      localExecutionTimeMs: 0,
    },
    append,
  );
}

export async function denyCursorWriteTool(exec: CursorWriteExec, append: CursorAppendMessage): Promise<void> {
  await appendCursorWriteResult(
    exec,
    {
      success: false,
      error: "Cursor requested Write, but Claude did not advertise the Write tool",
    },
    append,
  );
}

export function createCursorShellToolBridge(opts: {
  sessionId: string;
  messageId: string;
  model: string;
  log: Logger;
  traffic?: RequestContext["traffic"];
  proto?: CursorProto;
  onSession?: (sessionId: string) => void;
}): {
  readHandler: (exec: CursorReadExec, append: CursorAppendMessage) => Promise<void>;
  shellStreamHandler: (exec: CursorShellStreamExec, append: CursorAppendMessage) => Promise<void>;
  writeHandler: (exec: CursorWriteExec, append: CursorAppendMessage) => Promise<void>;
  stream: (upstream: ReadableStream<Uint8Array>, signal?: AbortSignal) => ReadableStream<Uint8Array>;
} {
  const state: CursorBridgeState = {
    sessionId: opts.sessionId,
    messageId: opts.messageId,
    model: opts.model,
    iterator: undefined as unknown as AsyncGenerator<CursorStreamEvent>,
    waiters: [],
    log: opts.log,
    traffic: opts.traffic,
    onSession: opts.onSession,
  };

  const notifyTool = (tool: PendingNativeTool) => {
    state.pendingTool = tool;
    for (const waiter of state.waiters.splice(0)) waiter(tool);
  };

  const createPendingTool = <T extends PendingNativeTool>(
    append: CursorAppendMessage,
    build: (base: {
      toolUseId: string;
      startedAt: number;
      append: CursorAppendMessage;
      resolve(result: CursorNativeToolResult): void;
      result: Promise<CursorNativeToolResult>;
    }) => T,
  ): T => {
    const toolUseId = `call_cursor_${crypto.randomUUID().replace(/-/g, "")}`;
    let resolve!: (result: CursorNativeToolResult) => void;
    const result = new Promise<CursorNativeToolResult>((r) => {
      resolve = r;
    });
    return build({
      toolUseId,
      startedAt: Date.now(),
      append,
      resolve,
      result,
    });
  };

  return {
    async readHandler(exec, append) {
      const { path } = cursorReadArgs(exec);
      const tool = createPendingTool<PendingReadTool>(append, (base) => ({
        kind: "Read",
        exec,
        path,
        ...base,
      }));
      opts.traffic?.writeJsonEvent("038-cursor-tool-bridge-pause", {
        kind: tool.kind,
        toolUseId: tool.toolUseId,
        path,
      });
      notifyTool(tool);
      const readResult = await tool.result;
      await appendCursorReadResult(
        exec,
        buildCursorReadResultFromNativeToolResult(readResult),
        append,
      );
      opts.traffic?.writeJsonEvent("038-cursor-tool-bridge-resume", {
        kind: tool.kind,
        toolUseId: tool.toolUseId,
        isError: readResult.isError,
        contentChars: readResult.content.length,
      });
    },
    async shellStreamHandler(exec, append) {
      const { command, workingDirectory, timeoutMs } = cursorShellStreamArgs(exec);
      const tool = createPendingTool<PendingShellTool>(append, (base) => ({
        kind: "Bash",
        exec,
        command,
        workingDirectory,
        timeoutMs,
        ...base,
      }));
      opts.traffic?.writeJsonEvent("038-cursor-tool-bridge-pause", {
        kind: tool.kind,
        toolUseId: tool.toolUseId,
        command,
        workingDirectory,
        timeoutMs,
      });
      notifyTool(tool);
      const shellResult = await tool.result;
      await appendCursorShellStreamResult(
        exec,
        buildCursorShellStreamResultFromNativeToolResult(
          shellResult,
          tool.startedAt,
          workingDirectory,
        ),
        append,
      );
      opts.traffic?.writeJsonEvent("038-cursor-tool-bridge-resume", {
        kind: tool.kind,
        toolUseId: tool.toolUseId,
        isError: shellResult.isError,
        contentChars: shellResult.content.length,
      });
    },
    async writeHandler(exec, append) {
      const { path, content } = cursorWriteArgs(exec);
      const tool = createPendingTool<PendingWriteTool>(append, (base) => ({
        kind: "Write",
        exec,
        path,
        content,
        ...base,
      }));
      opts.traffic?.writeJsonEvent("038-cursor-tool-bridge-pause", {
        kind: tool.kind,
        toolUseId: tool.toolUseId,
        path,
        contentChars: content.length,
      });
      notifyTool(tool);
      const writeResult = await tool.result;
      await appendCursorWriteResult(exec, buildCursorWriteResultFromNativeToolResult(writeResult), append);
      opts.traffic?.writeJsonEvent("038-cursor-tool-bridge-resume", {
        kind: tool.kind,
        toolUseId: tool.toolUseId,
        isError: writeResult.isError,
        contentChars: writeResult.content.length,
      });
    },
    stream(upstream, signal) {
      state.iterator = decodeCursorStream(upstream, opts.proto, {
        traffic: opts.traffic,
        log: opts.log,
      });
      bridgeStates.set(opts.sessionId, state);
      return streamBridgeUntilToolOrEnd(state, signal);
    },
  };
}

export function resumeCursorShellToolBridge(
  body: AnthropicRequest,
  ctx: RequestContext,
  messageId: string,
): Response | undefined {
  const sessionId = ctx.sessionId;
  if (!sessionId) return undefined;
  const state = bridgeStates.get(sessionId);
  const tool = state?.pendingTool;
  if (!state || !tool) return undefined;
  const result = findToolResult(body, tool.toolUseId);
  if (!result) return undefined;

  state.pendingTool = undefined;
  state.messageId = messageId;
  state.model = body.model;
  tool.resolve({
    content: renderToolResultContent(result.content),
    isError: Boolean(result.is_error),
  });

  const stream = streamBridgeUntilToolOrEnd(state, ctx.signal);
  return new Response(stream, {
    headers: {
      "content-type": "text/event-stream",
      "cache-control": "no-cache",
      "connection": "keep-alive",
    },
  });
}

function streamBridgeUntilToolOrEnd(
  state: CursorBridgeState,
  signal?: AbortSignal,
): ReadableStream<Uint8Array> {
  const encoder = new TextEncoder();
  return new ReadableStream<Uint8Array>({
    async start(controller) {
      let closed = false;

      const emit = (event: string, data: unknown) => {
        if (closed || signal?.aborted || controller.desiredSize === null) return false;
        state.traffic?.writeJsonEvent("050-downstream-event", { event, data });
        controller.enqueue(encoder.encode(encodeSseEvent(event, data)));
        return true;
      };

      const framing = createCursorSseFramer({
        messageId: state.messageId,
        model: state.model,
        emit,
        mapUsage: cursorUsageToAnthropic,
      });

      const emitToolUseAndPause = (tool: PendingNativeTool) => {
        framing.emitToolPauseMessage((index) => {
          const input = toolUseInput(tool);
          emit("content_block_start", {
            type: "content_block_start",
            index,
            content_block: {
              type: "tool_use",
              id: tool.toolUseId,
              name: tool.kind,
              input: {},
            },
          });
          emit("content_block_delta", {
            type: "content_block_delta",
            index,
            delta: { type: "input_json_delta", partial_json: input },
          });
          emit("content_block_stop", { type: "content_block_stop", index });
        });
      };

      const emitMessageEnd = () => {
        framing.emitFinalMessage("end_turn");
      };

      const emitStreamError = (err: unknown) => {
        framing.emitError(err);
      };

      try {
        while (!signal?.aborted) {
          const next = state.pendingNext ?? state.iterator.next();
          state.pendingNext = next;
          const result = await Promise.race([
            next.then((value) => ({ type: "event" as const, value })),
            waitForPendingTool(state).then((tool) => ({ type: "tool" as const, tool })),
          ]);

          if (result.type === "tool") {
            emitToolUseAndPause(result.tool);
            return;
          }

          state.pendingNext = undefined;
          if (result.value.done) break;
          const event = result.value.value;
          state.traffic?.writeJsonEvent("040-cursor-event", event);
          switch (event.type) {
            case "session":
              state.onSession?.(event.sessionId);
              break;
            case "thinking_delta":
              framing.emitThinkingDelta(event.text);
              break;
            case "text_delta":
              framing.emitTextDelta(event.text);
              break;
            case "usage":
              framing.recordUsage(event.usage);
              break;
            case "end":
              break;
          }
        }

        emitMessageEnd();
        bridgeStates.delete(state.sessionId);
      } catch (err) {
        state.log.warn("cursor bridge stream error", { err: String(err) });
        emitStreamError(err);
        bridgeStates.delete(state.sessionId);
      } finally {
        closed = true;
        try {
          controller.close();
        } catch {}
      }
    },
  });
}

function waitForPendingTool(state: CursorBridgeState): Promise<PendingNativeTool> {
  if (state.pendingTool) return Promise.resolve(state.pendingTool);
  return new Promise((resolve) => state.waiters.push(resolve));
}

function findToolResult(body: AnthropicRequest, toolUseId: string): AnthropicToolResultBlock | undefined {
  for (let i = body.messages.length - 1; i >= 0; i--) {
    const message = body.messages[i];
    if (!message || message.role !== "user" || typeof message.content === "string") continue;
    for (const block of message.content) {
      if (block.type === "tool_result" && block.tool_use_id === toolUseId) return block;
    }
  }
  return undefined;
}

function renderToolResultContent(content: AnthropicToolResultBlock["content"]): string {
  if (typeof content === "string") return content;
  return content
    .map((block) => {
      if (block.type === "text") return block.text;
      if (block.type === "image") return "[image result omitted]";
      if (block.type === "thinking") return block.thinking;
      return JSON.stringify(block);
    })
    .join("\n");
}

function toolUseInput(tool: PendingNativeTool): string {
  if (tool.kind === "Read") {
    return JSON.stringify({
      file_path: tool.path,
    });
  }
  if (tool.kind === "Write") {
    return JSON.stringify({
      file_path: tool.path,
      content: tool.content,
    });
  }
  return JSON.stringify({
    command: claudeBashCommand(tool),
    timeout: tool.timeoutMs,
    description: "Run Cursor-requested shell command",
    run_in_background: false,
    dangerouslyDisableSandbox: false,
  });
}

function claudeBashCommand(tool: PendingShellTool): string {
  if (!tool.workingDirectory || tool.workingDirectory === process.cwd()) return tool.command;
  return `cd ${shellQuote(tool.workingDirectory)} && ${tool.command}`;
}

function shellQuote(value: string): string {
  return `'${value.replaceAll("'", "'\\''")}'`;
}
