import { afterEach, describe, expect, it } from "bun:test";
import { cursorProvider, createCursorProvider } from "./index.ts";
import { encodeConnectFrame, runCursorAgent, type CursorRunOptions } from "./client.ts";
import {
  collectCursorSse,
  decodeFrameJson,
  fakeCursorCtx,
  fakeProtoMerged as fakeProto,
  frame,
  jsonBytes,
  jwt,
  resourceExhaustedFrame,
  streamFromChunks,
} from "./cursor-test-helpers.ts";
import { mkdir, mkdtemp, stat, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { expectSseHeaders } from "../../anthropic/response.ts";

const originalToken = process.env.CCP_CURSOR_AUTH_TOKEN;

afterEach(() => {
  if (originalToken === undefined) delete process.env.CCP_CURSOR_AUTH_TOKEN;
  else process.env.CCP_CURSOR_AUTH_TOKEN = originalToken;
});

type CursorSseEvent = (Awaited<ReturnType<typeof collectCursorSse>>)[number];
type CursorProviderOptions = NonNullable<Parameters<typeof createCursorProvider>[0]>;
type CursorRunAgent = CursorProviderOptions["runAgent"];

function createCursorTestProvider(runAgent: CursorRunAgent) {
  return createCursorProvider({
    loadAuth: async () => ({ accessToken: "token", source: "test" }),
    runAgent,
    proto: fakeProto,
  });
}

function createRunStreamHarness(
  initialServerMessage: Record<string, any>,
  onWrite: (
    message: Record<string, any>,
    serverController: ReadableStreamDefaultController<Uint8Array>,
  ) => Promise<void> | void,
) {
  const sentFrames: Array<Record<string, any>> = [];
  let serverController!: ReadableStreamDefaultController<Uint8Array>;
  const serverReadable = new ReadableStream<Uint8Array>({
    start(controller) {
      serverController = controller;
      queueMicrotask(() => {
        controller.enqueue(frame(initialServerMessage));
      });
    },
  });
  return {
    sentFrames,
    runAgent: async (opts: CursorRunOptions) =>
      runCursorAgent({
        ...opts,
        proto: fakeProto,
        openRunStream: async () => ({
          readable: serverReadable,
          status: Promise.resolve({ status: 200 }),
          async write(frameBytes) {
            const message = decodeFrameJson(frameBytes) as Record<string, any>;
            sentFrames.push(message);
            await onWrite(message, serverController);
          },
          close() {},
        }),
      }),
  };
}

function findCursorSseEvent(
  events: readonly CursorSseEvent[],
  eventName: string,
  predicate: (event: CursorSseEvent) => boolean = () => true,
) {
  return events.find((event) => event.event === eventName && predicate(event));
}

function getToolUseStartEvent(events: readonly CursorSseEvent[]) {
  return findCursorSseEvent(
    events,
    "content_block_start",
    (event) => event.data.content_block?.type === "tool_use",
  );
}

function getInputJsonDeltaEvent(events: readonly CursorSseEvent[]) {
  return findCursorSseEvent(
    events,
    "content_block_delta",
    (event) => event.data.delta?.type === "input_json_delta",
  );
}

function getTextDeltaEvent(events: readonly CursorSseEvent[]) {
  return findCursorSseEvent(events, "content_block_delta");
}

function expectMessageStopReason(events: readonly CursorSseEvent[], reason: string) {
  expect(findCursorSseEvent(events, "message_delta")?.data.delta.stop_reason).toBe(reason);
}

function expectMessageStop(events: readonly CursorSseEvent[]) {
  expect(events.at(-1)?.event).toBe("message_stop");
}

function enqueueFinalAssistantResponse(
  serverController: ReadableStreamDefaultController<Uint8Array>,
  text: string,
  inputTokens: string,
  outputTokens: string,
) {
  serverController.enqueue(frame({
    interactionUpdate: { textDelta: { text } },
  }));
  serverController.enqueue(frame({
    interactionUpdate: { turnEnded: { inputTokens, outputTokens } },
  }));
  serverController.close();
}

describe("Cursor provider auth errors", () => {
  it("surfaces expired auth before calling Cursor", async () => {
    process.env.CCP_CURSOR_AUTH_TOKEN = jwt({ exp: 1 });

    const response = await cursorProvider.handleMessages(
      {
        model: "cursor",
        messages: [{ role: "user", content: "hello" }],
      },
      fakeCursorCtx({ sessionId: "session" }),
    );
    const body = (await response.json()) as {
      error: { type: string; message: string };
    };

    expect(response.status).toBe(401);
    expect(response.headers.get("content-type")).toBe("application/json");
    expect(body.error.type).toBe("authentication_error");
    expect(body.error.message).toContain("expired or near expiry");
  });
});

describe("Cursor provider messages", () => {
  it("returns assistant text for non-streaming requests", async () => {
    const provider = createCursorTestProvider(async () => {
      return streamFromChunks([
        frame({ interactionUpdate: { textDelta: { text: "hello" } } }),
        frame({ interactionUpdate: { turnEnded: { inputTokens: "4", outputTokens: "1" } } }),
        encodeConnectFrame(jsonBytes({}), 2),
      ]);
    });

    const response = await provider.handleMessages(
      {
        model: "cursor",
        messages: [{ role: "user", content: "hello" }],
      },
      fakeCursorCtx({ sessionId: "session" }),
    );
    const body = (await response.json()) as { content: Array<{ type: string; text?: string }> };

    expect(response.status).toBe(200);
    expect(response.headers.get("content-type")).toBe("application/json");
    expect(body.content).toEqual([{ type: "text", text: "hello" }]);
  });

  it("returns valid Anthropic SSE for streaming requests", async () => {
    const provider = createCursorTestProvider(async () => {
      return streamFromChunks([
        frame({ interactionUpdate: { textDelta: { text: "streamed" } } }),
        frame({ interactionUpdate: { turnEnded: { inputTokens: "4", outputTokens: "2" } } }),
        encodeConnectFrame(jsonBytes({}), 2),
      ]);
    });

    const response = await provider.handleMessages(
      {
        model: "cursor",
        stream: true,
        messages: [{ role: "user", content: "hello" }],
      },
      fakeCursorCtx({ sessionId: "session" }),
    );
    const events = await collectCursorSse(response);

    expectSseHeaders(response);
    expect(events.map((event) => event.event)).toContain("message_start");
    expect(getTextDeltaEvent(events)?.data.delta.text).toBe("streamed");
    expectMessageStop(events);
  });

  it("preserves Cursor Connect end error status for non-streaming requests", async () => {
    const provider = createCursorTestProvider(async () => {
      return streamFromChunks([
        resourceExhaustedFrame(),
      ]);
    });

    const response = await provider.handleMessages(
      {
        model: "cursor",
        messages: [{ role: "user", content: "hello" }],
      },
      fakeCursorCtx({ sessionId: "session" }),
    );
    const body = (await response.json()) as { error: { message: string } };

    expect(response.status).toBe(429);
    expect(body.error.message).toContain("resource_exhausted");
    expect(body.error.message).toContain("free requests limit");
  });

  it("bridges Cursor shellStreamArgs through Claude Bash tool_use and resumes from tool_result", async () => {
    let finalResponseSent = false;
    const workingDirectory = "/tmp/cursor bridge cwd";
    const { sentFrames, runAgent } = createRunStreamHarness(
      {
        message: {
          case: "execServerMessage",
          value: {
            id: 11,
            execId: "exec-shell",
            message: {
              case: "shellStreamArgs",
              value: {
                command: "printf should-not-run",
                workingDirectory,
                timeout: 5000,
              },
            },
          },
        },
      },
      (message, serverController) => {
        if (message.execClientMessage?.shellStream?.exit && !finalResponseSent) {
          finalResponseSent = true;
          enqueueFinalAssistantResponse(serverController, "resumed after native shell", "4", "3");
        }
      },
    );
    const provider = createCursorTestProvider(runAgent);

    const initial = await provider.handleMessages(
      {
        model: "cursor",
        stream: true,
        tools: [{ name: "Bash", input_schema: { type: "object" } }],
        messages: [{ role: "user", content: "run shell" }],
      },
      fakeCursorCtx({ sessionId: "session" }),
    );
    const initialEvents = await collectCursorSse(initial);
    const toolStart = getToolUseStartEvent(initialEvents);

    expect(toolStart?.data.content_block.name).toBe("Bash");
    expect(toolStart?.data.content_block.id).toStartWith("call_cursor_");
    const toolInputDelta = getInputJsonDeltaEvent(initialEvents);
    expect(JSON.parse(toolInputDelta?.data.delta.partial_json).command).toBe(
      "cd '/tmp/cursor bridge cwd' && printf should-not-run",
    );
    expectMessageStopReason(initialEvents, "tool_use");
    expect(sentFrames.some((message) => message.execClientMessage?.shellStream?.stdout)).toBe(false);

    const resume = await provider.handleMessages(
      {
        model: "cursor",
        stream: true,
        messages: [
          {
            role: "assistant",
            content: [{
              type: "tool_use",
              id: toolStart!.data.content_block.id,
              name: "Bash",
              input: { command: "printf should-not-run" },
            }],
          },
          {
            role: "user",
            content: [{
              type: "tool_result",
              tool_use_id: toolStart!.data.content_block.id,
              content: "native shell output",
              is_error: false,
            }],
          },
        ],
      },
      fakeCursorCtx({ sessionId: "session" }),
    );
    const resumeEvents = await collectCursorSse(resume);

    expect(sentFrames.some((message) =>
      message.execClientMessage?.shellStream?.stdout?.data === "native shell output"
    )).toBe(true);
    expect(sentFrames.some((message) =>
      message.execClientMessage?.shellStream?.stdout?.data === "should-not-run"
    )).toBe(false);
    expect(getTextDeltaEvent(resumeEvents)?.data.delta.text).toBe("resumed after native shell");
    expectMessageStopReason(resumeEvents, "end_turn");
    expectMessageStop(resumeEvents);
  });

  it("denies Cursor shellStreamArgs instead of executing internally when Claude did not advertise Bash", async () => {
    let finalResponseSent = false;
    const dir = await mkdtemp(join(tmpdir(), "cursor-denied-shell-"));
    const file = join(dir, "hidden-edit.txt");
    const { sentFrames, runAgent } = createRunStreamHarness(
      {
        message: {
          case: "execServerMessage",
          value: {
            id: 13,
            execId: "exec-shell-denied",
            message: {
              case: "shellStreamArgs",
              value: {
                command: `printf hidden > ${JSON.stringify(file)}`,
                workingDirectory: dir,
                timeout: 5000,
              },
            },
          },
        },
      },
      (message, serverController) => {
        if (message.execClientMessage?.shellStream?.exit && !finalResponseSent) {
          finalResponseSent = true;
          enqueueFinalAssistantResponse(serverController, "shell was denied", "4", "3");
        }
      },
    );
    const provider = createCursorTestProvider(runAgent);

    const response = await provider.handleMessages(
      {
        model: "cursor",
        stream: true,
        tools: [{ name: "Read", input_schema: { type: "object" } }],
        messages: [{ role: "user", content: "try hidden shell edit" }],
      },
      fakeCursorCtx({ sessionId: "session" }),
    );
    const events = await collectCursorSse(response);

    expect(events.some((event) =>
      event.event === "content_block_start" && event.data.content_block?.name === "Bash"
    )).toBe(false);
    expect(sentFrames.some((message) =>
      String(message.execClientMessage?.shellStream?.stderr?.data ?? "").includes("did not advertise the Bash tool")
    )).toBe(true);
    expect(sentFrames.find((message) => message.execClientMessage?.shellStream?.exit)
      ?.execClientMessage.shellStream.exit.code).toBe(1);
    expect(await exists(file)).toBe(false);
    expect(getTextDeltaEvent(events)?.data.delta.text).toBe("shell was denied");
    expectMessageStopReason(events, "end_turn");
  });

  it("bridges Cursor writeArgs through Claude Write tool_use and resumes from tool_result", async () => {
    let finalResponseSent = false;
    const dir = await mkdtemp(join(tmpdir(), "cursor-write-bridge-"));
    const file = join(dir, "history", "findings.md");
    const fileContent = "finding one\nfinding two\n";
    const { sentFrames, runAgent } = createRunStreamHarness(
      {
        message: {
          case: "execServerMessage",
          value: {
            id: 12,
            execId: "exec-write",
            message: {
              case: "writeArgs",
              value: {
                path: file,
                fileText: fileContent,
                returnFileContentAfterWrite: true,
              },
            },
          },
        },
      },
      (message, serverController) => {
        if (message.execClientMessage?.writeResult?.success && !finalResponseSent) {
          finalResponseSent = true;
          enqueueFinalAssistantResponse(serverController, "resumed after native write", "5", "4");
        }
      },
    );
    const provider = createCursorTestProvider(runAgent);

    const initial = await provider.handleMessages(
      {
        model: "cursor",
        stream: true,
        tools: [{ name: "Write", input_schema: { type: "object" } }],
        messages: [{ role: "user", content: "write file" }],
      },
      fakeCursorCtx({ sessionId: "session" }),
    );
    const initialEvents = await collectCursorSse(initial);
    const toolStart = getToolUseStartEvent(initialEvents);
    const toolInputDelta = getInputJsonDeltaEvent(initialEvents);

    expect(toolStart?.data.content_block.name).toBe("Write");
    expect(toolStart?.data.content_block.id).toStartWith("call_cursor_");
    expect(JSON.parse(toolInputDelta?.data.delta.partial_json)).toEqual({
      file_path: file,
      content: fileContent,
    });
    expectMessageStopReason(initialEvents, "tool_use");
    expect(sentFrames.some((message) => message.execClientMessage?.writeResult)).toBe(false);

    await mkdir(join(dir, "history"), { recursive: true });
    await writeFile(file, fileContent, "utf8");
    const resume = await provider.handleMessages(
      {
        model: "cursor",
        stream: true,
        messages: [
          {
            role: "assistant",
            content: [{
              type: "tool_use",
              id: toolStart!.data.content_block.id,
              name: "Write",
              input: { file_path: file, content: fileContent },
            }],
          },
          {
            role: "user",
            content: [{
              type: "tool_result",
              tool_use_id: toolStart!.data.content_block.id,
              content: `Wrote 3 lines to ${file}`,
              is_error: false,
            }],
          },
        ],
      },
      fakeCursorCtx({ sessionId: "session" }),
    );
    const resumeEvents = await collectCursorSse(resume);
    const writeResult = sentFrames.find((message) => message.execClientMessage?.writeResult)
      ?.execClientMessage.writeResult.success;

    expect(writeResult).toEqual({
      path: file,
      linesCreated: 3,
      fileSize: 24,
      fileContentAfterWrite: fileContent,
    });
    expect(sentFrames.at(-1)).toEqual({ execClientControlMessage: { streamClose: { id: 12 } } });
    expect(getTextDeltaEvent(resumeEvents)?.data.delta.text).toBe("resumed after native write");
    expectMessageStopReason(resumeEvents, "end_turn");
    expectMessageStop(resumeEvents);
  });

  it("bridges Cursor readArgs before writeArgs so Claude Write can edit existing files", async () => {
    let writeRequested = false;
    let finalResponseSent = false;
    const dir = await mkdtemp(join(tmpdir(), "cursor-read-write-bridge-"));
    const file = join(dir, "README.md");
    const originalContent = "# Demo\n\nOld detail.\n";
    const updatedContent = "# Demo\n\nUpdated detail.\n";
    await writeFile(file, originalContent, "utf8");
    const { sentFrames, runAgent } = createRunStreamHarness(
      {
        message: {
          case: "execServerMessage",
          value: {
            id: 21,
            execId: "exec-read",
            message: { case: "readArgs", value: { path: file } },
          },
        },
      },
      (message, serverController) => {
        if (message.execClientMessage?.readResult?.success && !writeRequested) {
          writeRequested = true;
          serverController.enqueue(frame({
            message: {
              case: "execServerMessage",
              value: {
                id: 22,
                execId: "exec-write",
                message: {
                  case: "writeArgs",
                  value: {
                    path: file,
                    fileText: updatedContent,
                    returnFileContentAfterWrite: true,
                  },
                },
              },
            },
          }));
        }
        if (message.execClientMessage?.writeResult?.success && !finalResponseSent) {
          finalResponseSent = true;
          enqueueFinalAssistantResponse(serverController, "edited existing file", "8", "5");
        }
      },
    );
    const provider = createCursorTestProvider(runAgent);

    const initial = await provider.handleMessages(
      {
        model: "cursor",
        stream: true,
        tools: [
          { name: "Read", input_schema: { type: "object" } },
          { name: "Write", input_schema: { type: "object" } },
        ],
        messages: [{ role: "user", content: "edit readme" }],
      },
      fakeCursorCtx({ sessionId: "session" }),
    );
    const initialEvents = await collectCursorSse(initial);
    const readToolStart = getToolUseStartEvent(initialEvents);
    const readToolInput = getInputJsonDeltaEvent(initialEvents);

    expect(readToolStart?.data.content_block.name).toBe("Read");
    expect(JSON.parse(readToolInput?.data.delta.partial_json)).toEqual({ file_path: file });
    expect(sentFrames.some((message) => message.execClientMessage?.readResult)).toBe(false);

    const afterRead = await provider.handleMessages(
      {
        model: "cursor",
        stream: true,
        messages: [
          {
            role: "assistant",
            content: [{
              type: "tool_use",
              id: readToolStart!.data.content_block.id,
              name: "Read",
              input: { file_path: file },
            }],
          },
          {
            role: "user",
            content: [{
              type: "tool_result",
              tool_use_id: readToolStart!.data.content_block.id,
              content: originalContent,
              is_error: false,
            }],
          },
        ],
      },
      fakeCursorCtx({ sessionId: "session" }),
    );
    const afterReadEvents = await collectCursorSse(afterRead);
    const readResult = sentFrames.find((message) => message.execClientMessage?.readResult)
      ?.execClientMessage.readResult.success;
    const writeToolStart = getToolUseStartEvent(afterReadEvents);
    const writeToolInput = getInputJsonDeltaEvent(afterReadEvents);

    expect(readResult).toEqual({
      path: file,
      content: originalContent,
      totalLines: 4,
      fileSize: "20",
    });
    expect(writeToolStart?.data.content_block.name).toBe("Write");
    expect(JSON.parse(writeToolInput?.data.delta.partial_json)).toEqual({
      file_path: file,
      content: updatedContent,
    });

    await writeFile(file, updatedContent, "utf8");
    const afterWrite = await provider.handleMessages(
      {
        model: "cursor",
        stream: true,
        messages: [
          {
            role: "assistant",
            content: [{
              type: "tool_use",
              id: writeToolStart!.data.content_block.id,
              name: "Write",
              input: { file_path: file, content: updatedContent },
            }],
          },
          {
            role: "user",
            content: [{
              type: "tool_result",
              tool_use_id: writeToolStart!.data.content_block.id,
              content: `Wrote 3 lines to ${file}`,
              is_error: false,
            }],
          },
        ],
      },
      fakeCursorCtx({ sessionId: "session" }),
    );
    const afterWriteEvents = await collectCursorSse(afterWrite);
    const writeResult = sentFrames.find((message) => message.execClientMessage?.writeResult)
      ?.execClientMessage.writeResult.success;

    expect(writeResult).toEqual({
      path: file,
      linesCreated: 4,
      fileSize: 24,
      fileContentAfterWrite: updatedContent,
    });
    expect(getTextDeltaEvent(afterWriteEvents)?.data.delta.text).toBe("edited existing file");
    expectMessageStopReason(afterWriteEvents, "end_turn");
  });
});

async function exists(path: string): Promise<boolean> {
  try {
    await stat(path);
    return true;
  } catch {
    return false;
  }
}
