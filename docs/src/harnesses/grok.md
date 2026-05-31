# Grok

`agentpack grok` launches the Grok CLI with a redirected home. The installed `grok 0.1.219` rejects `--plugin-dir` and `--settings`, so agentpack configures it through `GROK_HOME` and a staged `config.toml` instead.

## What the launcher does

```sh
GROK_HOME="<staging>/modes/<mode>/grok-home" grok --cwd <project-root>
```

agentpack injects `--cwd` when you don't supply it. Extra arguments are forwarded:

```sh
agentpack grok
agentpack --yolo grok       # adds --always-approve
```

The staged home is **seeded** from your real `~/.grok/` (`config.toml`, `skills`, `agents`, `commands`, `plugins`) so user config keeps working. Auth survives staging rebuilds: agentpack links your real `~/.grok/auth.json` and `mcp_credentials.json` when present.

## Staged layout

```text
grok-home/
  config.toml          # seeded + [plugins].paths + [mcp_servers] + attribution guidance
  auth.json            # linked to real ~/.grok/auth.json
grok/
  agentpack-bundle/
    plugin.json
    commands/ agents/ skills/ rules/
```

Pack content is rendered into `grok/agentpack-bundle/` with a root `plugin.json`, and the staged `config.toml` gets a `[plugins].paths` entry pointing at that bundle. MCP servers are written as Grok-native `[mcp_servers]` TOML in `config.toml`.

## Hooks are not staged

This is a deliberate limitation, not an omission. Grok's `hooks.json` format is Claude-compatible, but `grok inspect --json` confirms it reads hooks only from the **real** `~/.grok/hooks` — a redirected `GROK_HOME` is honored for `config.toml` and MCP but **not** for hook discovery. Bundle hooks loaded via `[plugins].paths` come up disabled (they need interactive trust), and `grok plugin install --trust` writes into the real `~/.grok` (pollution). With no isolated, headless path available, Grok's hook support stays unsupported.

Because Grok also reads Claude-compatible user sources from your real `~/.claude`, agentpack's collision check accounts for both `~/.grok` and `~/.claude` when removing pack copies that would shadow a user install.

## Attribution

`grok 0.1.219` has no verified first-class attribution-off setting. agentpack stages prompt-level guidance (`AGENTS.md` in the staged home) and does not modify your real `~/.grok`.

## Environment

| Variable | Effect |
|---|---|
| `GROK_PATH` | Path to the `grok` binary |
| `AGENTPACK_STAGING_ROOT` | Override the staging root |

See [Environment Variables](../reference/env-vars.md) for the complete list.
