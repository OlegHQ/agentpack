export interface AuthStatus {
  expires: number;
}

export interface AuthStatusPrinter<TAuth extends AuthStatus> {
  loadAuth: () => Promise<TAuth | undefined>;
  authPath: () => string;
  formatIdentityLine: (auth: TAuth) => string;
  extraLines?: (auth: TAuth) => string[];
}

export async function printAuthStatus<TAuth extends AuthStatus>(
  options: AuthStatusPrinter<TAuth>,
): Promise<void> {
  const auth = await options.loadAuth();
  if (!auth) {
    console.log("Not authenticated");
    process.exit(1);
  }
  const ms = auth.expires - Date.now();
  console.log(options.formatIdentityLine(auth));
  console.log(`Expires: ${new Date(auth.expires).toISOString()} (in ${Math.floor(ms / 1000)}s)`);
  for (const line of options.extraLines?.(auth) ?? []) {
    console.log(line);
  }
  console.log(`Storage: ${options.authPath()}`);
}
