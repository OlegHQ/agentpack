import { countToolSchemaTokens } from "./tool-schema.ts";
import {
  flattenSystemText,
  normalizeContent,
  toolResultToString,
} from "../translate/anthropic-content.ts";
import type { AnthropicRequest, AnthropicTool } from "../../anthropic/schema.ts";

export const IMAGE_TOKEN_ESTIMATE = 2000;

export function countContentParts(
  content: string | { type: string; text?: string }[],
  countToken: (text: string) => number,
): number {
  if (typeof content === "string") return countToken(content);
  let total = 0;
  for (const p of content) {
    if (p.type === "text") total += countToken(p.text ?? "");
    else total += IMAGE_TOKEN_ESTIMATE;
  }
  return total;
}

export type AnthropicRequestTokenCounter = (text: string) => number;

export function countAnthropicTokens(
  req: AnthropicRequest,
  countToken: AnthropicRequestTokenCounter,
  includeThinking = false,
): number {
  const base = {
    req,
    countToken,
    tools: req.tools,
    readToolName: (tool: AnthropicTool) => tool.name,
    readToolDescription: (tool: AnthropicTool) =>
      "description" in tool ? tool.description : undefined,
    readToolSchema: (tool: AnthropicTool) => ("input_schema" in tool ? tool.input_schema : {}),
  };
  if (includeThinking) {
    return countAnthropicRequestTokensWithSystem({ ...base, includeThinking: true });
  }
  return countAnthropicRequestTokensWithSystem({ ...base, includeThinking: false });
}

type AnthropicToolReaders<TTool> = {
  readToolName: (tool: TTool) => string;
  readToolDescription: (tool: TTool) => string | undefined;
  readToolSchema: (tool: TTool) => unknown;
};

type CountAnthropicRequestTokenOptions<TTool> = {
  req: AnthropicRequest;
  countToken: AnthropicRequestTokenCounter;
  tools?: TTool[];
} & AnthropicToolReaders<TTool>;

export function countAnthropicRequestTokens<TTool>(
  options: CountAnthropicRequestTokenOptions<TTool> & {
    includeThinking?: false;
  },
): number;

export function countAnthropicRequestTokens<TTool>(
  options: CountAnthropicRequestTokenOptions<TTool> & {
    includeThinking: true;
  },
): number;

export function countAnthropicRequestTokens<TTool>({
  req,
  countToken,
  tools,
  readToolName,
  readToolDescription,
  readToolSchema,
  includeThinking = false,
}: CountAnthropicRequestTokenOptions<TTool> & { includeThinking?: boolean }): number {
  let total = 0;
  for (const msg of req.messages) {
    const blocks = normalizeContent(msg.content);
    for (const block of blocks) {
      if (block.type === "text") {
        total += countToken(block.text);
      } else if (block.type === "image") {
        total += IMAGE_TOKEN_ESTIMATE;
      } else if (block.type === "tool_use") {
        total += countToken(block.name);
        total += countToken(JSON.stringify(block.input ?? {}));
      } else if (block.type === "tool_result") {
        total += countToken(toolResultToString(block.content));
      } else if (includeThinking && block.type === "thinking") {
        total += countToken(block.thinking);
      }
    }
  }

  total += countToolSchemaTokens(tools, readToolName, readToolDescription, readToolSchema);

  total += req.messages.length * 4;
  return total;
}

export function countAnthropicRequestTokensWithSystem<TTool>(
  options: CountAnthropicRequestTokenOptions<TTool> & {
    includeThinking?: false;
  },
): number;

export function countAnthropicRequestTokensWithSystem<TTool>(
  options: CountAnthropicRequestTokenOptions<TTool> & {
    includeThinking: true;
  },
): number;

export function countAnthropicRequestTokensWithSystem<TTool>(
  options: CountAnthropicRequestTokenOptions<TTool> & { includeThinking?: boolean },
): number {
  let total = 0;
  const system = flattenSystemText(options.req.system);
  if (system) total += options.countToken(system);
  total += options.includeThinking
    ? countAnthropicRequestTokens({ ...options, includeThinking: true })
    : countAnthropicRequestTokens({ ...options, includeThinking: false });
  return total;
}
