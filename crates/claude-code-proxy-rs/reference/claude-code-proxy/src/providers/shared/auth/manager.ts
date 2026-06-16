export interface TokenResponse {
  access_token: string;
  refresh_token: string;
  expires_in?: number;
}

export function validateTokenResponse<T extends TokenResponse>(t: unknown): asserts t is T {
  if (!t || typeof t !== "object") throw new Error("Invalid token response: not an object");
  const o = t as Record<string, unknown>;
  if (typeof o.access_token !== "string" || !o.access_token)
    throw new Error("Invalid token response: missing access_token");
  if (typeof o.refresh_token !== "string" || !o.refresh_token)
    throw new Error("Invalid token response: missing refresh_token");
  if (
    o.expires_in !== undefined &&
    (typeof o.expires_in !== "number" || !Number.isFinite(o.expires_in) || o.expires_in <= 0)
  )
    throw new Error("Invalid token response: bad expires_in");
}

export interface AuthLifecycle<T extends { expires: number }> {
  getAuth(): Promise<T>;
  forceRefresh(): Promise<T>;
  resetCache(): void;
  setCached(auth: T | undefined): void;
}

export function createAuthLifecycle<T extends { expires: number }>(deps: {
  loadAuth: () => Promise<T | undefined>;
  loginRequiredMessage: string;
  forceRefreshUnauthenticatedMessage: string;
  refreshMarginMs: number;
  refreshNow: (current: T) => Promise<T>;
}): AuthLifecycle<T> {
  let cached: T | undefined;
  let inflight: Promise<T> | undefined;

  async function getAuth(): Promise<T> {
    if (!cached) {
      const stored = await deps.loadAuth();
      if (!stored) throw new Error(deps.loginRequiredMessage);
      cached = stored;
    }
    if (cached.expires - deps.refreshMarginMs > Date.now()) {
      return cached;
    }
    if (inflight) return inflight;
    inflight = deps.refreshNow(cached).finally(() => {
      inflight = undefined;
    });
    return inflight;
  }

  async function forceRefresh(): Promise<T> {
    if (!cached) {
      const stored = await deps.loadAuth();
      if (!stored) throw new Error(deps.forceRefreshUnauthenticatedMessage);
      cached = stored;
    }
    if (inflight) return inflight;
    inflight = deps.refreshNow(cached).finally(() => {
      inflight = undefined;
    });
    return inflight;
  }

  function resetCache(): void {
    cached = undefined;
  }

  function setCached(auth: T | undefined): void {
    cached = auth;
  }

  return { getAuth, forceRefresh, resetCache, setCached };
}
