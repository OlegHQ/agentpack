import { CLIENT_ID, oauthHost, REFRESH_MARGIN_MS } from "./constants.ts";
import { commonHeaders } from "./headers.ts";
import { extractUserId } from "./jwt.ts";
import type { TokenResponse } from "./login.ts";
import { clearAuth, loadAuth, saveAuth, type StoredAuth } from "./token-store.ts";
import { createAuthLifecycle, validateTokenResponse } from "../../shared/auth/manager.ts";
import { createLogger } from "../../../log.ts";

const log = createLogger("kimi.auth");

const RETRYABLE_STATUSES = new Set([429, 500, 502, 503, 504]);
const MAX_REFRESH_ATTEMPTS = 3;

const lifecycle = createAuthLifecycle<StoredAuth>({
  loadAuth,
  loginRequiredMessage: "Not authenticated. Run: claude-code-proxy kimi auth login",
  forceRefreshUnauthenticatedMessage: "Not authenticated",
  refreshMarginMs: REFRESH_MARGIN_MS,
  refreshNow,
});

export const getAuth = lifecycle.getAuth;
export const forceRefresh = lifecycle.forceRefresh;
export const resetCache = lifecycle.resetCache;

export class KimiAuthUnauthorizedError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "KimiAuthUnauthorizedError";
  }
}

export async function persistInitialTokens(tokens: TokenResponse): Promise<StoredAuth> {
  validateTokenResponse(tokens);
  const auth = tokensToStored(tokens);
  await saveAuth(auth);
  lifecycle.setCached(auth);
  return auth;
}

function tokensToStored(tokens: TokenResponse): StoredAuth {
  return {
    access: tokens.access_token,
    refresh: tokens.refresh_token,
    expires: Date.now() + (tokens.expires_in ?? 900) * 1000,
    scope: tokens.scope,
    userId: extractUserId(tokens.access_token),
  };
}

async function refreshNow(current: StoredAuth): Promise<StoredAuth> {
  if (!current.refresh) {
    throw new KimiAuthUnauthorizedError("No refresh token stored; re-authenticate");
  }
  const headers = await commonHeaders();

  for (let attempt = 0; attempt < MAX_REFRESH_ATTEMPTS; attempt++) {
    let resp: Response;
    try {
      resp = await fetch(`${oauthHost()}/api/oauth/token`, {
        method: "POST",
        headers: { ...headers, "Content-Type": "application/x-www-form-urlencoded" },
        body: new URLSearchParams({
          client_id: CLIENT_ID,
          grant_type: "refresh_token",
          refresh_token: current.refresh,
        }).toString(),
      });
    } catch (err) {
      log.warn("refresh network error", { attempt, err: String(err) });
      await backoff(attempt);
      continue;
    }

    if (resp.status === 200) {
      const tokens: unknown = await resp.json();
      validateTokenResponse<TokenResponse>(tokens);
      const next: StoredAuth = {
        ...tokensToStored(tokens),
        refresh: tokens.refresh_token || current.refresh,
        userId: extractUserId(tokens.access_token) || current.userId,
      };
      await saveAuth(next);
      lifecycle.setCached(next);
      return next;
    }

    if (resp.status === 401 || resp.status === 403) {
      lifecycle.setCached(undefined);
      await clearAuth().catch(() => undefined);
      const body = (await resp.json().catch(() => ({}))) as { error_description?: string };
      throw new KimiAuthUnauthorizedError(
        body.error_description || `Token refresh unauthorized (${resp.status})`,
      );
    }

    if (!RETRYABLE_STATUSES.has(resp.status)) {
      throw new Error(`Token refresh failed: ${resp.status}`);
    }

    log.warn("refresh retryable error", { attempt, status: resp.status });
    await backoff(attempt);
  }
  throw new Error(`Token refresh failed after ${MAX_REFRESH_ATTEMPTS} attempts`);
}

function backoff(attempt: number): Promise<void> {
  const ms = 2 ** attempt * 1000;
  return new Promise((r) => setTimeout(r, ms));
}
