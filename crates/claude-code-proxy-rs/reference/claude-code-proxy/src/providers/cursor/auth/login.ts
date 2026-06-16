import { createHash, randomBytes, randomUUID } from "node:crypto";
import { execFile } from "node:child_process";
import { platform } from "node:os";
import { promisify } from "node:util";
import { cursorBaseUrl } from "../../../config.ts";
import { saveCursorAuth, type CursorAuth } from "./token-store.ts";
import { parseCursorAuthTokens } from "./token-parser.ts";

const execFileAsync = promisify(execFile);
const CURSOR_WEBSITE_URL = "https://cursor.com";

interface LoginMetadata {
  uuid: string;
  verifier: string;
}

interface CursorLoginResult {
  accessToken: string;
  refreshToken: string;
}

function base64Url(bytes: Buffer): string {
  return bytes.toString("base64").replace(/\+/g, "-").replace(/\//g, "_").replace(/=/g, "");
}

function sleep(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

export function createCursorLogin(): { loginUrl: string; metadata: LoginMetadata } {
  const verifier = base64Url(randomBytes(32));
  const challenge = base64Url(createHash("sha256").update(verifier).digest());
  const uuid = randomUUID();
  const loginUrl = `${CURSOR_WEBSITE_URL}/loginDeepControl?challenge=${challenge}&uuid=${uuid}&mode=login&redirectTarget=cli`;
  return { loginUrl, metadata: { uuid, verifier } };
}

export async function openCursorLoginUrl(url: string): Promise<void> {
  if (platform() === "darwin") {
    await execFileAsync("open", [url]);
    return;
  }
  if (platform() === "win32") {
    await execFileAsync("cmd", ["/c", "start", "", url]);
    return;
  }
  await execFileAsync("xdg-open", [url]);
}

export async function waitForCursorLogin(
  metadata: LoginMetadata,
  opts: { maxAttempts?: number; onProgress?: (attempt: number) => void } = {},
): Promise<CursorLoginResult | undefined> {
  const maxAttempts = opts.maxAttempts ?? 150;
  let consecutiveErrors = 0;
  for (let attempt = 0; attempt < maxAttempts; attempt++) {
    const delay = Math.min(1000 * Math.pow(1.2, attempt), 10_000);
    try {
      const base = cursorBaseUrl().replace(/\/$/, "");
      const resp = await fetch(`${base}/auth/poll?uuid=${metadata.uuid}&verifier=${metadata.verifier}`, {
        headers: { "content-type": "application/json" },
      });
      if (resp.status === 404) {
        consecutiveErrors = 0;
        opts.onProgress?.(attempt);
        await sleep(delay);
        continue;
      }
      if (!resp.ok) {
        consecutiveErrors += 1;
        if (consecutiveErrors >= 3) return undefined;
        await sleep(delay);
        continue;
      }
      const parsed = await resp.json();
      const parsedTokens = parseCursorAuthTokens(parsed);
      if (!parsedTokens) return undefined;
      return parsedTokens;
    } catch {
      consecutiveErrors += 1;
      if (consecutiveErrors >= 3) return undefined;
      await sleep(delay);
    }
  }
  return undefined;
}

export async function runCursorLogin(): Promise<CursorAuth | undefined> {
  const login = createCursorLogin();
  console.log("Open this URL to authenticate with Cursor:");
  console.log(login.loginUrl);
  console.log();
  try {
    await openCursorLoginUrl(login.loginUrl);
  } catch (err) {
    console.log(`Could not open browser automatically: ${String(err)}`);
  }
  console.log("Waiting for Cursor login...");
  const result = await waitForCursorLogin(login.metadata, {
    onProgress(attempt) {
      if (attempt > 0 && attempt % 10 === 0) process.stdout.write(".");
    },
  });
  if (!result) return undefined;
  return saveCursorAuth({
    accessToken: result.accessToken,
    refreshToken: result.refreshToken,
  });
}
