import { join } from "node:path";
import { codexAuthFile, legacyConfigDir } from "../../../paths.ts";
import { createAuthStore } from "../../shared/auth/token-store.ts";

export interface StoredAuth {
  access: string;
  refresh: string;
  expires: number;
  accountId?: string;
}

const store = createAuthStore<StoredAuth>({
  file: codexAuthFile,
  legacyFile: () => join(legacyConfigDir(), "codex", "auth.json"),
  keychainService: "claude-code-proxy.codex",
});

export const loadAuth = store.loadAuth;
export const saveAuth = store.saveAuth;
export const clearAuth = store.clearAuth;
export const authPath = store.authPath;
