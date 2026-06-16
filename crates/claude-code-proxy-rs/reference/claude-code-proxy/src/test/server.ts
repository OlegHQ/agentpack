export interface TestServer {
  stop: () => void;
  port: number;
}

type StartServer = (opts: { port: number }) => TestServer;

const ATTEMPTS = 50;
const MIN_PORT = 40_000;
const PORT_SPAN = 20_000;

export function startTestServer(
  startServer: StartServer,
  portCandidates: Iterable<number> = defaultPortCandidates(),
): TestServer {
  let lastError: unknown;
  let attempts = 0;

  for (const port of portCandidates) {
    attempts += 1;
    if (!Number.isInteger(port) || port < 1 || port > 65535) {
      throw new Error(`Invalid test server port: ${port}`);
    }

    try {
      return startServer({ port });
    } catch (err) {
      if (!isPortUnavailableError(err)) throw err;
      lastError = err;
    }
  }

  throw new Error(`Unable to start test server after ${attempts} explicit port attempts`, {
    cause: lastError,
  });
}

function defaultPortCandidates(): number[] {
  const start = Math.abs(process.pid) % PORT_SPAN;
  return Array.from({ length: ATTEMPTS }, (_, i) => MIN_PORT + ((start + i) % PORT_SPAN));
}

function isPortUnavailableError(err: unknown): boolean {
  if (!(err instanceof Error)) return false;
  const code = (err as Error & { code?: unknown }).code;
  if (code === "EADDRINUSE") return true;
  return /EADDRINUSE|address already in use/i.test(err.message);
}
