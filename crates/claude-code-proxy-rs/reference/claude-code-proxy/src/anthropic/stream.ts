import type { AnthropicRequest } from "./schema.ts";

export function wantsDownstreamStream(body: Pick<AnthropicRequest, "stream">): boolean {
  return body.stream === true;
}
