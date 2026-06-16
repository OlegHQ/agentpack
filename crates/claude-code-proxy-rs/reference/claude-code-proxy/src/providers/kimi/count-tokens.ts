import { encode } from "gpt-tokenizer/model/gpt-4o";
import type { AnthropicRequest } from "../../anthropic/schema.ts";
import type { KimiChatRequest } from "./translate/request.ts";
import {
  countAnthropicTokens,
  IMAGE_TOKEN_ESTIMATE,
  countContentParts,
} from "../shared/count-tokens.ts";
import { countToolSchemaTokens } from "../shared/tool-schema.ts";

// Approximate: Kimi's tokenizer isn't gpt-tokenizer, but Claude Code's
// compaction logic only needs a monotonic estimate, not an exact count.
export function countTokens(req: AnthropicRequest): number {
  return countAnthropicTokens(req, (value) => encode(value).length, true);
}

export function countTranslatedTokens(req: KimiChatRequest): number {
  let total = 0;
  for (const m of req.messages) {
    if (m.role === "system") {
      total += encode(m.content).length;
    } else if (m.role === "user") {
      total += countContentParts(m.content, (v) => encode(v).length);
    } else if (m.role === "assistant") {
      if (typeof m.content === "string") total += encode(m.content).length;
      if (m.reasoning_content) total += encode(m.reasoning_content).length;
      for (const tc of m.tool_calls ?? []) {
        total += encode(tc.function.name).length;
        total += encode(tc.function.arguments).length;
      }
    } else if (m.role === "tool") {
      total += countContentParts(m.content, (v) => encode(v).length);
    }
  }

  total += countToolSchemaTokens(
    req.tools,
    (tool) => tool.function.name,
    (tool) => tool.function.description,
    (tool) => tool.function.parameters,
  );

  total += req.messages.length * 4;
  return total;
}
