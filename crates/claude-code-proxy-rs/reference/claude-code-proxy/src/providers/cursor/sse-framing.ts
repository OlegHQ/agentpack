import type { CursorUsage } from "./client.ts";
import { emitMessageStart } from "../shared/anthropic-sse.ts";

type CursorSseStopReason = "end_turn" | "tool_use";

interface CursorSseFramingOptions {
  messageId: string;
  model: string;
  emit: (event: string, data: unknown) => boolean;
  mapUsage: (usage?: CursorUsage) => unknown;
}

export interface CursorSseFramer {
  emitThinkingDelta(text: string): void;
  emitTextDelta(text: string): void;
  recordUsage(usage: CursorUsage): void;
  emitFinalMessage(stopReason: CursorSseStopReason): void;
  emitError(error: unknown): void;
  emitToolPauseMessage(emitToolUse: (index: number) => void): void;
  closeOpenBlocks(): void;
  ensureStart(): void;
  nextContentBlockIndex(): number;
}

export function createCursorSseFramer(opts: CursorSseFramingOptions): CursorSseFramer {
  let started = false;
  let thinkingOpen = false;
  let textOpen = false;
  let nextIndex = 0;
  let thinkingIndex = -1;
  let textIndex = -1;
  let finalUsage: CursorUsage | undefined;

  const emit = (event: string, data: unknown) => {
    opts.emit(event, data);
  };

  const nextContentBlockIndex = () => nextIndex++;

  const ensureStart = () => {
    if (started) return;
    started = true;
    emitMessageStart(emit, { messageId: opts.messageId, model: opts.model });
  };

  const openThinking = () => {
    if (thinkingOpen) return;
    ensureStart();
    thinkingOpen = true;
    thinkingIndex = nextIndex++;
    emit("content_block_start", {
      type: "content_block_start",
      index: thinkingIndex,
      content_block: { type: "thinking", thinking: "", signature: "" },
    });
  };

  const openText = () => {
    if (textOpen) return;
    ensureStart();
    textOpen = true;
    textIndex = nextIndex++;
    emit("content_block_start", {
      type: "content_block_start",
      index: textIndex,
      content_block: { type: "text", text: "" },
    });
  };

  const closeOpenBlocks = () => {
    if (thinkingOpen) {
      emit("content_block_stop", { type: "content_block_stop", index: thinkingIndex });
      thinkingOpen = false;
    }
    if (textOpen) {
      emit("content_block_stop", { type: "content_block_stop", index: textIndex });
      textOpen = false;
    }
  };

  const emitFinalMessage = (stopReason: CursorSseStopReason) => {
    ensureStart();
    closeOpenBlocks();
    emit("message_delta", {
      type: "message_delta",
      delta: { stop_reason: stopReason, stop_sequence: null },
      usage: opts.mapUsage(finalUsage),
    });
    emit("message_stop", { type: "message_stop" });
  };

  return {
    emitThinkingDelta(text) {
      openThinking();
      emit("content_block_delta", {
        type: "content_block_delta",
        index: thinkingIndex,
        delta: { type: "thinking_delta", thinking: text },
      });
    },
    emitTextDelta(text) {
      if (thinkingOpen) {
        emit("content_block_stop", { type: "content_block_stop", index: thinkingIndex });
        thinkingOpen = false;
      }
      openText();
      emit("content_block_delta", {
        type: "content_block_delta",
        index: textIndex,
        delta: { type: "text_delta", text },
      });
    },
    recordUsage(usage) {
      finalUsage = usage;
    },
    emitFinalMessage,
    emitError(error) {
      ensureStart();
      closeOpenBlocks();
      emit("error", {
        type: "error",
        error: { type: "api_error", message: String(error) },
      });
    },
    emitToolPauseMessage(emitToolUse) {
      closeOpenBlocks();
      ensureStart();
      const index = nextContentBlockIndex();
      emitToolUse(index);
      emitFinalMessage("tool_use");
    },
    closeOpenBlocks,
    ensureStart,
    nextContentBlockIndex() {
      return nextContentBlockIndex();
    },
  };
}
