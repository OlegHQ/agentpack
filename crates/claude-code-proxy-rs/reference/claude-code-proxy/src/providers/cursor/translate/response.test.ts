import { describe, expect, it } from "bun:test";
import { gzipSync } from "node:zlib";
import { decodeCursorStream, encodeConnectFrame } from "../client.ts";
import type { CursorStreamEvent } from "../client.ts";
import {
  collectCursorSse,
  fakeProto,
  frame,
  jsonBytes,
  streamFromChunks,
} from "../cursor-test-helpers.ts";
import {
  accumulateCursorResponse,
  cursorUsageToAnthropic,
  translateCursorStream,
} from "./response.ts";
import { createLogger } from "../../../log.ts";

/** Wraps decodeCursorStream with cancellation tracking. */
async function collectDecodedCursorEvents(
  ...chunks: Uint8Array[]
): Promise<{ events: CursorStreamEvent[]; cancelled: boolean }> {
  let cancelled = false;
  const stream = new ReadableStream<Uint8Array>({
    start(controller) {
      for (const chunk of chunks) controller.enqueue(chunk);
    },
    cancel() {
      cancelled = true;
    },
  });

  const events: CursorStreamEvent[] = [];
  for await (const event of decodeCursorStream(stream, fakeProto)) events.push(event);
  return { events, cancelled };
}

async function translateCursorSse(
  frames: Uint8Array[],
  messageId: string,
): Promise<Array<{ event: string; data: any }>> {
  const downstream = translateCursorStream(
    streamFromChunks(frames),
    {
      messageId,
      model: "cursor-plan",
      log: createLogger("cursor.response.test"),
      proto: fakeProto,
    },
  );
  return collectCursorSse(downstream);
}

