import type { CliHandlers } from "../types.ts";
import { runDeviceLogin } from "./auth/login.ts";
import { persistInitialTokens } from "./auth/manager.ts";
import { authPath, clearAuth, loadAuth } from "./auth/token-store.ts";
import { printAuthStatus } from "../shared/cli-status.ts";

export const kimiCli: CliHandlers = {
  async login() {
    const tokens = await runDeviceLogin();
    const saved = await persistInitialTokens(tokens);
    console.log(`Auth saved in ${authPath()}`);
    if (saved.userId) console.log(`User: ${saved.userId}`);
    const secs = Math.floor((saved.expires - Date.now()) / 1000);
    console.log(`Expires in ${secs}s`);
  },
  async status() {
    await printAuthStatus({
      loadAuth,
      authPath,
      formatIdentityLine: (auth) => `User: ${auth.userId ?? "(none)"}`,
      extraLines: (auth) => [`Scope: ${auth.scope ?? "(none)"}`],
    });
  },
  async logout() {
    await clearAuth();
    console.log("Logged out");
  },
};
