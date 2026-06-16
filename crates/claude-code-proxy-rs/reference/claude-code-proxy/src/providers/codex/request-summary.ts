import type { ResponsesInputItem, ResponsesRequest } from "./translate/request.ts";

const encoder = new TextEncoder();

export interface CodexRequestSizeSummary {
  bodyJsonBytes: number;
  instructionsBytes: number;
  inputJsonBytes: number;
  toolsJsonBytes: number;
  textJsonBytes: number;
  reasoningJsonBytes: number;
  includeJsonBytes: number;
  clientMetadataJsonBytes: number;
  inputItemCount: number;
  toolCount: number;
  inputImagePartCount: number;
  inputImageDataUrlBytes: number;
  inputTypeCounts: Record<string, number>;
  roleCounts: Record<string, number>;
  largestInputItems: Array<{ index: number; type: string; role?: string; jsonBytes: number }>;
  largestInputImages: Array<{
    itemIndex: number;
    partIndex: number;
    jsonBytes: number;
    imageUrlBytes: number;
    dataUrl: boolean;
  }>;
  largestTools: Array<{ index: number; name: string; jsonBytes: number }>;
}

export function summarizeCodexRequestSize(
  body: ResponsesRequest,
  bodyJson = JSON.stringify(body),
): CodexRequestSizeSummary {
  const imageParts = inputImageParts(body.input);
  return {
    bodyJsonBytes: byteLength(bodyJson),
    instructionsBytes: stringBytes(body.instructions),
    inputJsonBytes: jsonBytes(body.input),
    toolsJsonBytes: jsonBytes(body.tools),
    textJsonBytes: jsonBytes(body.text),
    reasoningJsonBytes: jsonBytes(body.reasoning),
    includeJsonBytes: jsonBytes(body.include),
    clientMetadataJsonBytes: jsonBytes(body.client_metadata),
    inputItemCount: body.input.length,
    toolCount: body.tools?.length ?? 0,
    inputImagePartCount: imageParts.length,
    inputImageDataUrlBytes: imageParts
      .filter((part) => part.image_url.startsWith("data:"))
      .reduce((sum, part) => sum + byteLength(part.image_url), 0),
    inputTypeCounts: countBy(body.input, (item) => item.type),
    roleCounts: countBy(body.input, (item) => ("role" in item ? item.role : undefined)),
    largestInputItems: largestInputItems(body.input),
    largestInputImages: largestInputImages(imageParts),
    largestTools: largestTools(body.tools),
  };
}

function largestInputItems(input: ResponsesInputItem[]) {
  return input
    .map((item, index) => ({
      index,
      type: item.type,
      role: "role" in item ? item.role : undefined,
      jsonBytes: jsonBytes(item),
    }))
    .sort((a, b) => b.jsonBytes - a.jsonBytes)
    .slice(0, 5);
}

function largestInputImages(imageParts: ReturnType<typeof inputImageParts>) {
  return imageParts
    .map(({ itemIndex, partIndex, image_url }) => ({
      itemIndex,
      partIndex,
      jsonBytes: jsonBytes({ type: "input_image", image_url }),
      imageUrlBytes: byteLength(image_url),
      dataUrl: image_url.startsWith("data:"),
    }))
    .sort((a, b) => b.imageUrlBytes - a.imageUrlBytes)
    .slice(0, 5);
}

function inputImageParts(input: ResponsesInputItem[]) {
  return input.flatMap((item, itemIndex) => {
    if (item.type !== "message") return [];
    return item.content.flatMap((part, partIndex) =>
      part.type === "input_image" ? [{ itemIndex, partIndex, image_url: part.image_url }] : [],
    );
  });
}

function largestTools(tools: ResponsesRequest["tools"]) {
  return (tools ?? [])
    .map((tool, index) => ({
      index,
      name: "name" in tool ? tool.name : tool.type,
      jsonBytes: jsonBytes(tool),
    }))
    .sort((a, b) => b.jsonBytes - a.jsonBytes)
    .slice(0, 5);
}

function countBy<T>(items: T[], keyFor: (item: T) => string | undefined): Record<string, number> {
  const counts: Record<string, number> = {};
  for (const item of items) {
    const key = keyFor(item);
    if (!key) continue;
    counts[key] = (counts[key] ?? 0) + 1;
  }
  return counts;
}

function jsonBytes(value: unknown): number {
  if (value === undefined) return 0;
  return byteLength(JSON.stringify(value));
}

function stringBytes(value: string | undefined): number {
  if (!value) return 0;
  return byteLength(value);
}

function byteLength(value: string): number {
  return encoder.encode(value).byteLength;
}
