import { afterEach, describe, expect, it } from "bun:test";
import { CodexHeaderTimeoutError, setCodexHeaderTimeoutForTests } from "./client.ts";

const originalFetch = globalThis.fetch;

afterEach(() => {
  globalThis.fetch = originalFetch;
  setCodexHeaderTimeoutForTests(60_000, 1);
});

describe("CodexHeaderTimeoutError", () => {
  it("identifies header wait timeouts", () => {
    const err = new CodexHeaderTimeoutError(123);

    expect(err.name).toBe("CodexHeaderTimeoutError");
    expect(err.message).toContain("123ms");
  });
});
