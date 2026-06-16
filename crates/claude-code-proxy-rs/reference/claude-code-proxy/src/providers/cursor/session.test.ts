import { afterEach, describe, expect, it } from "bun:test";
import {
  clearCursorSessionsForTests,
  cursorConversationForRequest,
  recordCursorConversation,
} from "./session.ts";

afterEach(() => {
  clearCursorSessionsForTests();
});

describe("Cursor session mapping", () => {
  it("reuses a Cursor conversation for the same Claude session", () => {
    const first = cursorConversationForRequest({ metadata: undefined }, "claude-session", 1000);
    const second = cursorConversationForRequest({ metadata: undefined }, "claude-session", 2000);

    expect(second).toBe(first);
  });

  it("uses explicit resume metadata and records it for continuation", () => {
    const explicit = cursorConversationForRequest(
      { metadata: { cursor_chat_id: "cursor-chat-1" } },
      "claude-session",
      1000,
    );
    const continued = cursorConversationForRequest({ metadata: undefined }, "claude-session", 2000);

    expect(explicit).toBe("cursor-chat-1");
    expect(continued).toBe("cursor-chat-1");
  });

  it("records observed server session ids", () => {
    recordCursorConversation("claude-session", "observed-cursor-session", 1000);

    expect(cursorConversationForRequest({ metadata: undefined }, "claude-session", 2000)).toBe(
      "observed-cursor-session",
    );
  });
});
