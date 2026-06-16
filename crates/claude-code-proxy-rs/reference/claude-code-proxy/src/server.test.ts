import { afterEach, describe, expect, it } from "bun:test";
import { loadConfig } from "./config.ts";
import type { createLogger } from "./log.ts";
import { startServer, normalizeIncomingModel, wrapStreamResponse } from "./server.ts";
import { groupSupportedModelsByProvider } from "./providers/registry.ts";
import { startTestServer } from "./test/server.ts";
import { resetSessionsForTest } from "./server/session-affinity.ts";

const servers: Array<{ stop: () => void }> = [];

afterEach(() => {
  for (const server of servers.splice(0)) server.stop();
  loadConfig({ forceReload: true });
  resetSessionsForTest();
});

function countTokens(port: number, model: string, sessionId?: string): Promise<Response> {
  return fetch(`http://127.0.0.1:${port}/v1/messages/count_tokens`, {
    method: "POST",
    headers: {
      "content-type": "application/json",
      ...(sessionId ? { "x-claude-code-session-id": sessionId } : {}),
    },
    body: JSON.stringify({ model, messages: [{ role: "user", content: "hello" }] }),
  });
}

describe("normalizeIncomingModel", () => {
  it("removes Claude Code local context hints without changing the model id otherwise", () => {
    expect(normalizeIncomingModel("gpt-5.5[1m]")).toBe("gpt-5.5");
    expect(normalizeIncomingModel("gpt-5.4-fast[1m]")).toBe("gpt-5.4-fast");
    expect(normalizeIncomingModel("kimi-for-coding")).toBe("kimi-for-coding");
  });
});

describe("server error responses", () => {
  it("returns JSON for health checks", async () => {
    const server = startTestServer(startServer);
    servers.push(server);

    const resp = await fetch(`http://127.0.0.1:${server.port}/healthz`);
    const body = (await resp.json()) as { ok: boolean };

    expect(resp.status).toBe(200);
    expect(resp.headers.get("content-type")).toBe("application/json");
    expect(body).toEqual({ ok: true });
  });

  it("returns JSON for unknown routes", async () => {
    const server = startTestServer(startServer);
    servers.push(server);

    const resp = await fetch(`http://127.0.0.1:${server.port}/not-a-route`);
    const body = (await resp.json()) as { type: string; error: { type: string; message: string } };

    expect(resp.status).toBe(404);
    expect(resp.headers.get("content-type")).toBe("application/json");
    expect(body).toEqual({
      type: "error",
      error: {
        type: "not_found",
        message: "No route for GET /not-a-route",
      },
    });
  });

  it("keeps invalid JSON request parsing failures as JSON", async () => {
    const server = startTestServer(startServer);
    servers.push(server);

    const resp = await fetch(`http://127.0.0.1:${server.port}/v1/messages`, {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: "{",
    });
    const body = (await resp.json()) as { error: { type: string; message: string } };

    expect(resp.status).toBe(400);
    expect(resp.headers.get("content-type")).toBe("application/json");
    expect(body.error.type).toBe("invalid_request_error");
  });

  it("uses active alias-provider grouping in unknown-model errors", async () => {
    loadConfig({ env: { CCP_ALIAS_PROVIDER: "kimi" }, forceReload: true });
    const server = startTestServer(startServer);
    servers.push(server);

    const resp = await fetch(`http://127.0.0.1:${server.port}/v1/messages`, {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({
        model: "not-a-real-model",
        messages: [{ role: "user", content: "hello" }],
      }),
    });
    const body = (await resp.json()) as { error: { message: string } };

    const parts: string[] = [];
    for (const [provider, models] of groupSupportedModelsByProvider()) {
      parts.push(`${provider}: ${models.join(", ")}`);
    }

    expect(resp.status).toBe(400);
    expect(body.error.message).toBe(
      `Unknown model "not-a-real-model". Supported: ${parts.join("; ")}.`,
    );
    expect([...groupSupportedModelsByProvider().keys()]).toEqual(["codex", "kimi", "cursor"]);
    expect(groupSupportedModelsByProvider().get("kimi")).toEqual(
      expect.arrayContaining(["haiku", "sonnet", "opus"]),
    );
  });
});

describe("stream wrapper", () => {
  it("strips hop-by-hop headers when wrapping upstream streams", async () => {
    const upstream = new Response("hello", {
      status: 207,
      statusText: "Multi-Status",
      headers: {
        "content-encoding": "gzip",
        "content-length": "5",
        "transfer-encoding": "chunked",
        "x-upstream": "ok",
      },
    });

    const wrapped = wrapStreamResponse(upstream, "test", 0, {
      info() {},
      error() {},
      warn() {},
      debug() {},
      child() {
        return this;
      },
    } as ReturnType<typeof createLogger>);

    expect(wrapped.status).toBe(207);
    expect(wrapped.statusText).toBe("Multi-Status");
    expect(wrapped.headers.get("content-encoding")).toBeNull();
    expect(wrapped.headers.get("content-length")).toBeNull();
    expect(wrapped.headers.get("transfer-encoding")).toBeNull();
    expect(wrapped.headers.get("x-upstream")).toBe("ok");
    expect(await wrapped.text()).toBe("hello");
  });
});

describe("server session-aware alias routing", () => {
  it("routes aliases to the concrete provider used earlier in the session", async () => {
    loadConfig({ env: { CCP_CODEX_SERVICE_TIER: "standard" }, forceReload: true });
    const server = startTestServer(startServer);
    servers.push(server);

    const sessionId = crypto.randomUUID();
    const fallback = await countTokens(server.port, "sonnet");
    expect(fallback.status).toBe(400);
    const fallbackBody = (await fallback.json()) as { error: { message: string } };
    expect(fallbackBody.error.message).toContain("Invalid service tier override");

    expect((await countTokens(server.port, "kimi-for-coding", sessionId)).status).toBe(200);
    expect((await countTokens(server.port, "sonnet", sessionId)).status).toBe(200);
  });
});
