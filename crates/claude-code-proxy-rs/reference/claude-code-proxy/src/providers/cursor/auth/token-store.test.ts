import { describe, expect, it } from "bun:test";
import { loadCursorAuth } from "./token-store.ts";
import { parseJwtClaims, tokenExpiryMs } from "./jwt.ts";
import { jwt } from "../cursor-test-helpers.ts";

describe("Cursor auth token discovery", () => {
  it("loads CCP_CURSOR_AUTH_TOKEN without reading Cursor storage", async () => {
    const token = jwt({ sub: "user_1", email: "user@example.com", exp: 2_000_000_000 });

    const auth = await loadCursorAuth({ CCP_CURSOR_AUTH_TOKEN: token });

    expect(auth?.accessToken).toBe(token);
    expect(auth?.source).toBe("environment");
    expect(auth?.userId).toBe("user_1");
    expect(auth?.email).toBe("user@example.com");
    expect(auth?.expires).toBe(2_000_000_000_000);
  });

  it("parses JWT claims and expiration", () => {
    const token = jwt({ sub: "user_2", exp: 123 });

    expect(parseJwtClaims(token)?.sub).toBe("user_2");
    expect(tokenExpiryMs(token)).toBe(123_000);
  });
});
