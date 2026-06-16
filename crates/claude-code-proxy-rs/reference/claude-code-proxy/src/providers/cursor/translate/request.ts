import type {
  AnthropicContentBlock,
  AnthropicImageBlock,
  AnthropicMessage,
  AnthropicRequest,
  AnthropicTextBlock,
  AnthropicToolResultContentBlock,
} from "../../../anthropic/schema.ts";

export interface CursorSelectedImage {
  data: string;
  uuid: string;
  path: string;
  mimeType: string;
}

export function renderCursorPrompt(req: AnthropicRequest): string {
  const sections: string[] = [];
  const system = renderSystem(req.system);
  if (system) sections.push(`<system>\n${system}\n</system>`);

  for (const message of req.messages) {
    const content = renderContent(message);
    if (content) sections.push(`<${message.role}>\n${content}\n</${message.role}>`);
  }

  if (req.tools?.length) {
    sections.push(
      `<tools>\n${req.tools
        .map((tool) =>
          JSON.stringify({
            name: tool.name,
            description: "description" in tool ? tool.description : undefined,
            input_schema: "input_schema" in tool ? tool.input_schema : {},
          }),
        )
        .join("\n")}\n</tools>`,
    );
  }

  return sections.join("\n\n");
}

export function cursorSelectedImages(req: AnthropicRequest): CursorSelectedImage[] {
  const images: CursorSelectedImage[] = [];
  let index = 0;
  for (const message of req.messages) {
    for (const block of messageBlocks(message)) {
      collectImageBlocks(block, (image) => {
        if (image.source.type !== "base64") return;
        const uuid = crypto.randomUUID();
        const extension = imageExtension(image.source.media_type);
        images.push({
          data: image.source.data,
          uuid,
          path: `claude-image-${++index}.${extension}`,
          mimeType: image.source.media_type,
        });
      });
    }
  }
  return images;
}

function renderSystem(system: AnthropicRequest["system"]): string | undefined {
  if (!system) return undefined;
  const blocks: AnthropicTextBlock[] =
    typeof system === "string" ? [{ type: "text", text: system }] : system;
  const text = blocks
    .filter((block) => block.type === "text" && typeof block.text === "string")
    .map((block) => block.text)
    .filter((line) => !line.startsWith("x-anthropic-billing-header:"))
    .join("\n\n");
  return text || undefined;
}

function renderContent(message: AnthropicMessage): string {
  return messageBlocks(message).map(renderBlock).filter(Boolean).join("\n\n");
}

function renderBlock(block: AnthropicContentBlock): string {
  switch (block.type) {
    case "text":
      return block.text;
    case "thinking":
      return `<thinking>\n${block.thinking}\n</thinking>`;
    case "image":
      if (block.source.type === "url") return `[image: ${block.source.url}]`;
      return `[image: ${block.source.media_type}, ${block.source.data.length} base64 chars]`;
    case "tool_use":
      return `<tool_use id="${block.id}" name="${block.name}">\n${JSON.stringify(block.input ?? {})}\n</tool_use>`;
    case "tool_result":
      return `<tool_result tool_use_id="${block.tool_use_id}"${block.is_error ? ' is_error="true"' : ""}>\n${renderToolResult(block.content)}\n</tool_result>`;
    case "server_tool_use":
      return `<server_tool_use id="${block.id}" name="${block.name}">\n${JSON.stringify(block.input ?? {})}\n</server_tool_use>`;
    case "web_search_tool_result":
      return `<web_search_tool_result tool_use_id="${block.tool_use_id}">\n${JSON.stringify(block.content)}\n</web_search_tool_result>`;
  }
}

function renderToolResult(content: AnthropicToolResultContentBlock[] | string): string {
  if (typeof content === "string") return content;
  return content.map(renderToolResultBlock).filter(Boolean).join("\n\n");
}

function renderToolResultBlock(block: AnthropicToolResultContentBlock): string {
  if (
    block.type === "text" ||
    block.type === "image" ||
    block.type === "tool_use" ||
    block.type === "tool_result" ||
    block.type === "thinking"
  ) {
    return renderBlock(block as AnthropicContentBlock);
  }
  const type = typeof block.type === "string" ? block.type : "unknown";
  return `[unsupported tool result block: ${type}]`;
}

function messageBlocks(message: AnthropicMessage): AnthropicContentBlock[] {
  return typeof message.content === "string"
    ? [{ type: "text", text: message.content }]
    : message.content;
}

function collectImageBlocks(
  block: AnthropicContentBlock,
  visit: (block: AnthropicImageBlock) => void,
): void {
  if (block.type === "image") {
    visit(block);
    return;
  }
  if (block.type !== "tool_result" || typeof block.content === "string") return;
  for (const child of block.content) {
    if (
      child.type === "text" ||
      child.type === "image" ||
      child.type === "tool_use" ||
      child.type === "tool_result" ||
      child.type === "thinking"
    ) {
      collectImageBlocks(child as AnthropicContentBlock, visit);
    }
  }
}

function imageExtension(mediaType: string): string {
  switch (mediaType) {
    case "image/jpeg":
      return "jpg";
    case "image/png":
      return "png";
    case "image/gif":
      return "gif";
    case "image/webp":
      return "webp";
    default:
      return "img";
  }
}
