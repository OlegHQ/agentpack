import { afterEach, describe, expect, it } from "bun:test";
import type { RequestContext } from "../types.ts";
import { loadConfig } from "../../config.ts";
import { kimiProvider } from "./index.ts";

afterEach(() => {
  loadConfig({ forceReload: true });
});

describe("kimiProvider", () => {
  it("returns token count JSON responses", async () => {
    const response = await kimiProvider.handleCountTokens(
      { model: "kimi-for-coding", messages: [{ role: "user", content: "hello" }] },
      fakeCtx(),
    );
    const body = (await response.json()) as { input_tokens: number };

    expect(response.status).toBe(200);
    expect(response.headers.get("content-type")).toBe("application/json");
    expect(body.input_tokens).toBeGreaterThan(0);
  });
});

function fakeCtx(): RequestContext {
  return {
    reqId: "kimi-req",
    signal: new AbortController().signal,
    childLogger: () => ({
      debug() {},
      info() {},
      warn() {},
      error() {},
      child() {
        return this;
      },
    }),
  };
}
