import { join } from "node:path";
import { kimiAuthFile, legacyConfigDir } from "../../../paths.ts";
import { createAuthStore } from "../../shared/auth/token-store.ts";

export interface StoredAuth {
  access: string;
  refresh: string;
  expires: number;
  scope?: string;
  userId?: string;
}

const store = createAuthStore<StoredAuth>({
  file: kimiAuthFile,
  legacyFile: () => join(legacyConfigDir(), "kimi", "auth.json"),
  keychainService: "claude-code-proxy.kimi",
});

export const loadAuth = store.loadAuth;
export const saveAuth = store.saveAuth;
export const clearAuth = store.clearAuth;
export const authPath = store.authPath;
