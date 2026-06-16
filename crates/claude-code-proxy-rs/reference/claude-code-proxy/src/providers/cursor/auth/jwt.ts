export interface CursorTokenClaims {
  exp?: number;
  sub?: string;
  email?: string;
  [key: string]: unknown;
}

export function parseJwtClaims(token: string): CursorTokenClaims | undefined {
  const parts = token.split(".");
  if (parts.length !== 3) return undefined;
  try {
    return JSON.parse(Buffer.from(parts[1]!, "base64url").toString()) as CursorTokenClaims;
  } catch {
    return undefined;
  }
}

export function tokenExpiryMs(token: string): number | undefined {
  const exp = parseJwtClaims(token)?.exp;
  return typeof exp === "number" ? exp * 1000 : undefined;
}
