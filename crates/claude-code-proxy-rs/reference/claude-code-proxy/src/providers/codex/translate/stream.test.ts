import { describe, expect, it } from "bun:test";
import { translateStream } from "./stream.ts";
import {
  abortingUpstream,
  collect,
  sse,
  silentLog,
  upstreamFromChunks,
  upstreamThatErrorsAfterChunks,
} from "./test-helpers.ts";

let now = 0;
const realNow = Date.now;

async function withMockNow<T>(run: () => Promise<T>): Promise<T> {
  Date.now = () => now;
  try {
    return await run();
  } finally {
    Date.now = realNow;
    now = 0;
  }
}

function readChunks(deltas: string[], includeDone = true): string[] {
  return [
    sse("response.output_item.added", {
      output_index: 0,
      item: { type: "function_call", call_id: "call_read", name: "Read" },
    }),
    ...deltas.map((delta) =>
      sse("response.function_call_arguments.delta", { output_index: 0, delta }),
    ),
    ...(includeDone
      ? [
          sse("response.output_item.done", {
            output_index: 0,
            item: { type: "function_call", arguments: '{"file_path":"/tmp/a"}' },
          }),
        ]
      : []),
  ];
}

function collectFromChunks(
  chunks: string[],
  options?: {
    advance?: () => void;
    log?: typeof silentLog;
    signal?: AbortSignal;
  },
): Promise<string> {
  const upstream = options?.advance
    ? upstreamFromChunks(chunks, options.advance)
    : upstreamFromChunks(chunks);
  return collect(
    translateStream(upstream, {
      messageId: "msg_1",
      model: "gpt-5.5",
      log: options?.log ?? silentLog,
      ...(options?.signal ? { signal: options.signal } : {}),
    }),
  );
}

function collectWithTime(chunks: string[]): Promise<string> {
  return withMockNow(() =>
    collectFromChunks(chunks, {
      advance: () => {
        now += 16_000;
      },
    }),
  );
}

function webSearchChunks(): string[] {
  return [
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
        "1. **TechRadar security article** - warns about malware.\n   https://www.techradar.com/pro/security/example\n",
    }),
    sse("response.output_text.delta", {
      output_index: 1,
      delta:
        "2. **The Verge GitHub Claude/Codex agents article** - covers agents.\n   https://www.theverge.com/news/873665/github-claude-codex-ai-agents",
    }),
    sse("response.output_item.done", {
      output_index: 1,
      item: { type: "message", id: "msg_upstream" },
    }),
    sse("response.completed", { response: { usage: { input_tokens: 10, output_tokens: 5 } } }),
  ];
}

