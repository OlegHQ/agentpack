#!/usr/bin/env bun
import { startServer } from "./server.ts";
import { createLogger, logFile } from "./log.ts";
import {
  port as configPort,
  configPath,
  configOverrideSummaryLines,
} from "./config.ts";
import { existsSync } from "node:fs";
import {
  getProvider,
  groupSupportedModelsByProvider,
  listProviders,
} from "./providers/registry.ts";
import type { CliHandlers } from "./providers/types.ts";

declare const BUILD_VERSION: string | undefined;
const VERSION = typeof BUILD_VERSION === "string" ? BUILD_VERSION : "dev";

const log = createLogger("cli");

async function main() {
  const args = process.argv.slice(2);
  const [first, ...rest] = args;

  if (first === "--version" || first === "-v" || first === "version") {
    console.log(`claude-code-proxy ${VERSION}`);
    return;
  }

  if (!first || first === "serve") {
    const port = configPort();
    startServer({ port });
    console.log(`Proxy listening on http://localhost:${port}`);
    console.log(`Logs: ${logFile()}`);
    printConfigSummary();
    console.log();
    console.log("Providers are selected per-request by ANTHROPIC_MODEL:");
    printSupportedModels();
    console.log();
    console.log("Configure Claude Code (pick a model from above):");
    console.log(`  export ANTHROPIC_BASE_URL="http://localhost:${port}"`);
    console.log(`  export ANTHROPIC_AUTH_TOKEN="anything"`);
    console.log(
      `  export ANTHROPIC_MODEL="gpt-5.5"                         # or kimi-for-coding[1m]`,
    );
    console.log(
      `  export ANTHROPIC_SMALL_FAST_MODEL="gpt-5.4-mini"          # background / title-gen`,
    );
    console.log(`  export CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC="1"`);
    return;
  }

  if (first === "models") {
    printSupportedModels({ full: rest.includes("--full") });
    return;
  }

  if (listProviders().includes(first)) {
    const provider = getProvider(first);
    await runProviderCommand(provider.name, provider.cli, rest);
    return;
  }

  usageAndExit();
}

async function runProviderCommand(name: string, cli: CliHandlers, args: string[]): Promise<void> {
  const [group, sub] = args;
  if (group !== "auth") usageAndExit();

  switch (sub) {
    case "login":
      if (!cli.login) {
        console.error(`${name}: browser login not supported`);
        process.exit(2);
      }
      await cli.login();
      process.exit(0);
    case "device":
      if (!cli.device) {
        console.error(`${name}: device login not supported`);
        process.exit(2);
      }
      await cli.device();
      process.exit(0);
    case "status":
      await cli.status();
      return;
    case "logout":
      await cli.logout();
      return;
    default:
      usageAndExit();
  }
}

function usageAndExit(): never {
  const providers = listProviders().join("|");
  const models = compactSupportedModelsSummary();
  console.log(`Usage:
  claude-code-proxy serve                      Run proxy (PORT env or config.json port, default 18765)
                                               Upstream is chosen per-request from ANTHROPIC_MODEL.
  claude-code-proxy models                     Show compact model list
  claude-code-proxy models --full              Show every supported model alias
  claude-code-proxy <provider> auth login      Browser OAuth
  claude-code-proxy <provider> auth device     Device-code OAuth
  claude-code-proxy <provider> auth status     Show current auth
  claude-code-proxy <provider> auth logout     Clear stored auth
  claude-code-proxy --version                  Show version

Providers: ${providers}
Models:
${models}
`);
  process.exit(2);
}

function printSupportedModels(opts: { full?: boolean } = {}): void {
  const groups = groupSupportedModelsByProvider();
  for (const provider of listProviders()) {
    const models = groups.get(provider) ?? [];
    console.log(`  ${provider}: ${formatProviderModels(provider, models, opts)}`);
  }
}

function compactSupportedModelsSummary(): string {
  const groups = groupSupportedModelsByProvider();
  return listProviders()
    .map((provider) => `  ${provider}: ${formatProviderModels(provider, groups.get(provider) ?? [])}`)
    .join("\n");
}

function formatProviderModels(provider: string, models: string[], opts: { full?: boolean } = {}): string {
  if (opts.full || provider !== "cursor") return models.join(", ");
  return formatCursorModels(models);
}

function formatCursorModels(models: string[]): string {
  const legacy = models.filter((model) => !model.includes(":"));
  const rawIds = new Set<string>();
  for (const model of models) {
    for (const prefix of ["cursor:", "cursor-plan:", "cursor-ask:"]) {
      if (model.startsWith(prefix)) rawIds.add(model.slice(prefix.length));
    }
  }

  const examples = [
    "cursor:gemini-3.1-pro",
    "cursor:gpt-5.5-high",
    "cursor-plan:gpt-5.5-high",
    "cursor-ask:gpt-5.5-high",
  ].filter((model) => models.includes(model));

  return [
    ...legacy,
    examples.length ? `examples: ${examples.join(", ")}` : undefined,
    `${rawIds.size} Cursor catalog models via cursor:<id>, cursor-plan:<id>, cursor-ask:<id>`,
    "run `claude-code-proxy models --full` for all aliases",
  ].filter(Boolean).join("; ");
}

function printConfigSummary(): void {
  const path = configPath();
  if (existsSync(path)) {
    console.log(`Config: ${path}`);
  }

  const overrides = configOverrideSummaryLines();

  if (overrides.length > 0) {
    console.log("Overrides:");
    for (const o of overrides) {
      console.log(`  ${o}`);
    }
  }
}

main().catch((err) => {
  log.error("cli fatal", { err: String(err), stack: (err as Error)?.stack });
  console.error(err);
  process.exit(1);
});
