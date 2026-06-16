import { describe, expect, it } from "bun:test";
import { startTestServer } from "./server.ts";

function bindError(): Error & { code: string } {
  const err = new Error("address already in use") as Error & { code: string };
  err.code = "EADDRINUSE";
  return err;
}

describe("startTestServer", () => {
  it("uses explicit non-zero ports and retries bind failures", () => {
    const seen: number[] = [];
    const server = startTestServer(({ port }) => {
      seen.push(port);
      if (seen.length === 1) throw bindError();
      return { port, stop() {} };
    }, [41000, 41001]);

    expect(server.port).toBe(41001);
    expect(seen).toEqual([41000, 41001]);
  });

  it("rejects port 0 candidates", () => {
    expect(() => startTestServer(({ port }) => ({ port, stop() {} }), [0])).toThrow(
      "Invalid test server port: 0",
    );
  });

  it("rethrows non-bind failures", () => {
    expect(() =>
      startTestServer(() => {
        throw new Error("boom");
      }, [41000]),
    ).toThrow("boom");
  });
});
