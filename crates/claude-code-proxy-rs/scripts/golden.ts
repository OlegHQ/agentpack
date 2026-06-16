import { readFileSync } from "node:fs";
import { translateRequest } from "../reference/claude-code-proxy/src/providers/codex/translate/request.ts";
import { reduceUpstream } from "../reference/claude-code-proxy/src/providers/codex/translate/reducer.ts";
import { accumulateResponse } from "../reference/claude-code-proxy/src/providers/codex/translate/accumulate.ts";
import { translateStream } from "../reference/claude-code-proxy/src/providers/codex/translate/stream.ts";

const mode = process.argv[2];
const fixture = process.argv[3];

if (!mode || !fixture) {
  console.error("usage: bun scripts/golden.ts <request|reduce|accumulate|sse> <fixture>");
  process.exit(2);
}

const log = {
  debug() {},
  info() {},
  warn() {},
  error() {},
};

function streamFromText(text: string): ReadableStream<Uint8Array> {
  const encoder = new TextEncoder();
  return new ReadableStream<Uint8Array>({
    start(controller) {
      controller.enqueue(encoder.encode(text));
      controller.close();
    },
  });
}

async function collectStream(stream: ReadableStream<Uint8Array>): Promise<string> {
  const reader = stream.getReader();
  const decoder = new TextDecoder();
  let out = "";
  while (true) {
    const { done, value } = await reader.read();
    if (done) break;
    out += decoder.decode(value, { stream: true });
  }
  out += decoder.decode();
  return out;
}

if (mode === "request") {
  const input = JSON.parse(readFileSync(fixture, "utf8"));
  const translated = translateRequest(input, { sessionId: "sess_golden", serviceTier: "priority" });
  console.log(JSON.stringify(translated));
} else if (mode === "reduce") {
  const input = readFileSync(fixture, "utf8");
  const events = [];
  for await (const event of reduceUpstream(streamFromText(input), log as any)) {
    events.push(event);
  }
  console.log(JSON.stringify(events));
} else if (mode === "accumulate") {
  const input = readFileSync(fixture, "utf8");
  const result = await accumulateResponse(streamFromText(input), {
    messageId: "msg_golden",
    model: "gpt-5.4",
    log: log as any,
  });
  console.log(JSON.stringify(result.response));
} else if (mode === "sse") {
  const input = readFileSync(fixture, "utf8");
  const stream = translateStream(streamFromText(input), {
    messageId: "msg_golden",
    model: "gpt-5.4",
    log: log as any,
  });
  console.log(JSON.stringify(await collectStream(stream)));
} else {
  console.error(`unknown mode: ${mode}`);
  process.exit(2);
}
