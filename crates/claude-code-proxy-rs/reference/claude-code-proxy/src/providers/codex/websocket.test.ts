import { afterEach, describe, expect, it } from "bun:test";
import { createServer } from "node:http";
import WebSocket, { WebSocketServer } from "ws";
import type { RequestContext } from "../types.ts";
import {
  CodexWebSocketSetupError,
  clearCodexWebSocketPoolForTests,
  codexWebSocketHeaders,
  codexWebSocketRequest,
  toWebSocketUrl,
  WEBSOCKET_PROTOCOL_HEADER,
} from "./websocket.ts";
import { collect, silentLog } from "./translate/test-helpers.ts";
import type { ResponsesWebSocketRequest } from "./translate/request.ts";

const websocketTimeoutMs = {
  connect: 1_000,
  idle: 1_000,
};

function ctx(): RequestContext {
  return {
    reqId: "req_1",
    signal: new AbortController().signal,
    childLogger: () => silentLog,
  };
}

afterEach(() => {
  clearCodexWebSocketPoolForTests();
});

function body(): ResponsesWebSocketRequest {
  return {
    model: "gpt-5.5",
    input: [{ type: "message", role: "user", content: [{ type: "input_text", text: "hello" }] }],
    store: false,
    stream: true,
  };
}

function webSocketRequest(
  url: string,
  options: { poolKey?: string } = {},
): ReturnType<typeof codexWebSocketRequest> {
  return codexWebSocketRequest({
    url,
    headers: new Headers(),
    body: body(),
    ctx: ctx(),
    connectTimeoutMs: websocketTimeoutMs.connect,
    idleTimeoutMs: websocketTimeoutMs.idle,
    ...options,
  });
}

async function withServer(
  handler: (socket: WebSocket, requestBody: Promise<unknown>, request: import("node:http").IncomingMessage) => void,
): Promise<{ url: string; close: () => Promise<void> }> {
  const server = createServer();
  const wss = new WebSocketServer({ server });
  wss.on("connection", (socket, request) => {
    const messages: unknown[] = [];
    const waiters: ((value: unknown) => void)[] = [];
    const nextBody = () =>
      new Promise<unknown>((resolve) => {
        const message = messages.shift();
        if (message !== undefined) resolve(message);
        else waiters.push(resolve);
      });
    socket.on("message", (data) => {
      const message = JSON.parse(data.toString());
      const waiter = waiters.shift();
      if (waiter) waiter(message);
      else messages.push(message);
    });
    handler(socket, nextBody(), request);
  });
  await new Promise<void>((resolve) => server.listen(0, "127.0.0.1", resolve));
  const address = server.address();
  if (!address || typeof address === "string") throw new Error("missing server address");
  return {
    url: `http://127.0.0.1:${address.port}/backend-api/codex/responses`,
    close: () =>
      new Promise<void>((resolve, reject) => {
        wss.close((err) => {
          if (err) reject(err);
          else server.close((closeErr) => (closeErr ? reject(closeErr) : resolve()));
        });
      }),
  };
}

async function withCompletionServer(
  onConnection: (socket: WebSocket, socketIndex: number) => void,
): Promise<{ url: string; close: () => Promise<void>; sockets: () => number }> {
  let sockets = 0;
  const server = await withServer((socket, requestBody, request) => {
    const socketIndex = ++sockets;
    onConnection(socket, socketIndex);
  });
  return {
    url: server.url,
    close: server.close,
    sockets: () => sockets,
  };
}

async function withAutoCompletionServer(): Promise<{
  url: string;
  close: () => Promise<void>;
  sockets: () => number;
}> {
  return withCompletionServer((socket, socketIndex) => {
    socket.on("message", () => {
      socket.send(
        JSON.stringify({ type: "response.completed", response: { id: `resp_${socketIndex}` } }),
      );
    });
  });
}

