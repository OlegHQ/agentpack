import type { CliHandlers } from "../types.ts";
import { runCursorLogin } from "./auth/login.ts";
import {
  type CursorAuth,
  clearCursorAuth,
  cursorAuthLocation,
  loadCursorAuth,
  missingAuthMessage,
} from "./auth/token-store.ts";

function printCursorAuth(auth: CursorAuth, mode: "login" | "status"): void {
  const storageLabel = mode === "login" ? "Logged in. Storage:" : "Storage:";
  console.log(`${storageLabel} ${auth.source}`);
  if (auth.email) console.log(`Email: ${auth.email}`);
  if (auth.userId) console.log(`User: ${auth.userId}`);
  if (mode === "login") {
    if (auth.expires) console.log(`Expires: ${new Date(auth.expires).toISOString()}`);
    return;
  }
  if (auth.expires) {
    const ms = auth.expires - Date.now();
    console.log(`Expires: ${new Date(auth.expires).toISOString()} (in ${Math.floor(ms / 1000)}s)`);
  } else {
    console.log("Expires: unknown");
  }
}

export const cursorCli: CliHandlers = {
  async login() {
    const auth = await runCursorLogin();
    if (!auth) {
      console.error("Cursor login did not complete.");
      process.exit(1);
    }
    console.log();
    printCursorAuth(auth, "login");
  },
  async status() {
    const auth = await loadCursorAuth();
    if (!auth) {
      console.log("Not authenticated");
      console.log(missingAuthMessage());
      process.exit(1);
    }
    printCursorAuth(auth, "status");
  },
  async logout() {
    await clearCursorAuth();
    console.log(`Cleared Cursor auth from ${cursorAuthLocation()}`);
  },
};
