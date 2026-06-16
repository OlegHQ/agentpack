import type { AliasProvider } from "../config.ts";
import { ANTHROPIC_STYLE_ALIASES } from "../providers/registry.ts";

export interface SessionState {
  seq: number;
  affinityProvider?: AliasProvider;
  lastSeen: number;
}

export const SESSION_IDLE_TTL_MS = 30 * 60 * 1000;
export const MAX_SESSIONS = 10_000;

const sessions = new Map<string, SessionState>();

export function existingSession(
  sessionId: string | undefined,
  now = Date.now(),
): SessionState | undefined {
  if (!sessionId) return undefined;
  const state = sessions.get(sessionId);
  if (!state) return undefined;
  if (now - state.lastSeen <= SESSION_IDLE_TTL_MS) return state;
  sessions.delete(sessionId);
  return undefined;
}

export function recordSessionRequest(
  sessionId: string | undefined,
  session: SessionState | undefined,
  providerName: string,
  model: string,
  now = Date.now(),
): SessionState | undefined {
  if (!sessionId) return undefined;
  const state = session ?? { seq: 0, lastSeen: now };
  state.seq += 1;
  state.lastSeen = now;
  const affinityProvider = affinityProviderFor(providerName);
  if (affinityProvider && !ANTHROPIC_STYLE_ALIASES.has(model)) {
    state.affinityProvider = affinityProvider;
  }
  sessions.set(sessionId, state);
  evictOldestSessions();
  return state;
}

export function resetSessionsForTest(): void {
  sessions.clear();
}

function affinityProviderFor(providerName: string): AliasProvider | undefined {
  if (providerName === "codex" || providerName === "kimi") return providerName;
  return undefined;
}

function evictOldestSessions(): void {
  while (sessions.size > MAX_SESSIONS) {
    const oldestSessionId = sessions.keys().next().value;
    if (!oldestSessionId) return;
    sessions.delete(oldestSessionId);
  }
}

