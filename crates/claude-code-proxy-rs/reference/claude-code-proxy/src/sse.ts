export interface SseEvent {
  event?: string;
  data: string;
}

export interface SseStreamStats {
  bytesRead: number;
  chunkCount: number;
  eventCount: number;
  startedAt: number;
  lastChunkAt?: number;
  lastEventAt?: number;
}

export function createSseStreamStats(now = Date.now()): SseStreamStats {
  return {
    bytesRead: 0,
    chunkCount: 0,
    eventCount: 0,
    startedAt: now,
  };
}

export function encodeSseEvent(event: string, data: unknown): string {
  return `event: ${event}\ndata: ${JSON.stringify(data)}\n\n`;
}

const BOUNDARY = /\r\n\r\n|\n\n|\r\r/;

export async function* parseSseStream(
  body: ReadableStream<Uint8Array>,
  stats = createSseStreamStats(),
): AsyncGenerator<SseEvent> {
  const reader = body.getReader();
  const decoder = new TextDecoder();
  let buf = "";
  try {
    while (true) {
      const { value, done } = await reader.read();
      if (done) break;
      stats.bytesRead += value.byteLength;
      stats.chunkCount += 1;
      stats.lastChunkAt = Date.now();
      buf += decoder.decode(value, { stream: true });
      let match: RegExpExecArray | null;
      while ((match = BOUNDARY.exec(buf)) !== null) {
        const raw = buf.slice(0, match.index);
        buf = buf.slice(match.index + match[0].length);
        const evt = parseEventBlock(raw);
        if (evt) {
          stats.eventCount += 1;
          stats.lastEventAt = Date.now();
          yield evt;
        }
      }
    }
    if (buf.trim()) {
      const evt = parseEventBlock(buf);
      if (evt) {
        stats.eventCount += 1;
        stats.lastEventAt = Date.now();
        yield evt;
      }
    }
  } finally {
    reader.releaseLock();
  }
}

function parseEventBlock(raw: string): SseEvent | undefined {
  let event: string | undefined;
  const dataLines: string[] = [];
  // Per SSE spec, lines are terminated by CR, LF, or CRLF.
  for (const line of raw.split(/\r\n|\n|\r/)) {
    if (!line || line.startsWith(":")) continue;
    const colon = line.indexOf(":");
    const field = colon === -1 ? line : line.slice(0, colon);
    const value = colon === -1 ? "" : line.slice(colon + 1).replace(/^ /, "");
    if (field === "event") event = value;
    else if (field === "data") dataLines.push(value);
  }
  if (!dataLines.length && !event) return undefined;
  return { event, data: dataLines.join("\n") };
}
