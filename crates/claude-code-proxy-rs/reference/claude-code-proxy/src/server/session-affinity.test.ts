import { describe, expect, it } from "bun:test";
import {
  MAX_SESSIONS,
  SESSION_IDLE_TTL_MS,
  existingSession,
  recordSessionRequest,
  resetSessionsForTest,
} from "./session-affinity.ts";

describe("server session affinity", () => {
  it("records concrete codex and kimi affinity for later aliases", () => {
    resetSessionsForTest();
    const codex = recordSessionRequest("codex-session", undefined, "codex", "gpt-5.4", 1);
    const kimi = recordSessionRequest("kimi-session", undefined, "kimi", "kimi-for-coding", 1);

    expect(codex?.affinityProvider).toBe("codex");
    expect(kimi?.affinityProvider).toBe("kimi");
    expect(existingSession("codex-session", 2)?.affinityProvider).toBe("codex");
    expect(existingSession("kimi-session", 2)?.affinityProvider).toBe("kimi");
  });

  it("does not overwrite affinity for aliases or cursor requests", () => {
    resetSessionsForTest();
    const first = recordSessionRequest("session", undefined, "codex", "gpt-5.4", 1);
    const alias = recordSessionRequest("session", first, "kimi", "sonnet", 2);
    const cursor = recordSessionRequest("session", alias, "cursor", "cursor-agent", 3);

    expect(alias?.affinityProvider).toBe("codex");
    expect(cursor?.affinityProvider).toBe("codex");
    expect(cursor?.seq).toBe(3);
  });

  it("expires idle sessions with the current TTL boundary", () => {
    resetSessionsForTest();
    const state = recordSessionRequest("session", undefined, "kimi", "kimi-for-coding", 100);

    expect(existingSession("session", 100 + SESSION_IDLE_TTL_MS)).toBe(state);
    expect(existingSession("session", 100 + SESSION_IDLE_TTL_MS + 1)).toBeUndefined();
  });

  it("evicts oldest sessions when the maximum is exceeded", () => {
    resetSessionsForTest();
    for (let i = 0; i <= MAX_SESSIONS; i += 1) {
      recordSessionRequest(`session-${i}`, undefined, "codex", "gpt-5.4", i);
    }

    expect(existingSession("session-0", MAX_SESSIONS + 1)).toBeUndefined();
    expect(existingSession("session-1", MAX_SESSIONS + 1)).toBeDefined();
    expect(existingSession(`session-${MAX_SESSIONS}`, MAX_SESSIONS + 1)).toBeDefined();
  });

  it("evicts by oldest map key rather than most recent lastSeen", () => {
    resetSessionsForTest();
    recordSessionRequest("session-0", undefined, "codex", "gpt-5.4", 0);

    for (let i = 1; i < MAX_SESSIONS; i += 1) {
      recordSessionRequest(`session-${i}`, undefined, "codex", "gpt-5.4", i);
    }

    const existing = existingSession("session-0", MAX_SESSIONS);
    recordSessionRequest("session-0", existing, "codex", "gpt-5.4", MAX_SESSIONS);
    recordSessionRequest("overflow", undefined, "codex", "gpt-5.4", MAX_SESSIONS + 1);

    expect(existingSession("session-0", MAX_SESSIONS + 2)).toBeUndefined();
    expect(existingSession("session-1", MAX_SESSIONS + 2)).toBeDefined();
  });
});

