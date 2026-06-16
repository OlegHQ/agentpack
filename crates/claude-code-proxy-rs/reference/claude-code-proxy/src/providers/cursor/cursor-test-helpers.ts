import { encodeConnectFrame } from "./client.ts";
import type { CursorProto, ProtoClass, ProtoMessage } from "./proto-loader.ts";
import type { RequestContext } from "../types.ts";
import { parseSseStream } from "../../sse.ts";

const encoder = new TextEncoder();
const decoder = new TextDecoder();

export type JsonProtoClassOptions = {
  mergeFields?: boolean;
};

export function jsonProtoClass(options: JsonProtoClassOptions = {}): ProtoClass {
  const { mergeFields = false } = options;
  return {
    fromBinary(bytes: Uint8Array): ProtoMessage {
      return messageFromJson(JSON.parse(decoder.decode(bytes)), options);
    },
    fromJson(json: unknown): ProtoMessage {
      return messageFromJson(json, options);
    },
  };
}

export function messageFromJson(
  json: unknown,
  options: JsonProtoClassOptions = {},
): ProtoMessage {
  const { mergeFields = false } = options;
  const message: ProtoMessage = {
    toBinary(): Uint8Array {
      return jsonBytes(json);
    },
    toJson(): unknown {
      return json;
    },
  };
  if (!mergeFields) return message;
  return Object.assign(
    message,
    json && typeof json === "object" && !Array.isArray(json) ? json : {},
  );
}

export function createFakeProto(options: JsonProtoClassOptions = {}): CursorProto {
  const protoClass = jsonProtoClass(options);
  return {
    AgentServerMessage: protoClass,
    AgentClientMessage: protoClass,
  };
}

export const fakeProtoMerged = createFakeProto({ mergeFields: true });
export const fakeProto = createFakeProto();

export function frame(json: unknown): Uint8Array {
  return encodeConnectFrame(jsonBytes(json));
}

export function jsonBytes(json: unknown): Uint8Array {
  return encoder.encode(JSON.stringify(json));
}

export function decodeFrameJson(frameBytes: Uint8Array): unknown {
  const buf = Buffer.from(frameBytes);
  const len = buf.readUInt32BE(1);
  return JSON.parse(decoder.decode(buf.subarray(5, 5 + len)));
}

export function streamFromChunks(chunks: Uint8Array[]): ReadableStream<Uint8Array> {
  return new ReadableStream<Uint8Array>({
    start(controller) {
      for (const chunk of chunks) controller.enqueue(chunk);
      controller.close();
    },
  });
}

export type FakeCursorCtxOptions = {
  sessionId?: string;
  reqId?: string;
};

export function fakeCursorCtx(options: FakeCursorCtxOptions = {}): RequestContext {
  const { sessionId, reqId = "req" } = options;
  return {
    reqId,
    ...(sessionId === undefined ? {} : { sessionId }),
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

export async function collectCursorSse(
  response: Response | ReadableStream<Uint8Array>,
): Promise<Array<{ event: string; data: any }>> {
  const events = [];
  const body = response instanceof ReadableStream ? response : response.body!;
  for await (const event of parseSseStream(body)) {
    events.push({ event: event.event ?? "message", data: JSON.parse(event.data) });
  }
  return events;
}

export function jwt(payload: Record<string, unknown>): string {
  return [
    Buffer.from(JSON.stringify({ alg: "none", typ: "JWT" })).toString("base64url"),
    Buffer.from(JSON.stringify(payload)).toString("base64url"),
    "signature",
  ].join(".");
}

export function resourceExhaustedFrame(): Uint8Array {
  return encodeConnectFrame(
    jsonBytes({
      error: {
        code: "resource_exhausted",
        message: "Error",
        details: [
          {
            debug: {
              details: {
                additionalInfo: {
                  chatMessage: "You've hit your free requests limit.",
                },
              },
            },
          },
        ],
      },
    }),
    2,
  );
}