describe("Codex WebSocket helpers", () => {
  it("converts HTTP URLs to WebSocket URLs", () => {
    expect(toWebSocketUrl("https://chatgpt.com/backend-api/codex/responses")).toBe(
      "wss://chatgpt.com/backend-api/codex/responses",
    );
    expect(toWebSocketUrl("http://127.0.0.1:1234/backend-api/codex/responses")).toBe(
      "ws://127.0.0.1:1234/backend-api/codex/responses",
    );
  });

  it("rejects unsupported URL schemes", () => {
    expect(() => toWebSocketUrl("file:///tmp/responses")).toThrow(
      "Unsupported Codex WebSocket URL scheme",
    );
  });

  it("sets the WebSocket beta header and removes content length", () => {
    const headers = new Headers({
      "openai-beta": "responses=experimental",
      "content-length": "123",
      authorization: "Bearer token",
    });

    expect(codexWebSocketHeaders(headers)).toEqual({
      authorization: "Bearer token",
      "openai-beta": WEBSOCKET_PROTOCOL_HEADER,
    });
  });

  it("carries upgrade failure status for auth refresh", () => {
    const err = new CodexWebSocketSetupError("upgrade failed", 401);

    expect(err.name).toBe("CodexWebSocketSetupError");
    expect(err.status).toBe(401);
  });

  it("converts websocket events to SSE and sends response.create without stream", async () => {
    const server = await withServer(async (socket, requestBody) => {
      const req = await requestBody;
      expect(req).toEqual({
        type: "response.create",
        model: "gpt-5.5",
        input: [
          { type: "message", role: "user", content: [{ type: "input_text", text: "hello" }] },
        ],
        store: false,
      });
      socket.send(JSON.stringify({ type: "response.completed", response: { id: "resp_1" } }));
    });
    try {
      const stream = await webSocketRequest(server.url);

      await expect(collect(stream)).resolves.toBe(
        'data: {"type":"response.completed","response":{"id":"resp_1"}}\n\n',
      );
    } finally {
      await server.close();
    }
  });

  it("rejects setup errors before exposing a stream", async () => {
    const server = await withServer((socket) => {
      socket.send(
        JSON.stringify({
          type: "error",
          status: 429,
          error: { code: "rate_limit", message: "slow down" },
          headers: { "retry-after": "3" },
        }),
      );
    });
    try {
      let caught: unknown;
      try {
        await webSocketRequest(server.url);
      } catch (err) {
        caught = err;
      }
      expect(caught).toBeInstanceOf(CodexWebSocketSetupError);
      const err = caught as CodexWebSocketSetupError;
      expect(err.status).toBe(429);
      expect(err.code).toBe("rate_limit");
      expect(err.retryAfter).toBe("3");
      expect(err.requestSent).toBe(false);
    } finally {
      await server.close();
    }
  });

  it("exposes response errors as SSE events", async () => {
    const server = await withServer(async (socket, requestBody) => {
      await requestBody;
      socket.send(
        JSON.stringify({
          type: "error",
          error: { code: "invalid_request", message: "bad request" },
        }),
      );
    });
    try {
      const stream = await webSocketRequest(server.url);

      await expect(collect(stream)).resolves.toBe(
        'data: {"type":"error","error":{"code":"invalid_request","message":"bad request"}}\n\n',
      );
    } finally {
      await server.close();
    }
  });

  it("reuses a pooled websocket", async () => {
    const server = await withAutoCompletionServer();
    try {
      const request = () =>
        webSocketRequest(server.url, {
          poolKey: "session-1",
        });

      await collect(await request());
      await collect(await request());

      expect(server.sockets()).toBe(1);
    } finally {
      clearCodexWebSocketPoolForTests();
      await server.close();
    }
  });

  it("does not pool websocket requests without a pool key", async () => {
    const server = await withAutoCompletionServer();
    try {
      const makeRequest = () => webSocketRequest(server.url).then(collect);

      await makeRequest();
      await makeRequest();
      expect(server.sockets()).toBe(2);
    } finally {
      await server.close();
    }
  });

});