describe("Cursor response translation", () => {
  it("maps usage tokens including cache reads and writes", () => {
    expect(
      cursorUsageToAnthropic({
        inputTokens: "100",
        outputTokens: "7",
        cacheReadTokens: "20",
        cacheWriteTokens: "3",
      }),
    ).toEqual({
      input_tokens: 77,
      output_tokens: 7,
      cache_creation_input_tokens: 3,
      cache_read_input_tokens: 20,
    });
  });

  it("decodes Connect-framed Cursor messages including gzip and end frames", async () => {
    const stream = streamFromChunks([
      frame({ interactionUpdate: { textDelta: { text: "hi" } } }),
      encodeConnectFrame(gzipSync(jsonBytes({ interactionUpdate: { textDelta: { text: "!" } } })), 1),
      encodeConnectFrame(jsonBytes({}), 2),
    ]);

    const events = [];
    for await (const event of decodeCursorStream(stream, fakeProto)) events.push(event);

    expect(events).toEqual([
      { type: "text_delta", text: "hi" },
      { type: "text_delta", text: "!" },
      { type: "end" },
    ]);
  });

  it("terminates on Connect end even if the HTTP/2 stream stays open", async () => {
    const { events, cancelled } = await collectDecodedCursorEvents(
      frame({ interactionUpdate: { textDelta: { text: "hi" } } }),
      encodeConnectFrame(jsonBytes({}), 2),
    );

    expect(events).toEqual([{ type: "text_delta", text: "hi" }, { type: "end" }]);
    expect(cancelled).toBe(true);
  });

  it("throws on Cursor Connect end errors", async () => {
    const stream = streamFromChunks([
      encodeConnectFrame(
        gzipSync(
          jsonBytes({
            error: {
              code: "resource_exhausted",
              message: "Error",
              details: [
                {
                  debug: {
                    details: {
                      title: "You've hit your usage limit",
                      additionalInfo: {
                        chatMessage: "You've hit your free requests limit.",
                      },
                    },
                  },
                },
              ],
            },
          }),
        ),
        3,
      ),
    ]);

    let err: unknown;
    try {
      for await (const _event of decodeCursorStream(stream, fakeProto)) {
        // Drain.
      }
    } catch (caught) {
      err = caught;
    }

    expect(String(err)).toContain("resource_exhausted");
    expect(String(err)).toContain("free requests limit");
  });

  it("terminates on turnEnded even if the HTTP/2 stream stays open", async () => {
    const { events, cancelled } = await collectDecodedCursorEvents(
      frame({ interactionUpdate: { textDelta: { text: "hi" } } }),
      frame({ interactionUpdate: { turnEnded: { inputTokens: "4", outputTokens: "1" } } }),
    );

    expect(events).toEqual([
      { type: "text_delta", text: "hi" },
      {
        type: "usage",
        usage: {
          inputTokens: "4",
          outputTokens: "1",
          cacheReadTokens: "0",
          cacheWriteTokens: "0",
        },
      },
      { type: "end" },
    ]);
    expect(cancelled).toBe(true);
  });

  it("terminates after output goes idle without a Cursor terminator", async () => {
    let cancelled = false;
    const stream = new ReadableStream<Uint8Array>({
      start(controller) {
        controller.enqueue(frame({ interactionUpdate: { textDelta: { text: "hi" } } }));
      },
      cancel() {
        cancelled = true;
      },
    });

    const events = [];
    for await (const event of decodeCursorStream(stream, fakeProto, { outputIdleTimeoutMs: 1 })) {
      events.push(event);
    }

    expect(events).toEqual([{ type: "text_delta", text: "hi" }, { type: "end" }]);
    expect(cancelled).toBe(true);
  });

  it("accumulates non-streaming thinking, text, usage, and session id", async () => {
    let observedSession: string | undefined;
    const result = await accumulateCursorResponse(
      streamFromChunks([
        frame({ execServerMessage: { requestContextArgs: { notesSessionId: "cursor-session" } } }),
        frame({ interactionUpdate: { thinkingDelta: { text: "think" } } }),
        frame({ interactionUpdate: { textDelta: { text: "hello" } } }),
        frame({ interactionUpdate: { turnEnded: { inputTokens: "10", outputTokens: "2" } } }),
        encodeConnectFrame(jsonBytes({}), 2),
      ]),
      {
        messageId: "msg_1",
        model: "cursor-plan",
        log: createLogger("cursor.response.test"),
        proto: fakeProto,
        onSession: (session) => {
          observedSession = session;
        },
      },
    );

    expect(observedSession).toBe("cursor-session");
    expect(result.response.content).toEqual([
      { type: "thinking", thinking: "think", signature: "" },
      { type: "text", text: "hello" },
    ]);
    expect(result.response.usage.output_tokens).toBe(2);
  });

  it("emits valid Anthropic SSE for thinking and text deltas", async () => {
    const events = await translateCursorSse(
      [
        frame({ interactionUpdate: { thinkingDelta: { text: "plan" } } }),
        frame({ interactionUpdate: { textDelta: { text: "done" } } }),
        frame({ interactionUpdate: { turnEnded: { inputTokens: "8", outputTokens: "3" } } }),
        encodeConnectFrame(jsonBytes({}), 2),
      ],
      "msg_2",
    );

    expect(events.map((event) => event.event)).toEqual([
      "message_start",
      "ping",
      "content_block_start",
      "content_block_delta",
      "content_block_stop",
      "content_block_start",
      "content_block_delta",
      "content_block_stop",
      "message_delta",
      "message_stop",
    ]);
    expect(events[2]?.data.content_block.type).toBe("thinking");
    expect(events[3]?.data.delta).toEqual({ type: "thinking_delta", thinking: "plan" });
    expect(events[6]?.data.delta).toEqual({ type: "text_delta", text: "done" });
    expect(events[8]?.data.usage.output_tokens).toBe(3);
  });

  it("emits an Anthropic SSE error for Cursor Connect end errors", async () => {
    const events = await translateCursorSse(
      [
        encodeConnectFrame(
          jsonBytes({
            error: {
              code: "resource_exhausted",
              message: "Error",
              details: [
                {
                  debug: {
                    details: {
                      additionalInfo: {
                        chatMessage: "You've hit your free requests limit.",
                      },
                    },
                  },
                },
              ],
            },
          }),
          2,
        ),
      ],
      "msg_error",
    );

    expect(events.map((event) => event.event)).toEqual(["message_start", "ping", "error"]);
    expect(events[2]?.data.error.message).toContain("resource_exhausted");
    expect(events[2]?.data.error.message).toContain("free requests limit");
  });
});
