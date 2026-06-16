import type { Logger } from "../../../log.ts";

export const silentLog: Logger = (() => {
  const log: Logger = {
    debug: () => {},
    info: () => {},
    warn: () => {},
    error: () => {},
    child: () => log,
  };
  return log;
})();

export function sse(type: string, payload: Record<string, unknown>): string {
  return `data: ${JSON.stringify({ type, ...payload })}\n\n`;
}

export function upstreamFromChunks(
  chunks: string[],
  onChunk?: () => void,
): ReadableStream<Uint8Array> {
  const encoder = new TextEncoder();
  let index = 0;
  return new ReadableStream<Uint8Array>({
    pull(controller) {
      if (index >= chunks.length) {
        controller.close();
        return;
      }
      onChunk?.();
      controller.enqueue(encoder.encode(chunks[index++]));
    },
  });
}

export function abortingUpstream(err: Error): ReadableStream<Uint8Array> {
  return new ReadableStream<Uint8Array>({
    pull(controller) {
      controller.error(err);
    },
  });
}

export function upstreamThatErrorsAfterChunks(
  chunks: string[],
  err: Error,
): ReadableStream<Uint8Array> {
  const encoder = new TextEncoder();
  let index = 0;
  return new ReadableStream<Uint8Array>({
    pull(controller) {
      if (index >= chunks.length) {
        controller.error(err);
        return;
      }
      controller.enqueue(encoder.encode(chunks[index++]));
    },
  });
}

export async function collect(stream: ReadableStream<Uint8Array>): Promise<string> {
  const reader = stream.getReader();
  const decoder = new TextDecoder();
  let out = "";
  while (true) {
    const { done, value } = await reader.read();
    if (done) return out;
    out += decoder.decode(value, { stream: true });
  }
}
