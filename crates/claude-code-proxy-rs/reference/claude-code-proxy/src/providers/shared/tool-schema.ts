import { encode } from "gpt-tokenizer/model/gpt-4o";

export function countToolSchemaTokens<T>(
  tools: Array<T> | undefined,
  readName: (tool: T) => string,
  readDescription: (tool: T) => string | undefined,
  readSchema: (tool: T) => unknown,
): number {
  let total = 0;
  for (const tool of tools ?? []) {
    total += encode(readName(tool)).length;
    const description = readDescription(tool);
    if (description) total += encode(description).length;
    total += encode(JSON.stringify(readSchema(tool) ?? {})).length;
  }
  return total;
}
