import { describe, expect, it } from "bun:test";
import { accumulateResponse } from "./accumulate.ts";
import { sse, silentLog, upstreamFromChunks } from "./test-helpers.ts";

describe("accumulateResponse", () => {
  it("returns Anthropic web search blocks and usage for Codex hosted web search", async () => {
    const result = await accumulateResponse(
      upstreamFromChunks([
        sse("response.output_item.added", {
          output_index: 0,
          item: { type: "web_search_call", id: "ws_1", status: "in_progress" },
        }),
        sse("response.web_search_call.in_progress", { output_index: 0, item_id: "ws_1" }),
        sse("response.web_search_call.searching", { output_index: 0, item_id: "ws_1" }),
        sse("response.web_search_call.completed", { output_index: 0, item_id: "ws_1" }),
        sse("response.output_item.done", {
          output_index: 0,
          item: {
            type: "web_search_call",
            id: "ws_1",
            status: "completed",
            action: {
              type: "search",
              query: "claude-code-proxy github",
              queries: ["claude-code-proxy github"],
            },
          },
        }),
        sse("response.output_item.added", {
          output_index: 1,
          item: { type: "message", id: "msg_upstream" },
        }),
        sse("response.output_text.delta", {
          output_index: 1,
          delta:
            "1. **TechRadar security article** - warns about malware.\n   https://www.techradar.com/pro/security/example",
        }),
        sse("response.output_item.done", {
          output_index: 1,
          item: { type: "message", id: "msg_upstream" },
        }),
        sse("response.completed", { response: { id: "resp_1", usage: { input_tokens: 10 } } }),
      ]),
      { messageId: "msg_1", model: "gpt-5.5", log: silentLog },
    );

    expect(result.response.content).toEqual([
      {
        type: "server_tool_use",
        id: "srvtoolu_ws_1",
        name: "web_search",
        input: { query: "claude-code-proxy github" },
      },
      {
        type: "web_search_tool_result",
        tool_use_id: "srvtoolu_ws_1",
        content: [
          {
            type: "web_search_result",
            title: "TechRadar security article",
            url: "https://www.techradar.com/pro/security/example",
          },
        ],
      },
      {
        type: "text",
        text: "1. **TechRadar security article** - warns about malware.\n   https://www.techradar.com/pro/security/example",
      },
    ]);
    expect(result.response.usage.server_tool_use).toEqual({ web_search_requests: 1 });
  });
});
