import type { AnthropicRequest } from "../../anthropic/schema.ts";

interface CursorSessionState {
  conversationId: string;
  lastSeen: number;
}

const SESSION_IDLE_TTL_MS = 30 * 60 * 1000;
const MAX_SESSIONS = 10_000;
const sessions = new Map<string, CursorSessionState>();

export function cursorConversationForRequest(
  req: Pick<AnthropicRequest, "metadata">,
  sessionId: string | undefined,
  now = Date.now(),
): string {
  const explicit = explicitConversationId(req.metadata);
  if (explicit) {
    if (sessionId) recordCursorConversation(sessionId, explicit, now);
    return explicit;
  }

  if (!sessionId) return crypto.randomUUID();

  const existing = sessions.get(sessionId);
  if (existing && now - existing.lastSeen <= SESSION_IDLE_TTL_MS) {
    existing.lastSeen = now;
    return existing.conversationId;
  }

  const conversationId = crypto.randomUUID();
  recordCursorConversation(sessionId, conversationId, now);
  return conversationId;
}

export function recordCursorConversation(
  sessionId: string | undefined,
  conversationId: string | undefined,
  now = Date.now(),
): void {
  if (!sessionId || !conversationId) return;
  sessions.set(sessionId, { conversationId, lastSeen: now });
  evictOldestSessions();
}

export function clearCursorSessionsForTests(): void {
  sessions.clear();
}

function explicitConversationId(metadata: unknown): string | undefined {
  if (!metadata || typeof metadata !== "object" || Array.isArray(metadata)) return undefined;
  const record = metadata as Record<string, unknown>;
  for (const key of ["cursor_chat_id", "cursorChatId", "cursor_resume", "cursorResume"]) {
    const value = record[key];
    if (typeof value === "string" && value) return value;
  }
  const cursor = record.cursor;
  if (cursor && typeof cursor === "object" && !Array.isArray(cursor)) {
    const nested = cursor as Record<string, unknown>;
    for (const key of ["chat_id", "chatId", "resume", "resumeId"]) {
      const value = nested[key];
      if (typeof value === "string" && value) return value;
    }
  }
  return undefined;
}

function evictOldestSessions(): void {
  while (sessions.size > MAX_SESSIONS) {
    const oldest = sessions.keys().next().value;
    if (!oldest) return;
    sessions.delete(oldest);
  }
}
