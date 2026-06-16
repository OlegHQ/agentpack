import type {
  AnthropicContentBlock,
  AnthropicImageBlock,
  AnthropicMessage,
  AnthropicRequest,
  AnthropicTextBlock,
  AnthropicToolResultContentBlock,
} from "../../anthropic/schema.ts";

const ANTHROPIC_EFFORTS = new Set(["low", "medium", "high", "max"]);

export function assertValidEffort(effort: unknown): void {
  if (effort !== undefined && !ANTHROPIC_EFFORTS.has(effort as string)) {
    throw new Error(
      `Invalid output_config.effort: ${JSON.stringify(effort)}. Must be one of: ${Array.from(ANTHROPIC_EFFORTS).join(", ")}`,
    );
  }
}

export type AnthropicToolChoice = "auto" | "none" | "required" | { type: "function"; name: string };

export function mapToolChoice(
  choice?: AnthropicRequest["tool_choice"],
): AnthropicToolChoice {
  if (!choice) return "auto";
  switch (choice.type) {
    case "auto":
      return "auto";
    case "none":
      return "none";
    case "any":
      return "required";
    case "tool":
      return choice.name ? { type: "function", name: choice.name } : "required";
  }
}

export function flattenSystemText(system: AnthropicRequest["system"]): string | undefined {
  if (!system) return undefined;
  const blocks: AnthropicTextBlock[] =
    typeof system === "string" ? [{ type: "text", text: system }] : system;
  const texts = blocks
    .filter((b) => b && b.type === "text" && typeof b.text === "string")
    .map((b) => b.text)
    .filter((t) => !t.startsWith("x-anthropic-billing-header:"));
  if (!texts.length) return undefined;
  return texts.join("\n\n");
}

export function normalizeContent(content: AnthropicMessage["content"]): AnthropicContentBlock[] {
  if (typeof content === "string") return [{ type: "text", text: content }];
  return content;
}

export function imageBlockToUrl(block: Extract<AnthropicContentBlock, { type: "image" }>): string {
  if (block.source.type === "url") return block.source.url;
  return `data:${block.source.media_type};base64,${block.source.data}`;
}

export function unsupportedToolResultBlockToString(
  block: AnthropicToolResultContentBlock,
): string {
  const type = typeof block.type === "string" ? block.type : "unknown";
  return `[unsupported content block omitted: ${type}]`;
}

export function isToolResultTextBlock(
  block: AnthropicToolResultContentBlock,
): block is AnthropicTextBlock {
  return block.type === "text" && typeof block.text === "string";
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return !!value && typeof value === "object";
}

export function isToolResultImageBlock(
  block: AnthropicToolResultContentBlock,
): block is AnthropicImageBlock {
  if (block.type !== "image") return false;
  const source = block.source;
  if (!isRecord(source)) return false;
  if (source.type === "url") return typeof source.url === "string";
  return (
    source.type === "base64" &&
    typeof source.media_type === "string" &&
    typeof source.data === "string"
  );
}

export function toolResultToString(content: string | AnthropicToolResultContentBlock[]): string {
  if (typeof content === "string") return content;
  return content
    .map((b) => {
      if (isToolResultTextBlock(b)) return b.text;
      if (isToolResultImageBlock(b)) {
        const mt = b.source.type === "base64" ? b.source.media_type : "url";
        return `[image omitted: ${mt}]`;
      }
      return unsupportedToolResultBlockToString(b);
    })
    .join("\n");
}
