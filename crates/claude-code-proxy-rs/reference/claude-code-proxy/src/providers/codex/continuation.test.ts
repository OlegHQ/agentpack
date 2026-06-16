import { afterEach, describe, expect, it } from "bun:test";
import {
  clearAllContinuationsForTests,
  continuationCandidate,
  hasContinuationForTests,
  recordContinuation,
} from "./continuation.ts";
import type { ResponsesInputItem, ResponsesRequest } from "./translate/request.ts";

function requestWithInput(
  input: ResponsesInputItem[],
  extra: Partial<ResponsesRequest> = {},
): ResponsesRequest {
  return {
    model: "gpt-5.5",
    input,
    store: false,
    stream: true,
    text: { verbosity: "low" },
    ...extra,
  };
}

afterEach(() => {
  clearAllContinuationsForTests();
});

describe("continuationCandidate", () => {
  it("is disabled unless the feature is enabled", () => {
    const req = requestWithInput([
      { type: "message", role: "user", content: [{ type: "input_text", text: "one" }] },
    ]);

    expect(continuationCandidate("s1", req, false)).toEqual({
      inputDeltaCount: 1,
      disabledReason: "disabled",
    });
  });

  it("uses previous response id for append-only input", () => {
    const first = requestWithInput([
      { type: "message", role: "user", content: [{ type: "input_text", text: "one" }] },
    ]);
    recordContinuation("s1", first, "resp_1", [
      { type: "message", role: "assistant", content: [{ type: "output_text", text: "two" }] },
    ]);

    const second = requestWithInput([
      { type: "message", role: "user", content: [{ type: "input_text", text: "one" }] },
      { type: "message", role: "assistant", content: [{ type: "output_text", text: "two" }] },
      { type: "message", role: "user", content: [{ type: "input_text", text: "three" }] },
    ]);

    const result = continuationCandidate("s1", second, true);

    expect(result.previousResponseId).toBe("resp_1");
    expect(result.inputDeltaCount).toBe(1);
    expect(result.inputDelta).toEqual([
      { type: "message", role: "user", content: [{ type: "input_text", text: "three" }] },
    ]);
  });

  it("clears state when the prompt signature changes", () => {
    const first = requestWithInput([
      { type: "message", role: "user", content: [{ type: "input_text", text: "one" }] },
    ]);
    recordContinuation("s1", first, "resp_1", []);

    const second = requestWithInput(first.input, { tool_choice: "none" });

    expect(continuationCandidate("s1", second, true).disabledReason).toBe("prompt_changed");
    expect(hasContinuationForTests("s1")).toBe(false);
  });

  it("preserves state when an exact repeat has no input delta", () => {
    const first = requestWithInput([
      { type: "message", role: "user", content: [{ type: "input_text", text: "one" }] },
    ]);
    recordContinuation("s1", first, "resp_1", []);

    expect(continuationCandidate("s1", first, true).disabledReason).toBe("empty_delta");
    expect(
      continuationCandidate(
        "s1",
        requestWithInput([
          ...first.input,
          { type: "message", role: "user", content: [{ type: "input_text", text: "two" }] },
        ]),
        true,
      ).previousResponseId,
    ).toBe("resp_1");
  });

  it("clears state when the transcript is not a strict prefix", () => {
    const first = requestWithInput([
      { type: "message", role: "user", content: [{ type: "input_text", text: "one" }] },
    ]);
    recordContinuation("s1", first, "resp_1", []);

    const second = requestWithInput([
      { type: "message", role: "user", content: [{ type: "input_text", text: "changed" }] },
      { type: "message", role: "user", content: [{ type: "input_text", text: "three" }] },
    ]);

    expect(continuationCandidate("s1", second, true).disabledReason).toBe("not_append_only");
    expect(hasContinuationForTests("s1")).toBe(false);
  });

  it("handles previous function call output as visible transcript", () => {
    const first = requestWithInput([
      { type: "message", role: "user", content: [{ type: "input_text", text: "read" }] },
    ]);
    recordContinuation("s1", first, "resp_1", [
      {
        type: "function_call",
        call_id: "call_1",
        name: "Read",
        arguments: '{"file_path":"/tmp/a"}',
      },
    ]);

    const second = requestWithInput([
      { type: "message", role: "user", content: [{ type: "input_text", text: "read" }] },
      {
        type: "function_call",
        call_id: "call_1",
        name: "Read",
        arguments: '{"file_path":"/tmp/a"}',
      },
      { type: "function_call_output", call_id: "call_1", output: "contents" },
      { type: "message", role: "user", content: [{ type: "input_text", text: "summarize" }] },
    ]);

    const result = continuationCandidate("s1", second, true);

    expect(result.previousResponseId).toBe("resp_1");
    expect(result.inputDelta).toEqual([
      { type: "function_call_output", call_id: "call_1", output: "contents" },
      { type: "message", role: "user", content: [{ type: "input_text", text: "summarize" }] },
    ]);
  });

  it("expires stale state", () => {
    const first = requestWithInput([
      { type: "message", role: "user", content: [{ type: "input_text", text: "one" }] },
    ]);
    recordContinuation("s1", first, "resp_1", [], 1_000);

    expect(continuationCandidate("s1", first, true, 31 * 60 * 1_000).disabledReason).toBe(
      "missing_state",
    );
    expect(hasContinuationForTests("s1")).toBe(false);
  });

  it("clears state when the response id is missing", () => {
    const first = requestWithInput([
      { type: "message", role: "user", content: [{ type: "input_text", text: "one" }] },
    ]);
    recordContinuation("s1", first, "resp_1", []);

    recordContinuation("s1", first, undefined, []);

    expect(hasContinuationForTests("s1")).toBe(false);
  });

  it("does not store oversized session transcripts", () => {
    const largeText = "x".repeat(2_000_001);
    const first = requestWithInput([
      { type: "message", role: "user", content: [{ type: "input_text", text: largeText }] },
    ]);

    recordContinuation("s1", first, "resp_1", []);

    expect(continuationCandidate("s1", first, true).disabledReason).toBe("missing_state");
  });
});
