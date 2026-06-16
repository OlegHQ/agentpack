import { encode } from "gpt-tokenizer/model/gpt-4o";
import type { AnthropicRequest } from "../../anthropic/schema.ts";
import type { ResponsesRequest } from "./translate/request.ts";
import { countAnthropicTokens, IMAGE_TOKEN_ESTIMATE } from "../shared/count-tokens.ts";
import { countToolSchemaTokens } from "../shared/tool-schema.ts";

export function countTokens(req: AnthropicRequest): number {
  return countAnthropicTokens(req, (value) => encode(value).length);
}

export function countTranslatedTokens(
  req: Pick<ResponsesRequest, "instructions" | "input" | "tools" | "text" | "tool_choice">,
): number {
  let total = 0;
  if (req.instructions) total += encode(req.instructions).length;

  for (const item of req.input) {
    if (item.type === "message") {
      for (const part of item.content) {
        if (part.type === "input_text" || part.type === "output_text") {
          total += encode(part.text).length;
        } else if (part.type === "input_image") {
          total += IMAGE_TOKEN_ESTIMATE;
        }
      }
    } else if (item.type === "function_call") {
      total += encode(item.call_id).length;
      total += encode(item.name).length;
      total += encode(item.arguments).length;
    } else if (item.type === "function_call_output") {
      total += encode(item.call_id).length;
      total += encode(item.output).length;
    }
  }

  total += countToolSchemaTokens(
    req.tools,
    (tool) => ("name" in tool ? tool.name : tool.type),
    (tool) => ("description" in tool ? tool.description : undefined),
    (tool) => ("parameters" in tool ? tool.parameters : undefined),
  );

  if (req.text?.format?.type === "json_schema") {
    total += encode(req.text.format.name).length;
    total += encode(JSON.stringify(req.text.format.schema)).length;
  }

  if (typeof req.tool_choice === "string") {
    total += encode(req.tool_choice).length;
  } else if (req.tool_choice) {
    total += encode(req.tool_choice.type).length;
    if ("name" in req.tool_choice) total += encode(req.tool_choice.name).length;
  }

  total += req.input.length * 4;
  return total;
}
