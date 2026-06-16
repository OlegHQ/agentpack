import { readFile, unlink } from "node:fs/promises";
import { join } from "node:path";
import { cursorBaseUrl } from "../../../config.ts";
import { keychainDelete, keychainGet, keychainSet } from "../../../keychain.ts";
import { cursorAuthFile, legacyConfigDir } from "../../../paths.ts";
import { writeAtomicJson } from "../../shared/auth/atomic-write.ts";
import { parseCursorAuthTokens } from "./token-parser.ts";
import { parseJwtClaims, tokenExpiryMs } from "./jwt.ts";

export interface CursorAuth {
  accessToken: string;
  refreshToken?: string;
  apiKey?: string;
  expires?: number;
  userId?: string;
  email?: string;
  source: string;
}

interface CursorAuthFile {
  accessToken?: string;
  refreshToken?: string;
  apiKey?: string;
}

const KEYCHAIN_SERVICE = "claude-code-proxy.cursor";
const KEYCHAIN_ACCOUNT = "auth";
const REFRESH_EXPIRY_SKEW_MS = 60_000;

export async function loadCursorAuth(env: NodeJS.ProcessEnv = process.env): Promise<CursorAuth | undefined> {
  const envToken = env.CCP_CURSOR_AUTH_TOKEN || env.CURSOR_AUTH_TOKEN;
  if (envToken) return authFromToken(envToken, "environment");

  if (process.platform === "darwin") {
    const raw = keychainGet(KEYCHAIN_SERVICE, KEYCHAIN_ACCOUNT);
    if (!raw) return undefined;
    const parsed = JSON.parse(raw) as CursorAuthFile;
    if (!parsed.accessToken) return undefined;
    return refreshIfNeeded(enrich({ ...parsed, accessToken: parsed.accessToken, source: cursorAuthLocation() }));
  }

  for (const path of authFileCandidates()) {
    try {
      const raw = await readFile(path, "utf8");
      const parsed = JSON.parse(raw) as CursorAuthFile;
      if (parsed.accessToken) return refreshIfNeeded(enrich({ ...parsed, accessToken: parsed.accessToken, source: path }));
    } catch (err: any) {
      if (err?.code !== "ENOENT") throw err;
    }
  }

  return undefined;
}

export async function clearCursorAuth(): Promise<void> {
  if (process.platform === "darwin") {
    keychainDelete(KEYCHAIN_SERVICE, KEYCHAIN_ACCOUNT);
    return;
  }

  for (const path of authFileCandidates()) {
    try {
      await unlink(path);
    } catch (err: any) {
      if (err?.code !== "ENOENT") throw err;
    }
  }
}

export async function saveCursorAuth(auth: CursorAuthFile): Promise<CursorAuth> {
  if (!auth.accessToken) throw new Error("Cursor auth accessToken is required");
  if (process.platform === "darwin") {
    keychainSet(KEYCHAIN_SERVICE, KEYCHAIN_ACCOUNT, JSON.stringify(auth));
    return enrich({ ...auth, accessToken: auth.accessToken, source: cursorAuthLocation() });
  }

  const path = cursorAuthFile();
  await writeAtomicJson(path, auth);
  return enrich({ ...auth, accessToken: auth.accessToken, source: path });
}

export function cursorAuthLocation(): string {
  return process.platform === "darwin" ? "macOS Keychain (claude-code-proxy.cursor)" : cursorAuthFile();
}

export function missingAuthMessage(): string {
  return [
    "Cursor authentication was not found.",
    "Run `claude-code-proxy cursor auth login`, or set CCP_CURSOR_AUTH_TOKEN/CURSOR_AUTH_TOKEN.",
    "The proxy stores Cursor credentials in its own claude-code-proxy.cursor storage, not Cursor Agent's Keychain/auth.json.",
  ].join(" ");
}

export function expiredAuthMessage(auth: CursorAuth): string {
  const expires = auth.expires ? new Date(auth.expires).toISOString() : "unknown";
  return `Cursor access token from ${auth.source} is expired or near expiry (${expires}). Run \`claude-code-proxy cursor auth login\` again or set CCP_CURSOR_AUTH_TOKEN.`;
}

function authFromToken(accessToken: string, source: string): CursorAuth {
  return enrich({ accessToken, source });
}

function enrich(auth: Omit<CursorAuth, "expires" | "userId" | "email"> & Partial<CursorAuth>): CursorAuth {
  const claims = parseJwtClaims(auth.accessToken);
  return {
    ...auth,
    expires: tokenExpiryMs(auth.accessToken),
    userId: typeof claims?.sub === "string" ? claims.sub : auth.userId,
    email: typeof claims?.email === "string" ? claims.email : auth.email,
  };
}

async function refreshIfNeeded(auth: CursorAuth): Promise<CursorAuth> {
  if (!auth.refreshToken || !auth.expires || auth.expires > Date.now() + REFRESH_EXPIRY_SKEW_MS) {
    return auth;
  }
  const refreshed = await refreshCursorAuth(auth.refreshToken);
  if (!refreshed) return auth;
  return saveCursorAuth({
    accessToken: refreshed.accessToken,
    refreshToken: refreshed.refreshToken,
    apiKey: auth.apiKey,
  });
}

async function refreshCursorAuth(refreshToken: string): Promise<{ accessToken: string; refreshToken: string } | undefined> {
  const resp = await fetch(`${cursorBaseUrl().replace(/\/$/, "")}/auth/refresh`, {
    method: "POST",
    headers: {
      "content-type": "application/json",
      authorization: `Bearer ${refreshToken}`,
    },
    body: JSON.stringify({}),
  });
  if (!resp.ok) return undefined;
  const parsed = await resp.json();
  return parseCursorAuthTokens(parsed);
}

function authFileCandidates(): string[] {
  const primary = cursorAuthFile();
  const legacy = join(legacyConfigDir(), "cursor", "auth.json");
  return legacy === primary ? [primary] : [primary, legacy];
}
