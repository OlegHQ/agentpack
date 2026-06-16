import { encode } from "gpt-tokenizer/model/gpt-4o";
import type { AnthropicRequest } from "../../anthropic/schema.ts";
import { renderCursorPrompt } from "./translate/request.ts";
import { countToolSchemaTokens } from "../shared/tool-schema.ts";

export function countCursorTokens(req: AnthropicRequest): number {
  let total = encode(renderCursorPrompt(req)).length;
  total += countToolSchemaTokens(
    req.tools,
    (tool) => tool.name,
    (tool) => ("description" in tool ? tool.description : undefined),
    (tool) => ("input_schema" in tool ? tool.input_schema : {}),
  );
  return total;
}
