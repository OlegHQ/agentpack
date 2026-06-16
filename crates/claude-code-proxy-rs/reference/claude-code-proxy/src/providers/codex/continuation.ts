import type { ResponsesInputItem, ResponsesRequest } from "./translate/request.ts";

interface ContinuationState {
  responseId: string;
  promptSignature: string;
  transcript: ResponsesInputItem[];
  transcriptBytes: number;
  updatedAt: number;
}

const states = new Map<string, ContinuationState>();
const TTL_MS = 30 * 60 * 1000;
const MAX_STATES = 10_000;
const MAX_SESSION_TRANSCRIPT_BYTES = 2_000_000;
const MAX_TOTAL_TRANSCRIPT_BYTES = 20_000_000;
let totalTranscriptBytes = 0;

export interface ContinuationCandidate {
  previousResponseId?: string;
  inputDelta?: ResponsesInputItem[];
  inputDeltaCount: number;
  disabledReason?: string;
}

export function continuationCandidate(
  sessionId: string | undefined,
  body: ResponsesRequest,
  enabled: boolean,
  now = Date.now(),
): ContinuationCandidate {
  if (!enabled) return { inputDeltaCount: body.input.length, disabledReason: "disabled" };
  if (!sessionId) return { inputDeltaCount: body.input.length, disabledReason: "missing_session" };
  const state = states.get(sessionId);
  if (!state || now - state.updatedAt > TTL_MS) {
    clearContinuation(sessionId);
    return { inputDeltaCount: body.input.length, disabledReason: "missing_state" };
  }
  const signature = promptSignature(body);
  if (signature !== state.promptSignature) {
    clearContinuation(sessionId);
    return { inputDeltaCount: body.input.length, disabledReason: "prompt_changed" };
  }
  const suffix = inputSuffixAfterPrefix(body.input, state.transcript);
  if (!suffix) {
    clearContinuation(sessionId);
    return { inputDeltaCount: body.input.length, disabledReason: "not_append_only" };
  }
  if (suffix.length === 0) {
    return { inputDeltaCount: 0, disabledReason: "empty_delta" };
  }
  return {
    previousResponseId: state.responseId,
    inputDelta: suffix,
    inputDeltaCount: suffix.length,
  };
}

export function recordContinuation(
  sessionId: string | undefined,
  requestBody: ResponsesRequest,
  responseId: string | undefined,
  outputItems: ResponsesInputItem[],
  now = Date.now(),
): void {
  if (!sessionId) return;
  if (!responseId) {
    clearContinuation(sessionId);
    return;
  }
  const transcript = [...requestBody.input, ...outputItems];
  const transcriptBytes = byteLength(JSON.stringify(transcript));
  if (transcriptBytes > MAX_SESSION_TRANSCRIPT_BYTES) {
    clearContinuation(sessionId);
    return;
  }
  clearContinuation(sessionId);
  states.set(sessionId, {
    responseId,
    promptSignature: promptSignature(requestBody),
    transcript,
    transcriptBytes,
    updatedAt: now,
  });
  totalTranscriptBytes += transcriptBytes;
  evictOldest();
}

export function clearContinuation(sessionId: string | undefined): void {
  if (!sessionId) return;
  const existing = states.get(sessionId);
  if (existing) totalTranscriptBytes -= existing.transcriptBytes;
  states.delete(sessionId);
}

export function hasContinuationForTests(sessionId: string): boolean {
  return states.has(sessionId);
}

export function clearAllContinuationsForTests(): void {
  states.clear();
  totalTranscriptBytes = 0;
}

function inputSuffixAfterPrefix(
  input: ResponsesInputItem[],
  prefix: ResponsesInputItem[],
): ResponsesInputItem[] | undefined {
  if (prefix.length > input.length) return undefined;
  for (let i = 0; i < prefix.length; i++) {
    if (JSON.stringify(input[i]) !== JSON.stringify(prefix[i])) return undefined;
  }
  return input.slice(prefix.length);
}

function promptSignature(body: ResponsesRequest): string {
  const { input: _input, ...signature } = body;
  return stableJson(signature);
}

function stableJson(value: unknown): string {
  if (value === null || typeof value !== "object") return JSON.stringify(value);
  if (Array.isArray(value)) return `[${value.map(stableJson).join(",")}]`;
  const entries = Object.entries(value as Record<string, unknown>)
    .filter(([, child]) => child !== undefined)
    .sort(([a], [b]) => a.localeCompare(b));
  return `{${entries.map(([key, child]) => `${JSON.stringify(key)}:${stableJson(child)}`).join(",")}}`;
}

function evictOldest(): void {
  while (states.size > MAX_STATES || totalTranscriptBytes > MAX_TOTAL_TRANSCRIPT_BYTES) {
    const key = states.keys().next().value;
    if (!key) return;
    clearContinuation(key);
  }
}

function byteLength(value: string): number {
  return new TextEncoder().encode(value).byteLength;
}
