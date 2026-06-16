import { describe, expect, it } from "bun:test";
import { cursorSelectedImages, renderCursorPrompt } from "./request.ts";
import type { AnthropicRequest } from "../../../anthropic/schema.ts";

describe("Cursor prompt rendering", () => {
  it("renders system, messages, tools, and tool results deterministically", () => {
    const req: AnthropicRequest = {
      model: "cursor",
      system: "Follow instructions.",
      messages: [
        { role: "user", content: "Question" },
        {
          role: "assistant",
          content: [
            { type: "text", text: "Calling tool" },
            { type: "tool_use", id: "toolu_1", name: "Read", input: { file: "package.json" } },
          ],
        },
        {
          role: "user",
          content: [
            {
              type: "tool_result",
              tool_use_id: "toolu_1",
              content: [{ type: "text", text: "package content" }],
            },
          ],
        },
      ],
      tools: [{ name: "Read", input_schema: { type: "object" } }],
    };

    const prompt = renderCursorPrompt(req);

    expect(prompt).toContain("<system>\nFollow instructions.\n</system>");
    expect(prompt).toContain("<user>\nQuestion\n</user>");
    expect(prompt).toContain('<tool_use id="toolu_1" name="Read">');
    expect(prompt).toContain('<tool_result tool_use_id="toolu_1">');
    expect(prompt).toContain('"name":"Read"');
  });

  it("extracts base64 image blocks for Cursor selected context", () => {
    const req: AnthropicRequest = {
      model: "cursor",
      messages: [
        {
          role: "user",
          content: [
            { type: "text", text: "Describe this" },
            { type: "image", source: { type: "base64", media_type: "image/png", data: "aGVsbG8=" } },
            { type: "image", source: { type: "url", url: "https://example.invalid/image.png" } },
          ],
        },
      ],
    };

    const images = cursorSelectedImages(req);

    expect(images).toHaveLength(1);
    expect(images[0]?.data).toBe("aGVsbG8=");
    expect(images[0]?.mimeType).toBe("image/png");
    expect(images[0]?.path).toBe("claude-image-1.png");
    expect(typeof images[0]?.uuid).toBe("string");
  });
});
