import { afterEach, describe, expect, it } from "bun:test";
import type { AnthropicRequest } from "../../anthropic/schema.ts";
import type { RequestContext } from "../types.ts";
import { loadConfig } from "../../config.ts";
import { codexProvider } from "./index.ts";

const ctx: RequestContext = {
  reqId: "test-req",
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

const countTokensRequest: AnthropicRequest = {
  model: "gpt-5.4",
  messages: [{ role: "user", content: "hello" }],
};

const expectInvalidCountTokens = async (env: Record<string, string>, message: string) => {
  loadConfig({ env, forceReload: true });
  const resp = await codexProvider.handleCountTokens(countTokensRequest, ctx);

  expect(resp.status).toBe(400);
  expect(resp.headers.get("content-type")).toBe("application/json");
  expect(await resp.json()).toEqual({
    type: "error",
    error: {
      type: "invalid_request_error",
      message,
    },
  });
};

afterEach(() => {
  loadConfig({ forceReload: true });
});

describe("codexProvider", () => {
  it("returns 400 for invalid service tier config during token counting", async () => {
    await expectInvalidCountTokens(
      { CCP_CODEX_SERVICE_TIER: "standard" },
      'Invalid service tier override: "standard". Must be one of: fast, priority, flex',
    );
  });

  it("returns 400 for invalid forced model during token counting", async () => {
    await expectInvalidCountTokens(
      { CCP_CODEX_MODEL: "gpt-4.1" },
      'Model "gpt-5.4" resolves to unsupported model "gpt-4.1"',
    );
  });
});
