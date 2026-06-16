export type AccumulatedTextBlock = {
  kind: "text";
  index: number;
  text: string;
};

export type AccumulatedThinkingBlock = {
  kind: "thinking";
  index: number;
  text: string;
};

export type AccumulatedToolBlock = {
  kind: "tool";
  index: number;
  id: string;
  name: string;
  args: string;
};

export type AccumulatedBlock =
  | AccumulatedTextBlock
  | AccumulatedThinkingBlock
  | AccumulatedToolBlock;

export interface BlockAccumulator {
  onTextStart(index: number): void;
  onTextDelta(index: number, text: string): boolean;
  onThinkingStart(index: number): void;
  onThinkingDelta(index: number, text: string): boolean;
  onToolStart(index: number, id: string, name: string): void;
  onToolDelta(index: number, partialJson: string): boolean;
  orderedBlocks(): readonly AccumulatedBlock[];
}

export interface AnthropicNonStreamUsage {
  input_tokens: number;
  output_tokens: number;
  cache_creation_input_tokens: number;
  cache_read_input_tokens: number;
  server_tool_use?: {
    web_search_requests?: number;
  };
}

export type AnthropicTextContent = { type: "text"; text: string };
export type AnthropicThinkingContent = { type: "thinking"; thinking: string; signature: string };
export type AnthropicToolUseContent = {
  type: "tool_use";
  id: string;
  name: string;
  input: unknown;
};
export type AnthropicServerToolUseContent = {
  type: "server_tool_use";
  id: string;
  name: "web_search";
  input: { query: string };
};
export type AnthropicWebSearchResultContent = {
  type: "web_search_result";
  title: string;
  url: string;
  page_age?: string | null;
};
export type AnthropicWebSearchToolResultContent = {
  type: "web_search_tool_result";
  tool_use_id: string;
  content: AnthropicWebSearchResultContent[];
};

export type AnthropicNonStreamContent =
  | AnthropicTextContent
  | AnthropicThinkingContent
  | AnthropicToolUseContent
  | AnthropicServerToolUseContent
  | AnthropicWebSearchToolResultContent;

export interface AnthropicNonStreamResponse<ContentType = AnthropicNonStreamContent> {
  id: string;
  type: "message";
  role: "assistant";
  model: string;
  content: Array<ContentType>;
  stop_reason: "end_turn" | "tool_use" | "max_tokens" | null;
  stop_sequence: null;
  usage: AnthropicNonStreamUsage;
}

export interface CachedInputUsage {
  inputTokens?: number;
  outputTokens?: number;
  cachedInputTokens?: number;
}

export type TextToolReducerEvent =
  | { kind: "text-start"; index: number }
  | { kind: "text-delta"; index: number; text: string }
  | { kind: "text-stop"; index: number }
  | { kind: "tool-start"; index: number; id: string; name: string }
  | { kind: "tool-delta"; index: number; partialJson: string }
  | { kind: "tool-stop"; index: number };

export function defaultAnthropicNonStreamUsage(): AnthropicNonStreamUsage {
  return {
    input_tokens: 0,
    output_tokens: 0,
    cache_creation_input_tokens: 0,
    cache_read_input_tokens: 0,
  };
}

export function mapCachedInputUsageToAnthropicUsage(
  usage: CachedInputUsage = {},
): AnthropicNonStreamUsage {
  const inputTokens = usage.inputTokens ?? 0;
  const outputTokens = usage.outputTokens ?? 0;
  const cachedTokens = Math.max(0, usage.cachedInputTokens ?? 0);
  return {
    input_tokens: Math.max(0, inputTokens - cachedTokens),
    output_tokens: outputTokens,
    cache_creation_input_tokens: 0,
    cache_read_input_tokens: cachedTokens,
  };
}

export function collectAnthropicContentFromAccumulatedBlocks<TContent>(
  blocks: readonly AccumulatedBlock[],
  handlers: {
    onThinking?: (index: number, text: string) => TContent | undefined;
    onText: (index: number, text: string) => TContent | undefined;
    onTool: (index: number, id: string, name: string, args: string) => TContent | undefined;
  },
): TContent[] {
  const content: TContent[] = [];
  for (const block of blocks) {
    if (block.kind === "thinking") {
      const item = handlers.onThinking?.(block.index, block.text);
      if (item !== undefined) content.push(item);
      continue;
    }
    if (block.kind === "text") {
      const item = handlers.onText(block.index, block.text);
      if (item !== undefined) content.push(item);
      continue;
    }
    const item = handlers.onTool(block.index, block.id, block.name, block.args);
    if (item !== undefined) content.push(item);
  }
  return content;
}

export function createBlockAccumulator(options?: { includeThinking?: boolean }): BlockAccumulator {
  const ordered: number[] = [];
  const blocks = new Map<number, AccumulatedBlock>();
  const includeThinking = options?.includeThinking === true;

  return {
    onTextStart(index: number): void {
      blocks.set(index, { kind: "text", index, text: "" });
      ordered.push(index);
    },
    onTextDelta(index: number, text: string): boolean {
      const block = blocks.get(index);
      if (block?.kind === "text") {
        block.text += text;
        return true;
      }
      return false;
    },
    onThinkingStart(index: number): void {
      if (!includeThinking) return;
      blocks.set(index, { kind: "thinking", index, text: "" });
      ordered.push(index);
    },
    onThinkingDelta(index: number, text: string): boolean {
      const block = blocks.get(index);
      if (block?.kind === "thinking") {
        block.text += text;
        return true;
      }
      return false;
    },
    onToolStart(index: number, id: string, name: string): void {
      blocks.set(index, { kind: "tool", index, id, name, args: "" });
      ordered.push(index);
    },
    onToolDelta(index: number, partialJson: string): boolean {
      const block = blocks.get(index);
      if (block?.kind === "tool") {
        block.args += partialJson;
        return true;
      }
      return false;
    },
    orderedBlocks(): readonly AccumulatedBlock[] {
      return ordered.flatMap((index) => {
        const block = blocks.get(index);
        return block ? [block] : [];
      });
    },
  };
}

export function parseToolInputJsonOrRaw(args: string): unknown {
  try {
    return args ? JSON.parse(args) : {};
  } catch {
    return { _raw: args };
  }
}
