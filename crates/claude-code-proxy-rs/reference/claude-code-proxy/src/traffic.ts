import { mkdir, writeFile } from "node:fs/promises";
import { dirname, join } from "node:path";
import { stateDir } from "./paths.ts";
import { REDACT_KEYS } from "./log.ts";
import type { TrafficCapture } from "./providers/types.ts";

const encoder = new TextEncoder();

export function trafficCaptureEnabled(env = process.env): boolean {
  const value = env.CCP_TRAFFIC_LOG;
  return value === "1" || value === "true" || value === "yes";
}

export function createTrafficCapture(opts: {
  reqId: string;
  sessionId?: string;
  sessionSeq?: number;
  provider?: string;
}): TrafficCapture | undefined {
  if (!trafficCaptureEnabled()) return undefined;
  const sessionPart = sanitizePathPart(opts.sessionId || "no-session");
  const seqPart = String(opts.sessionSeq ?? 0).padStart(6, "0");
  const providerPart = sanitizePathPart(opts.provider || "unknown-provider");
  const reqPart = sanitizePathPart(opts.reqId);
  const dir = join(stateDir(), "traffic", sessionPart, `${seqPart}-${providerPart}-${reqPart}`);
  let artifactCounter = 0;
  let eventCounter = 0;

  const artifactPath = (name: string) => {
    artifactCounter += 1;
    return join(dir, `${String(artifactCounter).padStart(3, "0")}-${sanitizePathPart(name)}`);
  };
  const eventPath = (name: string) => {
    eventCounter += 1;
    return join(
      dir,
      "events",
      `${String(eventCounter).padStart(6, "0")}-${sanitizePathPart(name)}`,
    );
  };

  return {
    writeJson(name, value) {
      const path = artifactPath(ensureExtension(name, ".json"));
      void writeCaptureFile(path, JSON.stringify(redactTraffic(value), null, 2));
    },
    writeText(name, value) {
      const path = artifactPath(ensureExtension(name, ".txt"));
      void writeCaptureFile(path, value);
    },
    writeBytes(name, value) {
      const path = artifactPath(name);
      void writeCaptureFile(path, value);
    },
    writeJsonEvent(name, value) {
      const path = eventPath(ensureExtension(name, ".json"));
      void writeCaptureFile(path, JSON.stringify(redactTraffic(value), null, 2));
    },
  };
}

export function headersToRecord(headers: Headers): Record<string, string> {
  const out: Record<string, string> = {};
  for (const [key, value] of headers) out[key] = value;
  return out;
}

function redactTraffic(value: unknown, depth = 0): unknown {
  if (depth > 100) return "[depth-limit]";
  if (value == null) return value;
  if (typeof value !== "object") return value;
  if (Array.isArray(value)) return value.map((item) => redactTraffic(item, depth + 1));
  const out: Record<string, unknown> = {};
  for (const [key, child] of Object.entries(value as Record<string, unknown>)) {
    out[key] = REDACT_KEYS.has(key.toLowerCase())
      ? redactValue(child)
      : redactTraffic(child, depth + 1);
  }
  return out;
}

function redactValue(value: unknown): string {
  if (typeof value === "string") return `[redacted len=${value.length}]`;
  return "[redacted]";
}

async function writeCaptureFile(path: string, value: string | Uint8Array): Promise<void> {
  try {
    await mkdir(dirname(path), { recursive: true });
    await writeFile(path, typeof value === "string" ? encoder.encode(value) : value, {
      mode: 0o600,
    });
  } catch {
    // Traffic capture must not affect request handling.
  }
}

function sanitizePathPart(value: string): string {
  const sanitized = value.replace(/[^a-zA-Z0-9._-]/g, "_");
  return sanitized.slice(0, 160) || "unknown";
}

function ensureExtension(name: string, extension: string): string {
  return name.endsWith(extension) ? name : `${name}${extension}`;
}
