import { aliasProvider, type AliasProvider } from "../config.ts";
import type { Provider } from "./types.ts";
import { codexProvider } from "./codex/index.ts";
import { kimiProvider } from "./kimi/index.ts";
import { cursorProvider } from "./cursor/index.ts";
import { isCursorModel } from "./cursor/translate/model.ts";

export const ANTHROPIC_STYLE_ALIASES = new Set([
  "haiku",
  "claude-haiku-4-5",
  "claude-haiku-4-5-20251001",
  "sonnet",
  "claude-sonnet-4-6",
  "opus",
  "claude-opus-4-7",
]);

const PROVIDERS: Record<string, Provider> = {
  codex: codexProvider,
  kimi: kimiProvider,
  cursor: cursorProvider,
};

export function getProvider(name: string): Provider {
  const p = PROVIDERS[name];
  if (!p) {
    throw new Error(`Unknown provider: ${name}. Available: ${Object.keys(PROVIDERS).join(", ")}`);
  }
  return p;
}

export function listProviders(): string[] {
  return Object.keys(PROVIDERS);
}

export function allProviders(): Provider[] {
  return Object.values(PROVIDERS);
}

export function normalizeIncomingModel(model: string): string {
  return model.replace(/\[1m\]$/i, "");
}

export function providerForModel(
  model: string,
  aliasProviderOverride?: AliasProvider,
): Provider | undefined {
  const normalizedModel = normalizeIncomingModel(model);
  if (ANTHROPIC_STYLE_ALIASES.has(normalizedModel))
    return getProvider(aliasProviderOverride ?? aliasProvider());
  if (isCursorModel(normalizedModel)) return cursorProvider;
  for (const p of allProviders()) {
    if (p.supportedModels.has(normalizedModel)) return p;
  }
  return undefined;
}

export function groupSupportedModelsByProvider(): Map<string, string[]> {
  const groups = new Map<string, string[]>();
  for (const { model, provider } of allSupportedModels()) {
    const models = groups.get(provider) ?? [];
    models.push(model);
    groups.set(provider, models);
  }
  return groups;
}

export function allSupportedModels(): Array<{ model: string; provider: string }> {
  const out: Array<{ model: string; provider: string }> = [];
  const activeAliasProvider = aliasProvider();
  for (const p of allProviders()) {
    for (const m of p.supportedModels) out.push({ model: m, provider: p.name });
    if (p.name === activeAliasProvider) {
      for (const m of ANTHROPIC_STYLE_ALIASES) out.push({ model: m, provider: p.name });
    }
  }
  return out;
}
