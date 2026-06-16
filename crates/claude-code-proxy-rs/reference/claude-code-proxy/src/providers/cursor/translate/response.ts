import { encodeSseEvent } from "../../../sse.ts";
import type { Logger } from "../../../log.ts";
import type { TrafficCapture } from "../../types.ts";
import { decodeCursorStream, type CursorStreamEvent, type CursorUsage } from "../client.ts";
import type { CursorProto } from "../proto-loader.ts";
import { createCursorSseFramer } from "../sse-framing.ts";

export interface AnthropicCursorResponse {
  id: string;
  type: "message";
  role: "assistant";
  model: string;
  content: Array<{ type: "thinking"; thinking: string; signature: string } | { type: "text"; text: string }>;
  stop_reason: "end_turn" | null;
  stop_sequence: null;
  usage: AnthropicUsage;
}

export interface AnthropicUsage {
  input_tokens: number;
  output_tokens: number;
  cache_creation_input_tokens: number;
  cache_read_input_tokens: number;
}

export function cursorUsageToAnthropic(usage?: CursorUsage): AnthropicUsage {
  const input = toNumber(usage?.inputTokens);
  const output = toNumber(usage?.outputTokens);
  const cacheRead = toNumber(usage?.cacheReadTokens);
  const cacheWrite = toNumber(usage?.cacheWriteTokens);
  return {
    input_tokens: Math.max(0, input - cacheRead - cacheWrite),
    output_tokens: output,
    cache_creation_input_tokens: cacheWrite,
    cache_read_input_tokens: cacheRead,
  };
}

export async function accumulateCursorResponse(
  upstream: ReadableStream<Uint8Array>,
  opts: {
    messageId: string;
    model: string;
    log: Logger;
    traffic?: TrafficCapture;
    proto?: CursorProto;
    onSession?: (sessionId: string) => void;
  },
): Promise<{ response: AnthropicCursorResponse; cursorSessionId?: string }> {
  let text = "";
  let thinking = "";
  let usage: CursorUsage | undefined;
  let cursorSessionId: string | undefined;

  for await (const event of decodeCursorStream(upstream, opts.proto, { traffic: opts.traffic, log: opts.log })) {
    opts.traffic?.writeJsonEvent("040-cursor-event", event);
    switch (event.type) {
      case "session":
        cursorSessionId = event.sessionId;
        opts.onSession?.(event.sessionId);
        break;
      case "thinking_delta":
        thinking += event.text;
        break;
      case "text_delta":
        text += event.text;
        break;
      case "usage":
        usage = event.usage;
        break;
      case "end":
        break;
    }
  }

  const content: AnthropicCursorResponse["content"] = [];
  if (thinking) content.push({ type: "thinking", thinking, signature: "" });
  if (text) content.push({ type: "text", text });
  opts.log.debug("cursor accumulate finish", {
    textChars: text.length,
    thinkingChars: thinking.length,
    cursorSessionId,
    usage,
  });

  return {
    cursorSessionId,
    response: {
      id: opts.messageId,
      type: "message",
      role: "assistant",
      model: opts.model,
      content,
      stop_reason: "end_turn",
      stop_sequence: null,
      usage: cursorUsageToAnthropic(usage),
    },
  };
}

export function translateCursorStream(
  upstream: ReadableStream<Uint8Array>,
  opts: {
    messageId: string;
    model: string;
    log: Logger;
    signal?: AbortSignal;
    traffic?: TrafficCapture;
    proto?: CursorProto;
    onSession?: (sessionId: string) => void;
  },
): ReadableStream<Uint8Array> {
  const encoder = new TextEncoder();
  return new ReadableStream<Uint8Array>({
    async start(controller) {
      let closed = false;

      const emit = (event: string, data: unknown) => {
        if (closed || opts.signal?.aborted || controller.desiredSize === null) return false;
        opts.traffic?.writeJsonEvent("050-downstream-event", { event, data });
        controller.enqueue(encoder.encode(encodeSseEvent(event, data)));
        return true;
      };

      const framing = createCursorSseFramer({
        messageId: opts.messageId,
        model: opts.model,
        emit,
        mapUsage: cursorUsageToAnthropic,
      });

      try {
        for await (const event of decodeCursorStream(upstream, opts.proto, { traffic: opts.traffic, log: opts.log })) {
          opts.traffic?.writeJsonEvent("040-cursor-event", event);
          if (opts.signal?.aborted) return;
          switch (event.type) {
            case "session":
              opts.onSession?.(event.sessionId);
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
        framing.emitFinalMessage("end_turn");
      } catch (err) {
        opts.log.warn("cursor stream error", { err: String(err) });
        framing.emitError(err);
      } finally {
        closed = true;
        try {
          controller.close();
        } catch {
          // Consumer cancellation can close the controller first.
        }
      }
    },
  });
}

function toNumber(value: string | undefined): number {
  if (!value) return 0;
  const parsed = Number(value);
  return Number.isFinite(parsed) ? parsed : 0;
}