describe("translateStream", () => {
  it("emits keepalive pings while Read arguments are buffered", async () => {
    const chunks = [
      ...readChunks(['{"file_path"', '":"/tmp/a"}']),
      sse("response.completed", { response: { usage: {} } }),
    ];

    const output = await collectWithTime(chunks);

    expect(output).toContain("event: content_block_start");
    expect(output).toContain("event: content_block_delta");
    expect(output.match(/event: ping/g)?.length).toBeGreaterThanOrEqual(2);
    expect(output).toContain("event: message_stop");
  });

  it("emits keepalive pings for upstream keepalive events", async () => {
    const chunks = [
      sse("response.output_item.added", {
        output_index: 0,
        item: { type: "message", id: "msg_upstream" },
      }),
      sse("keepalive", {}),
      sse("keepalive", {}),
      sse("response.output_item.done", {
        output_index: 0,
        item: { type: "message", id: "msg_upstream" },
      }),
      sse("response.completed", { response: { usage: {} } }),
    ];

    const output = await collectWithTime(chunks);

    expect(output.match(/event: ping/g)?.length).toBeGreaterThanOrEqual(2);
    expect(output).toContain("event: message_stop");
  });

  it("emits an error instead of message_stop when upstream ends with an open Read block", async () => {
    const chunks = readChunks(['{"file_path"', ':"/tmp/a"'], false);

    const output = await collectFromChunks(chunks);

    expect(output).toContain("event: content_block_start");
    expect(output).toContain("event: content_block_stop");
    expect(output).toContain("event: error");
    expect(output).toContain("Upstream stream ended without a terminal response event");
    expect(output).not.toContain("input_json_delta");
    expect(output).not.toContain("event: message_stop");
  });

  it("reports upstream read failures as upstream stream errors", async () => {
    const chunks = readChunks(['{"file_path"'], false);
    const err = new DOMException("The connection was closed.", "AbortError");
    const logs: Array<{ msg: string; fields: unknown }> = [];
    const log = {
      ...silentLog,
      warn: (msg: string, fields: unknown) => logs.push({ msg, fields }),
    };

    const output = await collect(
      translateStream(upstreamThatErrorsAfterChunks(chunks, err), {
        messageId: "msg_1",
        model: "gpt-5.5",
        log,
      }),
    );

    expect(output.indexOf("event: content_block_stop")).toBeLessThan(
      output.indexOf("event: error"),
    );
    expect(output).toContain("event: error");
    expect(output).toContain("Upstream stream read failed: The connection was closed.");
    expect(output).not.toContain("input_json_delta");
    expect(logs.some((entry) => entry.msg === "upstream stream error")).toBe(true);
  });

  it("emits tool_use stop when Codex closes after a completed tool call", async () => {
    const chunks = [
      sse("response.output_item.added", {
        output_index: 0,
        item: { type: "function_call", call_id: "call_search", name: "WebSearch" },
      }),
      sse("response.function_call_arguments.done", {
        output_index: 0,
        arguments: '{"query":"claude-code-proxy github"}',
      }),
      sse("response.output_item.done", {
        output_index: 0,
        item: {
          type: "function_call",
          call_id: "call_search",
          name: "WebSearch",
          arguments: '{"query":"claude-code-proxy github"}',
        },
      }),
    ];

    const output = await collect(
      translateStream(
        upstreamThatErrorsAfterChunks(chunks, new Error("Codex WebSocket connection closed")),
        {
          messageId: "msg_1",
          model: "gpt-5.5",
          log: silentLog,
        },
      ),
    );

    expect(output).toContain("event: content_block_start");
    expect(output).toContain("input_json_delta");
    expect(output).toContain('"stop_reason":"tool_use"');
    expect(output).toContain("event: message_stop");
    expect(output).not.toContain("event: error");
  });

  it("emits Anthropic web search blocks and usage for Codex hosted web search", async () => {
    const output = await collectFromChunks(webSearchChunks());

    expect(output).toContain('"type":"server_tool_use"');
    expect(output).toContain('"id":"srvtoolu_ws_1"');
    expect(output).toContain('"name":"web_search"');
    expect(output).toContain('"partial_json":"{\\"query\\":\\"claude-code-proxy github\\"}"');
    expect(output).toContain('"type":"web_search_tool_result"');
    expect(output).toContain('"tool_use_id":"srvtoolu_ws_1"');
    expect(output).toContain('"title":"TechRadar security article"');
    expect(output).toContain('"url":"https://www.techradar.com/pro/security/example"');
    expect(output).toContain('"web_search_requests":1');
    expect(output).toContain("event: message_stop");
    expect(output).not.toContain("event: error");
    expect(output.indexOf('"type":"web_search_tool_result"')).toBeLessThan(
      output.indexOf('"type":"text_delta"'),
    );
  });

  it("fails buffered Read arguments that exceed the safe duration", async () => {
    const chunks = readChunks(['{"file_path"', ':"/tmp/a"'], false);

    const output = await withMockNow(() =>
      collectFromChunks(chunks, {
        advance: () => {
          now += 121_000;
        },
      }),
    );

    expect(output).toContain("Buffered Read tool arguments exceeded safe limits");
    expect(output).toContain("event: content_block_stop");
    expect(output).toContain("event: error");
    expect(output).not.toContain("input_json_delta");
    expect(output).not.toContain("event: message_stop");
  });

  it("treats aborted upstream reads as cancellation", async () => {
    const abort = new AbortController();
    abort.abort();
    const err = new DOMException("The connection was closed.", "AbortError");

    const output = await collect(
      translateStream(abortingUpstream(err), {
        messageId: "msg_1",
        model: "gpt-5.5",
        log: silentLog,
        signal: abort.signal,
      }),
    );

    expect(output).not.toContain("event: error");
  });
});
