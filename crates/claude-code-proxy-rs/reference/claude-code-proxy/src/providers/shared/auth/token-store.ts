import { readFile, unlink } from "node:fs/promises";
import { keychainGet, keychainSet, keychainDelete } from "../../../keychain.ts";
import { writeAtomicJson } from "./atomic-write.ts";

const KEYCHAIN_ACCOUNT = "auth";

export interface AuthStoreOptions {
  file: () => string;
  legacyFile: () => string;
  keychainService: string;
}

export interface AuthStore<T> {
  loadAuth(): Promise<T | undefined>;
  saveAuth(auth: T): Promise<void>;
  clearAuth(): Promise<void>;
  authPath(): string;
}

export function createAuthStore<T>(options: AuthStoreOptions): AuthStore<T> {
  const { file, legacyFile, keychainService } = options;

  return {
    async loadAuth(): Promise<T | undefined> {
      if (process.platform === "darwin") {
        const raw = keychainGet(keychainService, KEYCHAIN_ACCOUNT);
        if (!raw) return undefined;
        return JSON.parse(raw) as T;
      }

      const primary = file();
      try {
        const raw = await readFile(primary, "utf8");
        return JSON.parse(raw) as T;
      } catch (err: any) {
        if (err?.code !== "ENOENT") throw err;
      }
      const legacy = legacyFile();
      if (legacy === primary) return undefined;
      try {
        const raw = await readFile(legacy, "utf8");
        return JSON.parse(raw) as T;
      } catch (err: any) {
        if (err?.code === "ENOENT") return undefined;
        throw err;
      }
    },

    async saveAuth(auth: T): Promise<void> {
      if (process.platform === "darwin") {
        keychainSet(keychainService, KEYCHAIN_ACCOUNT, JSON.stringify(auth));
        return;
      }

      await writeAtomicJson(file(), auth);
    },

    async clearAuth(): Promise<void> {
      if (process.platform === "darwin") {
        keychainDelete(keychainService, KEYCHAIN_ACCOUNT);
        return;
      }

      for (const path of [file(), legacyFile()]) {
        try {
          await unlink(path);
        } catch (err: any) {
          if (err?.code !== "ENOENT") throw err;
        }
      }
    },

    authPath(): string {
      return process.platform === "darwin" ? "macOS Keychain" : file();
    },
  };
}
