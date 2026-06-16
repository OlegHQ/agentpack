import { describe, expect, it } from "bun:test";
import {
  anthropicErrorBody,
  expectSseHeaders,
  jsonError,
  jsonResponse,
  sseResponse,
  streamResponse,
} from "./response.ts";

describe("anthropicErrorBody", () => {
  it("returns Anthropic error payload shape", () => {
    expect(anthropicErrorBody("invalid_request_error", "Bad request")).toEqual({
      type: "error",
      error: { type: "invalid_request_error", message: "Bad request" },
    });
  });
});

describe("jsonResponse and jsonError", () => {
  it("normalizes JSON response shape and content type", async () => {
    const response = jsonError(400, "invalid_request_error", "Bad request");
    const body = (await response.json()) as { type: "error"; error: { type: string; message: string } };

    expect(response.status).toBe(400);
    expect(response.headers.get("content-type")).toBe("application/json");
    expect(body).toEqual({
      type: "error",
      error: { type: "invalid_request_error", message: "Bad request" },
    });
  });

  it("preserves custom headers when returning JSON errors", () => {
    const response = jsonError(429, "rate_limit_error", "Slow down", { "retry-after": "10" });
    expect(response.headers.get("retry-after")).toBe("10");
    expect(response.headers.get("content-type")).toBe("application/json");
  });

  it("preserves headers on generic JSON responses", async () => {
    const response = jsonResponse({ ok: true }, { headers: { "x-trace-id": "abc" } });
    expect(response.headers.get("content-type")).toBe("application/json");
    expect(response.headers.get("x-trace-id")).toBe("abc");
    expect(await response.json()).toEqual({ ok: true });
  });
});

describe("sseResponse", () => {
  it("adds SSE headers and preserves extra headers", () => {
    const response = sseResponse("data", {
      headers: {
        "x-upstream": "origin",
        "cache-control": "custom",
        connection: "close",
      },
    });

    expect(response.status).toBe(200);
    expectSseHeaders(response);
    expect(response.headers.get("x-upstream")).toBe("origin");
  });
});

describe("streamResponse", () => {
  it("strips hop-by-hop headers but preserves status, statusText and passthrough headers", async () => {
    const upstream = new Response("stream-data", {
      status: 203,
      statusText: "Accepted",
      headers: {
        "content-encoding": "gzip",
        "content-length": "10",
        "transfer-encoding": "chunked",
        "x-upstream": "provider",
      },
    });
    const response = streamResponse(upstream, upstream.body);

    expect(response.status).toBe(203);
    expect(response.statusText).toBe("Accepted");
    expect(response.headers.get("content-encoding")).toBeNull();
    expect(response.headers.get("content-length")).toBeNull();
    expect(response.headers.get("transfer-encoding")).toBeNull();
    expect(response.headers.get("x-upstream")).toBe("provider");
    expect(await response.text()).toBe("stream-data");
  });
});
