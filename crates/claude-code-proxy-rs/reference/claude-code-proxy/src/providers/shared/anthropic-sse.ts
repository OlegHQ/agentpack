/**
 * Emit the Anthropic `message_start` event followed by a `ping`.
 *
 * Every provider stream translator needs this exact sequence before any
 * content blocks, error events, or message_delta can be sent. The caller
 * owns stream-level state (whether start has already been emitted) and the
 * emit function.
 */
export function emitMessageStart(
  emit: (event: string, data: unknown) => void,
  opts: { messageId: string; model: string },
): void {
  emit("message_start", {
    type: "message_start",
    message: {
      id: opts.messageId,
      type: "message",
      role: "assistant",
      model: opts.model,
      content: [],
      stop_reason: null,
      stop_sequence: null,
      usage: {
        input_tokens: 0,
        output_tokens: 0,
        cache_creation_input_tokens: 0,
        cache_read_input_tokens: 0,
      },
    },
  });
  emit("ping", { type: "ping" });
}
