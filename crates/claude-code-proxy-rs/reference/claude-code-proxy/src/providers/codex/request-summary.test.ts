import { describe, expect, it } from "bun:test";
import { summarizeCodexRequestSize } from "./request-summary.ts";
import type { ResponsesRequest } from "./translate/request.ts";

describe("summarizeCodexRequestSize", () => {
  it("reports inline image byte weight separately", () => {
    const req: ResponsesRequest = {
      model: "gpt-5.5",
      input: [
        {
          type: "message",
          role: "user",
          content: [
            { type: "input_text", text: "look" },
            { type: "input_image", image_url: `data:image/png;base64,${"a".repeat(20)}` },
          ],
        },
        {
          type: "message",
          role: "user",
          content: [
            { type: "input_image", image_url: "https://example.invalid/image.png" },
            { type: "input_image", image_url: `data:image/png;base64,${"b".repeat(40)}` },
          ],
        },
      ],
      store: false,
      stream: true,
    };

    const summary = summarizeCodexRequestSize(req);

    expect(summary.inputImagePartCount).toBe(3);
    expect(summary.inputImageDataUrlBytes).toBe("data:image/png;base64,".length * 2 + 20 + 40);
    expect(summary.largestInputImages).toEqual([
      expect.objectContaining({ itemIndex: 1, partIndex: 1, dataUrl: true }),
      expect.objectContaining({ itemIndex: 0, partIndex: 1, dataUrl: true }),
      expect.objectContaining({ itemIndex: 1, partIndex: 0, dataUrl: false }),
    ]);
  });
});
