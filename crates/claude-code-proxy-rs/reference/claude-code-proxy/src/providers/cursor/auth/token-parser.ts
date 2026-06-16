type ParsedCursorAuthTokens = {
  accessToken: string;
  refreshToken: string;
};

export function parseCursorAuthTokens(parsed: unknown): ParsedCursorAuthTokens | undefined {
  if (!parsed || typeof parsed !== "object") return undefined;
  const obj = parsed as Partial<Record<string, unknown>>;
  if (typeof obj.accessToken === "string" && typeof obj.refreshToken === "string") {
    return { accessToken: obj.accessToken, refreshToken: obj.refreshToken };
  }
  return undefined;
}
