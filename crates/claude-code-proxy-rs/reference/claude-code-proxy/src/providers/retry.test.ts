import { describe, expect, it } from "bun:test";
import {
  computeBackoffDelay,
  MAX_RATE_LIMIT_RETRIES,
  RETRY_INITIAL_DELAY_MS,
  RETRY_MAX_DELAY_MS,
  retryOn429,
  sleep,
} from "./retry.ts";

const silentLog = {
  debug: () => {},
  info: () => {},
  warn: () => {},
  error: () => {},
  child: () => silentLog,
};

async function expectRejects(promise: Promise<unknown>, check: (err: unknown) => void) {
  try {
    await promise;
  } catch (err) {
    check(err);
    return;
  }
  throw new Error("Expected promise to reject");
}

describe("computeBackoffDelay", () => {
  it("uses jittered exponential backoff without retry-after", () => {
    // equal jitter: result is in [cap/2, cap]
    for (const attempt of [0, 1, 2]) {
      const cap = RETRY_INITIAL_DELAY_MS * 2 ** attempt;
      const { waitMs } = computeBackoffDelay(attempt);
      expect(waitMs).toBeGreaterThanOrEqual(cap / 2);
      expect(waitMs).toBeLessThanOrEqual(cap);
    }
  });

  it("caps exponential backoff at max delay", () => {
    const { waitMs } = computeBackoffDelay(20);
    expect(waitMs).toBeGreaterThanOrEqual(RETRY_MAX_DELAY_MS / 2);
    expect(waitMs).toBeLessThanOrEqual(RETRY_MAX_DELAY_MS);
  });

  it("respects numeric retry-after as seconds", () => {
    expect(computeBackoffDelay(0, "5").waitMs).toBe(5000);
  });

  it("flags retry-after that exceeds budget", () => {
    const out = computeBackoffDelay(0, "120");
    expect(out.waitMs).toBe(RETRY_MAX_DELAY_MS);
    expect(out.exceedsBudget).toBe(true);
  });

  it("rejects non-numeric retry-after garbage", () => {
    const { waitMs } = computeBackoffDelay(0, "1abc");
    expect(waitMs).toBeGreaterThanOrEqual(RETRY_INITIAL_DELAY_MS / 2);
    expect(waitMs).toBeLessThanOrEqual(RETRY_INITIAL_DELAY_MS);
  });

  it("parses HTTP-date retry-after", () => {
    const date = new Date(Date.now() + 5000).toUTCString();
    const out = computeBackoffDelay(0, date);
    expect(out.waitMs).toBeGreaterThan(3500);
    expect(out.waitMs).toBeLessThanOrEqual(5000);
  });
});

describe("sleep", () => {
  it("rejects if signal already aborted", async () => {
    const c = new AbortController();
    c.abort();
    await expectRejects(sleep(1000, c.signal), (err) => expect(err).toBeInstanceOf(Error));
  });

  it("rejects when aborted mid-sleep", async () => {
    const c = new AbortController();
    const p = sleep(1000, c.signal);
    setTimeout(() => c.abort(), 10);
    await expectRejects(p, (err) => expect(err).toBeInstanceOf(Error));
  });
});

describe("retryOn429", () => {
  class FakeRateLimit extends Error {
    constructor(public retryAfter?: string) {
      super("rate limited");
    }
  }

  const retryWithFakeRateLimit = (retryAfter: string, onAttempt: () => void) =>
    retryOn429(
      async () => {
        onAttempt();
        throw new FakeRateLimit(retryAfter);
      },
      {
        log: silentLog,
        classify: (err) =>
          err instanceof FakeRateLimit ? { retryAfter: err.retryAfter } : undefined,
      },
    );

  it("returns successful result without retry", async () => {
    let calls = 0;
    const result = await retryOn429(
      async () => {
        calls++;
        return "ok";
      },
      {
        log: silentLog,
        classify: () => undefined,
      },
    );
    expect(result).toBe("ok");
    expect(calls).toBe(1);
  });

  const retryLimitCases: Array<{
    name: string;
    retryAfter: string;
    expectedCalls: number;
    maxDurationMs?: number;
  }> = [
    {
      name: "retries up to MAX_RATE_LIMIT_RETRIES then throws",
      retryAfter: "0",
      expectedCalls: MAX_RATE_LIMIT_RETRIES + 1,
      maxDurationMs: 2000,
    },
    {
      name: "gives up immediately when retry-after exceeds budget",
      retryAfter: "120",
      expectedCalls: 1,
    },
  ] as const;

  for (const tc of retryLimitCases) {
    it(tc.name, async () => {
      let calls = 0;
      const start = Date.now();
      await expectRejects(
        retryWithFakeRateLimit(tc.retryAfter, () => {
          calls++;
        }),
        (err) => expect(err).toBeInstanceOf(FakeRateLimit),
      );
      expect(calls).toBe(tc.expectedCalls);
      if (tc.maxDurationMs !== undefined) {
        expect(Date.now() - start).toBeLessThan(tc.maxDurationMs);
      }
    });
  }

  it("does not retry non-rate-limit errors", async () => {
    let calls = 0;
    await expectRejects(
      retryOn429(
        async () => {
          calls++;
          throw new Error("other");
        },
        {
          log: silentLog,
          classify: () => undefined,
        },
      ),
      (err) => expect(err).toEqual(new Error("other")),
    );
    expect(calls).toBe(1);
  });

  it("succeeds after a transient 429", async () => {
    let calls = 0;
    const result = await retryOn429(
      async () => {
        calls++;
        if (calls === 1) throw new FakeRateLimit("0");
        return "recovered";
      },
      {
        log: silentLog,
        classify: (err) =>
          err instanceof FakeRateLimit ? { retryAfter: err.retryAfter } : undefined,
      },
    );
    expect(result).toBe("recovered");
    expect(calls).toBe(2);
  });
});
