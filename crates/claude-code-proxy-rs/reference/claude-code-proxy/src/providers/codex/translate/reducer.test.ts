import { describe, expect, it } from "bun:test";
import { reduceUpstream } from "./reducer.ts";
import {
  sse,
  silentLog,
  upstreamFromChunks,
  upstreamThatErrorsAfterChunks,
} from "./test-helpers.ts";

async function events(chunks: string[]) {
  return collectEvents(upstreamFromChunks(chunks));
}

async function collectEvents(upstream: ReadableStream<Uint8Array>) {
  const out = [];
  for await (const event of reduceUpstream(upstream, silentLog)) out.push(event);
  return out;
}

describe("reduceUpstream finish metadata", () => {
  it("captures completed response id and assistant text output items", async () => {
    const out = await events([
      sse("response.output_item.added", {
        output_index: 0,
        item: { type: "message", id: "msg_upstream" },
      }),
      sse("response.output_text.delta", { output_index: 0, delta: "hello" }),
      sse("response.output_item.done", {
        output_index: 0,
        item: { type: "message", id: "msg_upstream" },
      }),
      sse("response.completed", { response: { id: "resp_1", usage: { input_tokens: 3 } } }),
    ]);

    expect(out.at(-1)).toEqual({
      kind: "finish",
      stopReason: "end_turn",
      terminalType: "response.completed",
      continuationEligible: true,
      usage: { input_tokens: 3 },
      webSearchRequests: 0,
      responseId: "resp_1",
      outputItems: [
        { type: "message", role: "assistant", content: [{ type: "output_text", text: "hello" }] },
      ],
    });
  });

  it("captures sanitized Read function call arguments", async () => {
    const out = await events([
      sse("response.output_item.added", {
        output_index: 0,
        item: { type: "function_call", call_id: "call_1", name: "Read" },
      }),
      sse("response.function_call_arguments.done", {
        output_index: 0,
        arguments: '{"file_path":"/tmp/a","pages":""}',
      }),
      sse("response.output_item.done", {
        output_index: 0,
        item: {
          type: "function_call",
          call_id: "call_1",
          name: "Read",
          arguments: '{"file_path":"/tmp/a","pages":""}',
        },
      }),
      sse("response.completed", { response: { id: "resp_1", usage: {} } }),
    ]);

    expect(out.at(-1)).toMatchObject({
      kind: "finish",
      stopReason: "tool_use",
      terminalType: "response.completed",
      continuationEligible: true,
      webSearchRequests: 0,
      responseId: "resp_1",
      outputItems: [
        {
          type: "function_call",
          call_id: "call_1",
          name: "Read",
          arguments: '{"file_path":"/tmp/a"}',
        },
      ],
    });
  });

  it("treats hosted web search response events as progress", async () => {
    const out = await events([
      sse("response.output_item.added", {
        output_index: 0,
        item: { type: "web_search_call", id: "ws_1", status: "in_progress" },
      }),
      sse("response.web_search_call.in_progress", { output_index: 0, item_id: "ws_1" }),
      sse("response.web_search_call.searching", { output_index: 0, item_id: "ws_1" }),
      sse("response.web_search_call.completed", { output_index: 0, item_id: "ws_1" }),
      sse("response.output_item.done", {
        output_index: 0,
        item: { type: "web_search_call", id: "ws_1", status: "completed" },
      }),
      sse("response.output_item.added", {
        output_index: 1,
        item: { type: "message", id: "msg_upstream" },
      }),
      sse("response.output_text.delta", { output_index: 1, delta: "result text" }),
      sse("response.output_item.done", {
        output_index: 1,
        item: { type: "message", id: "msg_upstream" },
      }),
      sse("response.completed", { response: { id: "resp_1", usage: { input_tokens: 3 } } }),
    ]);

    expect(out.filter((event) => event.kind === "progress").length).toBeGreaterThanOrEqual(3);
    expect(out).toContainEqual({
      kind: "web-search",
      index: 0,
      resultIndex: 1,
      id: "srvtoolu_ws_1",
      query: "",
    });
    expect(out.at(-1)).toEqual({
      kind: "finish",
      stopReason: "end_turn",
      terminalType: "response.completed",
      continuationEligible: true,
      usage: { input_tokens: 3 },
      webSearchRequests: 1,
      responseId: "resp_1",
      outputItems: [
        {
          type: "message",
          role: "assistant",
          content: [{ type: "output_text", text: "result text" }],
        },
      ],
    });
  });

  it("finishes completed tool calls when the Codex WebSocket closes before a terminal event", async () => {
    const out = await collectEvents(
      upstreamThatErrorsAfterChunks(
        [
          sse("response.output_item.added", {
            output_index: 0,
            item: { type: "function_call", call_id: "call_1", name: "WebSearch" },
          }),
          sse("response.function_call_arguments.done", {
            output_index: 0,
            arguments: '{"query":"claude-code-proxy github"}',
          }),
          sse("response.output_item.done", {
            output_index: 0,
            item: {
              type: "function_call",
              call_id: "call_1",
              name: "WebSearch",
              arguments: '{"query":"claude-code-proxy github"}',
            },
          }),
        ],
        new Error("Codex WebSocket connection closed"),
      ),
    );

    expect(out.at(-1)).toEqual({
      kind: "finish",
      stopReason: "tool_use",
      terminalType: "response.incomplete",
      continuationEligible: false,
      usage: undefined,
      webSearchRequests: 0,
      responseId: undefined,
      outputItems: [
        {
          type: "function_call",
          call_id: "call_1",
          name: "WebSearch",
          arguments: '{"query":"claude-code-proxy github"}',
        },
      ],
    });
  });

  it("marks response.done as continuation eligible when complete", async () => {
    const out = await events([
      sse("response.done", {
        response: {
          id: "resp_1",
          usage: {},
        },
      }),
    ]);

    expect(out.at(-1)).toMatchObject({
      kind: "finish",
      stopReason: "end_turn",
      terminalType: "response.done",
      continuationEligible: true,
      webSearchRequests: 0,
      responseId: "resp_1",
      outputItems: [],
    });
  });

  it("marks incomplete terminals as max tokens and preserves terminal type", async () => {
    const out = await events([
      sse("response.incomplete", {
        response: {
          id: "resp_1",
          status: "incomplete",
          incomplete_details: { reason: "max_output_tokens" },
          usage: {},
        },
      }),
    ]);

    expect(out.at(-1)).toMatchObject({
      kind: "finish",
      stopReason: "max_tokens",
      terminalType: "response.incomplete",
      continuationEligible: false,
      webSearchRequests: 0,
      responseId: "resp_1",
      outputItems: [],
    });
  });
});
