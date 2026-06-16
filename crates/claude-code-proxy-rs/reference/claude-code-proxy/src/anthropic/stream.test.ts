import { describe, expect, it } from "bun:test";
import { wantsDownstreamStream } from "./stream.ts";

describe("wantsDownstreamStream", () => {
  it("uses Anthropic's non-streaming default", () => {
    expect(wantsDownstreamStream({})).toBe(false);
  });

  it("streams only when explicitly requested", () => {
    expect(wantsDownstreamStream({ stream: true })).toBe(true);
    expect(wantsDownstreamStream({ stream: false })).toBe(false);
  });
});
