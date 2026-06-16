import type { CliHandlers } from "../types.ts";
import { runDeviceLogin } from "./auth/device.ts";
import { persistInitialTokens } from "./auth/manager.ts";
import { runBrowserLogin } from "./auth/pkce.ts";
import { authPath, clearAuth, loadAuth } from "./auth/token-store.ts";
import { printAuthStatus } from "../shared/cli-status.ts";

export const codexCli: CliHandlers = {
  async login() {
    const tokens = await runBrowserLogin();
    const saved = await persistInitialTokens(tokens);
    console.log(`Auth saved in ${authPath()}`);
    if (saved.accountId) console.log(`Account: ${saved.accountId}`);
  },
  async device() {
    const tokens = await runDeviceLogin();
    const saved = await persistInitialTokens(tokens);
    console.log(`Auth saved in ${authPath()}`);
    if (saved.accountId) console.log(`Account: ${saved.accountId}`);
  },
  async status() {
    await printAuthStatus({
      loadAuth,
      authPath,
      formatIdentityLine: (auth) => `Account: ${auth.accountId ?? "(none)"}`,
    });
  },
  async logout() {
    await clearAuth();
    console.log("Logged out");
  },
};
