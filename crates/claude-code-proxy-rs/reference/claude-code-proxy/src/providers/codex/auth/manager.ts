import { CLIENT_ID, ISSUER, REFRESH_MARGIN_MS } from "./constants.ts";
import { extractAccountId, type TokenResponse } from "./jwt.ts";
import { loadAuth, saveAuth, type StoredAuth } from "./token-store.ts";
import { createAuthLifecycle, validateTokenResponse } from "../../shared/auth/manager.ts";

const lifecycle = createAuthLifecycle<StoredAuth>({
  loadAuth,
  loginRequiredMessage: "Not authenticated. Run: claude-code-proxy codex auth login",
  forceRefreshUnauthenticatedMessage: "Not authenticated",
  refreshMarginMs: REFRESH_MARGIN_MS,
  refreshNow,
});

export const getAuth = lifecycle.getAuth;
export const forceRefresh = lifecycle.forceRefresh;
export const resetCache = lifecycle.resetCache;

async function refreshNow(current: StoredAuth): Promise<StoredAuth> {
  const resp = await fetch(`${ISSUER}/oauth/token`, {
    method: "POST",
    headers: { "Content-Type": "application/x-www-form-urlencoded" },
    body: new URLSearchParams({
      grant_type: "refresh_token",
      refresh_token: current.refresh,
      client_id: CLIENT_ID,
    }).toString(),
  });
  if (!resp.ok) throw new Error(`Token refresh failed: ${resp.status}`);
  const tokens = await resp.json();
  validateTokenResponse(tokens);
  const accountId = extractAccountId(tokens) || current.accountId;
  const next: StoredAuth = {
    access: tokens.access_token,
    refresh: tokens.refresh_token || current.refresh,
    expires: Date.now() + (tokens.expires_in ?? 3600) * 1000,
    accountId,
  };
  await saveAuth(next);
  lifecycle.setCached(next);
  return next;
}

export async function persistInitialTokens(tokens: TokenResponse): Promise<StoredAuth> {
  validateTokenResponse(tokens);
  const auth: StoredAuth = {
    access: tokens.access_token,
    refresh: tokens.refresh_token,
    expires: Date.now() + (tokens.expires_in ?? 3600) * 1000,
    accountId: extractAccountId(tokens),
  };
  await saveAuth(auth);
  lifecycle.setCached(auth);
  return auth;
}
