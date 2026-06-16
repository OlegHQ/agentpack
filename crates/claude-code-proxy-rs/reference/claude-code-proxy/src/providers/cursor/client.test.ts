import { describe, expect, it } from "bun:test";
import { mkdtemp, readFile, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { decodeCursorStream, encodeConnectFrame, runCursorAgent } from "./client.ts";
import {
  decodeFrameJson,
  fakeProtoMerged as fakeProto,
  fakeCursorCtx,
  frame,
  jsonBytes,
  streamFromChunks,
} from "./cursor-test-helpers.ts";

type CursorRunOptions = Omit<Parameters<typeof runCursorAgent>[0], "ctx" | "proto" | "openRunStream">;
type CursorClientMessage = Record<string, any>;

function buildRunOptions(overrides: Partial<CursorRunOptions> = {}): CursorRunOptions {
  return {
    prompt: "hello",
    mode: "AGENT_MODE_AGENT",
    conversationId: "conversation",
    model: { modelId: "composer-2.5" },
    auth: { accessToken: "token", source: "test" },
    ...overrides,
  };
}

function buildServerExecFrame(
  id: number,
  execId: string | undefined,
  message: { case: string; value: Record<string, any> },
): Uint8Array {
  const serverMessage = {
    id,
    ...(execId === undefined ? {} : { execId }),
    message,
  };

  return frame({
    message: {
      case: "execServerMessage",
      value: serverMessage,
    },
  });
}

function buildServerKvFrame(
  id: number,
  message: { case: string; value: Record<string, any> },
): Uint8Array {
  return frame({
    message: {
      case: "kvServerMessage",
      value: {
        id,
        message,
      },
    },
  });
}

function buildServerInteractionQueryFrame(
  id: number,
  query: { case: string; value: Record<string, any> },
): Uint8Array {
  return frame({
    message: {
      case: "interactionQuery",
      value: {
        id,
        query,
      },
    },
  });
}

function buildServerStreamCloseFrame(): Uint8Array {
  return encodeConnectFrame(jsonBytes({}), 2);
}

function assertHeartbeatMessage(messages: CursorClientMessage[], index: number) {
  expect(messages[index]).toEqual({ execClientControlMessage: { heartbeat: {} } });
}

function assertStreamCloseMessage(messages: CursorClientMessage[], index: number, id?: number) {
  expect(messages[index]).toEqual({
    execClientControlMessage: {
      streamClose: id === undefined ? {} : { id },
    },
  });
}

async function runCursorAgentWithFrames(
  serverFrames: Uint8Array[],
  runOptions: Partial<CursorRunOptions> = {},
): Promise<{ upstream: ReadableStream<Uint8Array>; sentFrames: Uint8Array[] }> {
  const sentFrames: Uint8Array[] = [];

  const upstream = await runCursorAgent({
    ...buildRunOptions(runOptions),
    ctx: fakeCursorCtx(),
    proto: fakeProto,
    openRunStream: async () => ({
      readable: streamFromChunks(serverFrames),
      status: Promise.resolve({ status: 200 }),
      async write(frame) {
        sentFrames.push(frame);
      },
      close() {},
    }),
  });

  return { upstream, sentFrames };
}

function decodeSentFrames(sentFrames: Uint8Array[]): CursorClientMessage[] {
  return sentFrames.map((frameBytes) => decodeFrameJson(frameBytes) as CursorClientMessage);
}

async function runAndDrainCursorAgentWithFrames(
  serverFrames: Uint8Array[],
  runOptions: Partial<CursorRunOptions> = {},
): Promise<CursorClientMessage[]> {
  const { upstream, sentFrames } = await runCursorAgentWithFrames(serverFrames, runOptions);
  await drain(upstream);
  return decodeSentFrames(sentFrames);
}

describe("Cursor protocol client", () => {
  it("acks exec setup and KV messages on the HTTP/2 Run stream", async () => {
    const { upstream, sentFrames } = await runCursorAgentWithFrames([
      buildServerExecFrame(0, undefined, { case: "requestContextArgs", value: {} }),
      buildServerKvFrame(0, { case: "setBlobArgs", value: {} }),
      buildServerKvFrame(2, { case: "getBlobArgs", value: {} }),
      buildServerStreamCloseFrame(),
    ]);
    await drain(upstream);

    const clientMessages = decodeSentFrames(sentFrames);
    expect(clientMessages[0]?.runRequest.conversationId).toBe("conversation");
    expect(clientMessages[0]?.runRequest.clientSupportsInlineImages).toBe(true);
    assertHeartbeatMessage(clientMessages, 1);
    expect(clientMessages[2]?.execClientMessage.requestContextResult.success.requestContext).toBeDefined();
    expect(clientMessages[2]?.execClientMessage.requestContextResult.success.requestContext.webSearchEnabled).toBe(true);
    expect(clientMessages[2]?.execClientMessage.requestContextResult.success.requestContext.webFetchEnabled).toBe(true);
    assertStreamCloseMessage(clientMessages, 3);
    expect(clientMessages[4]).toEqual({ kvClientMessage: { setBlobResult: {} } });
    expect(clientMessages[5]).toEqual({ kvClientMessage: { getBlobResult: {}, id: 2 } });
  });

  it("sends selected images in the Cursor run request", async () => {
    const { upstream, sentFrames } = await runCursorAgentWithFrames(
      [buildServerStreamCloseFrame()],
      {
        prompt: "describe image",
        selectedImages: [
          {
            data: "aGVsbG8=",
            uuid: "image-id",
            path: "claude-image-1.png",
            mimeType: "image/png",
          },
        ],
      },
    );
    await drain(upstream);

    const clientMessages = decodeSentFrames(sentFrames);
    expect(clientMessages[0]?.runRequest.action.userMessageAction.userMessage.selectedContext).toEqual({
      selectedImages: [
        {
          data: "aGVsbG8=",
          uuid: "image-id",
          path: "claude-image-1.png",
          mimeType: "image/png",
        },
      ],
    });
    expect(clientMessages[0]?.runRequest.clientSupportsInlineImages).toBe(true);
  });

  it("approves Cursor web search and web fetch interaction queries", async () => {
    const { upstream, sentFrames } = await runCursorAgentWithFrames(
      [
        buildServerInteractionQueryFrame(11, {
          case: "webSearchRequestQuery",
          value: { args: { searchTerm: "cursor" } },
        }),
        buildServerInteractionQueryFrame(12, {
          case: "webFetchRequestQuery",
          value: { args: { url: "https://example.com" } },
        }),
        buildServerInteractionQueryFrame(13, { case: "generateImageRequestQuery", value: {} }),
        buildServerStreamCloseFrame(),
      ],
      { prompt: "search the web" },
    );
    await drain(upstream);

    const clientMessages = decodeSentFrames(sentFrames);
    expect(clientMessages).toContainEqual({
      interactionResponse: { id: 11, webSearchRequestResponse: { approved: {} } },
    });
    expect(clientMessages).toContainEqual({
      interactionResponse: { id: 12, webFetchRequestResponse: { approved: {} } },
    });
    expect(clientMessages.some((message) => message.interactionResponse?.id === 13)).toBe(false);
  });

  it("answers Cursor readArgs with file content and closes the exec stream", async () => {
    const dir = await mkdtemp(join(tmpdir(), "cursor-read-"));
    const file = join(dir, "SKILL.md");
    await writeFile(file, "hello\nworld\n", "utf8");

    const clientMessages = await runAndDrainCursorAgentWithFrames([
      buildServerExecFrame(7, "exec-read", {
        case: "readArgs",
        value: { path: file },
      }),
      buildServerStreamCloseFrame(),
    ]);
    assertHeartbeatMessage(clientMessages, 1);
    expect(clientMessages[2]).toEqual({
      execClientMessage: {
        id: 7,
        execId: "exec-read",
        readResult: {
          success: {
            path: file,
            content: "hello\nworld\n",
            totalLines: 3,
            fileSize: "12",
          },
        },
      },
    });
    assertStreamCloseMessage(clientMessages, 3, 7);
  });

  it("answers Cursor writeArgs by writing the file and closing the exec stream", async () => {
    const dir = await mkdtemp(join(tmpdir(), "cursor-write-"));
    const file = join(dir, "history", "findings.md");

    const clientMessages = await runAndDrainCursorAgentWithFrames([
      buildServerExecFrame(10, "exec-write", {
        case: "writeArgs",
        value: {
          path: file,
          fileText: "finding one\nfinding two\n",
          returnFileContentAfterWrite: true,
        },
      }),
      buildServerStreamCloseFrame(),
    ]);
    expect(await readFile(file, "utf8")).toBe("finding one\nfinding two\n");
    assertHeartbeatMessage(clientMessages, 1);
    expect(clientMessages[2]).toEqual({
      execClientMessage: {
        id: 10,
        execId: "exec-write",
        writeResult: {
          success: {
            path: file,
            linesCreated: 3,
            fileSize: 24,
            fileContentAfterWrite: "finding one\nfinding two\n",
          },
        },
      },
    });
    assertStreamCloseMessage(clientMessages, 3, 10);
  });

  it("answers Cursor grepArgs with glob file matches and closes the exec stream", async () => {
    const dir = await mkdtemp(join(tmpdir(), "cursor-grep-"));
    await writeFile(join(dir, "README.md"), "hello\n", "utf8");
    await writeFile(join(dir, "notes.txt"), "hello\n", "utf8");

    const clientMessages = await runAndDrainCursorAgentWithFrames([
      buildServerExecFrame(8, "exec-grep", {
        case: "grepArgs",
        value: {
          pattern: "",
          path: dir,
          glob: "**/README*",
          outputMode: "files_with_matches",
        },
      }),
      buildServerStreamCloseFrame(),
    ]);
    assertHeartbeatMessage(clientMessages, 1);
    expect(clientMessages[2]).toEqual({
      execClientMessage: {
        id: 8,
        execId: "exec-grep",
        grepResult: {
          success: {
            pattern: "**/README*",
            path: dir,
            outputMode: "files_with_matches",
            workspaceResults: {
              [dir]: {
                files: {
                  files: ["README.md"],
                  totalFiles: 1,
                },
              },
            },
          },
        },
      },
    });
    assertStreamCloseMessage(clientMessages, 3, 8);
  });

  it("answers Cursor shellStreamArgs with stream events and closes the exec stream", async () => {
    const { upstream, sentFrames } = await runCursorAgentWithFrames([
      buildServerExecFrame(9, "exec-shell", {
        case: "shellStreamArgs",
        value: {
          command: "printf stdout; printf stderr >&2",
          workingDirectory: process.cwd(),
          timeout: 5000,
        },
      }),
      buildServerStreamCloseFrame(),
    ]);
    let trace = "";
    for await (const event of decodeCursorStream(upstream, fakeProto)) {
      if (event.type === "text_delta") trace += event.text;
    }

    const clientMessages = decodeSentFrames(sentFrames);
    assertHeartbeatMessage(clientMessages, 1);
    expect(clientMessages.some((message) => message.execClientMessage?.shellStream?.start)).toBe(true);
    expect(clientMessages.some((message) => message.execClientMessage?.shellStream?.stdout?.data === "stdout")).toBe(true);
    expect(clientMessages.some((message) => message.execClientMessage?.shellStream?.stderr?.data === "stderr")).toBe(true);
    expect(clientMessages.some((message) => message.execClientMessage?.shellStream?.exit?.code === 0)).toBe(true);
    assertStreamCloseMessage(clientMessages, clientMessages.length - 1, 9);
    expect(trace).toContain("Bash(printf stdout; printf stderr >&2)");
    expect(trace).toContain("stdout");
    expect(trace).toContain("stderr");
  });

  it("closes the Cursor run stream when the downstream consumer cancels", async () => {
    let closeCalls = 0;
    const upstream = await runCursorAgent({
      ...buildRunOptions(),
      ctx: fakeCursorCtx(),
      proto: fakeProto,
      openRunStream: async () => ({
        readable: new ReadableStream<Uint8Array>(),
        status: Promise.resolve({ status: 200 }),
        async write() {},
        close() {
          closeCalls += 1;
        },
      }),
    });

    await upstream.cancel("done");

    expect(closeCalls).toBe(1);
  });

  it("stops Cursor heartbeats when the Run stream closes", async () => {
    const originalSetInterval = globalThis.setInterval;
    const originalClearInterval = globalThis.clearInterval;
    const fakeTimer = Symbol("heartbeat-timer");
    let heartbeatCleared = false;
    let resolveClosed!: () => void;
    const closed = new Promise<void>((resolve) => {
      resolveClosed = resolve;
    });
    let closeCalls = 0;

    globalThis.setInterval = (() => fakeTimer) as unknown as typeof setInterval;
    globalThis.clearInterval = ((timer: unknown) => {
      if (timer === fakeTimer) heartbeatCleared = true;
    }) as typeof clearInterval;

    try {
      await runCursorAgent({
        ...buildRunOptions(),
        ctx: fakeCursorCtx(),
        proto: fakeProto,
        openRunStream: async () => ({
          readable: new ReadableStream<Uint8Array>(),
          status: Promise.resolve({ status: 200 }),
          closed,
          async write() {},
          close() {
            closeCalls += 1;
          },
        }),
      });

      resolveClosed();
      await Promise.resolve();

      expect(heartbeatCleared).toBe(true);
      expect(closeCalls).toBe(1);
    } finally {
      globalThis.setInterval = originalSetInterval;
      globalThis.clearInterval = originalClearInterval;
    }
  });
});

async function drain(stream: ReadableStream<Uint8Array>): Promise<void> {
  const reader = stream.getReader();
  try {
    while (!(await reader.read()).done) {
      // Drain.
    }
  } finally {
    reader.releaseLock();
  }
}
