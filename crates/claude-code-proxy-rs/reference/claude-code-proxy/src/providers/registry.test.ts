import { afterEach, describe, expect, it } from "bun:test";
import { mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import {
  allSupportedModels,
  ANTHROPIC_STYLE_ALIASES,
  getProvider,
  groupSupportedModelsByProvider,
  normalizeIncomingModel,
  providerForModel,
} from "./registry.ts";
import { loadConfig } from "../config.ts";

afterEach(() => {
  loadConfig({ forceReload: true });
});

function providersFor(model: string): string[] {
  return allSupportedModels()
    .filter((entry) => entry.model === model)
    .map((entry) => entry.provider);
}

function withAliasConfig<T>(aliasProvider: string, test: (configPath: string) => T): T {
  const dir = mkdtempSync(join(tmpdir(), "ccp-alias-provider-"));
  const configPath = join(dir, "config.json");
  writeFileSync(configPath, JSON.stringify({ aliasProvider }));
  try {
    return test(configPath);
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
}

describe("provider routing", () => {
  it("routes fast Codex model aliases after context suffix normalization", () => {
    expect(normalizeIncomingModel("gpt-5.4-fast[1m]")).toBe("gpt-5.4-fast");
    expect(providerForModel("gpt-5.4-fast[1m]")?.name).toBe("codex");
  });

  it("routes Anthropic-style aliases to Codex by default", () => {
    loadConfig({ env: {}, forceReload: true });

    for (const alias of ANTHROPIC_STYLE_ALIASES) {
      expect(providerForModel(alias)?.name).toBe("codex");
    }
  });

  it("routes every Anthropic-style alias to Kimi when env selects Kimi", () => {
    loadConfig({ env: { CCP_ALIAS_PROVIDER: "kimi" }, forceReload: true });

    for (const alias of ANTHROPIC_STYLE_ALIASES) {
      expect(providerForModel(alias)?.name).toBe("kimi");
    }
  });

  it("lets session affinity override the global alias provider", () => {
    loadConfig({ env: {}, forceReload: true });

    expect(providerForModel("sonnet", "kimi")?.name).toBe("kimi");
  });

  it("routes aliases to Codex when config file selects Codex", () => {
    withAliasConfig("codex", (path) => {
      loadConfig({ configPath: path, env: {}, forceReload: true });
      expect(providerForModel("claude-opus-4-7")?.name).toBe("codex");
    });
  });

  it("lets env override config file alias provider", () => {
    withAliasConfig("codex", (path) => {
      loadConfig({ configPath: path, env: { CCP_ALIAS_PROVIDER: "kimi" }, forceReload: true });
      expect(providerForModel("haiku")?.name).toBe("kimi");
    });
  });

  it("lists aliases only under the active alias provider", () => {
    loadConfig({ env: { CCP_ALIAS_PROVIDER: "codex" }, forceReload: true });

    expect(providersFor("sonnet")).toEqual(["codex"]);
  });

  it("groups supported models without changing provider or model order", () => {
    loadConfig({ env: { CCP_ALIAS_PROVIDER: "kimi" }, forceReload: true });

    const grouped = groupSupportedModelsByProvider();
    const expected = new Map<string, string[]>();
    for (const { model, provider } of allSupportedModels()) {
      const models = expected.get(provider) ?? [];
      models.push(model);
      expected.set(provider, models);
    }

    expect([...grouped.keys()]).toEqual([...expected.keys()]);
    expect([...grouped.entries()]).toEqual([...expected.entries()]);
    expect(grouped.get("kimi")).toEqual(expect.arrayContaining([...ANTHROPIC_STYLE_ALIASES]));
    expect(grouped.get("codex") ?? []).not.toEqual(expect.arrayContaining(["sonnet"]));
  });

  it("does not duplicate supported model entries", () => {
    for (const env of [{}, { CCP_ALIAS_PROVIDER: "codex" }]) {
      loadConfig({ env, forceReload: true });
      const models = allSupportedModels().map(({ model }) => model);
      expect(new Set(models).size).toBe(models.length);
    }
  });

  it("keeps concrete model routing unchanged", () => {
    loadConfig({ env: { CCP_ALIAS_PROVIDER: "codex" }, forceReload: true });

    expect(providerForModel("kimi-for-coding")?.name).toBe("kimi");
    expect(providerForModel("gpt-5.4-fast")?.name).toBe("codex");
    expect(providerForModel("cursor")?.name).toBe("cursor");
    expect(providerForModel("cursor-plan")?.name).toBe("cursor");
    expect(providerForModel("cursor:claude-sonnet-4-6")?.name).toBe("cursor");
  });

  it("lists Cursor aliases in supported models", () => {
    const cursorModels = allSupportedModels()
      .filter((entry) => entry.provider === "cursor")
      .map((entry) => entry.model);

    expect(cursorModels).toContain("cursor");
    expect(cursorModels).toContain("cursor-plan");
    expect(cursorModels).toContain("composer-2.5-fast");
  });

  it("keeps auth CLI commands available through the registry", () => {
    const codex = getProvider("codex").cli;
    expect(Object.keys(codex).sort()).toEqual(["device", "login", "logout", "status"]);
    expect(typeof codex.login).toBe("function");
    expect(typeof codex.device).toBe("function");
    expect(typeof codex.status).toBe("function");
    expect(typeof codex.logout).toBe("function");

    const kimi = getProvider("kimi").cli;
    expect(Object.keys(kimi).sort()).toEqual(["login", "logout", "status"]);
    expect(typeof kimi.login).toBe("function");
    expect(kimi.device).toBeUndefined();
    expect(typeof kimi.status).toBe("function");
    expect(typeof kimi.logout).toBe("function");

    const cursor = getProvider("cursor").cli;
    expect(Object.keys(cursor).sort()).toEqual(["login", "logout", "status"]);
    expect(typeof cursor.login).toBe("function");
    expect(cursor.device).toBeUndefined();
    expect(typeof cursor.status).toBe("function");
    expect(typeof cursor.logout).toBe("function");
  });
});
