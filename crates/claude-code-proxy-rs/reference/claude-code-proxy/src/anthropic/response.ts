import { expect } from "bun:test";

type ResponseBody = ConstructorParameters<typeof Response>[0];
type ResponseHeaders = ResponseInit["headers"];
type HeadersConstructorInput = ConstructorParameters<typeof Headers>[0];

export interface AnthropicErrorBody {
  type: "error";
  error: {
    type: string;
    message: string;
  };
}

export function anthropicErrorBody(type: string, message: string): AnthropicErrorBody {
  return { type: "error", error: { type, message } };
}

export function jsonError(status: number, type: string, message: string, headers?: ResponseHeaders): Response {
  return jsonResponse(anthropicErrorBody(type, message), { status, headers });
}

export function jsonResponse(body: unknown, init: ResponseInit = {}): Response {
  return new Response(JSON.stringify(body), {
    ...init,
    headers: jsonHeaders(init.headers),
  });
}

export function sseResponse(body: ResponseBody, init: ResponseInit = {}): Response {
  return new Response(body, {
    status: 200,
    ...init,
    headers: sseHeaders(init.headers),
  });
}

export function streamResponse(resp: Response, body: ResponseBody): Response {
  return new Response(body, {
    status: resp.status,
    statusText: resp.statusText,
    headers: streamResponseHeaders(resp.headers),
  });
}

export function streamResponseHeaders(headers: ResponseHeaders): Headers {
  const out = new Headers(headers as HeadersConstructorInput);
  out.delete("content-encoding");
  out.delete("content-length");
  out.delete("transfer-encoding");
  return out;
}

function jsonHeaders(headers: ResponseHeaders): Headers {
  const out = new Headers(headers as HeadersConstructorInput);
  out.set("content-type", "application/json");
  return out;
}

function sseHeaders(headers: ResponseHeaders): Headers {
  const out = new Headers(headers as HeadersConstructorInput);
  out.set("content-type", "text/event-stream");
  out.set("cache-control", "no-cache");
  out.set("connection", "keep-alive");
  return out;
}

export function expectSseHeaders(response: Response): void {
  expect(response.headers.get("content-type")).toBe("text/event-stream");
  expect(response.headers.get("cache-control")).toBe("no-cache");
  expect(response.headers.get("connection")).toBe("keep-alive");
}
