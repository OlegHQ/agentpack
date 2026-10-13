# Antigravity

`agentpack agy` launches the Antigravity CLI (`agy`). Antigravity has no config-root override and shares auth and settings with the desktop app under your real profile, so agentpack leaves `HOME` and `~/.gemini` untouched and delivers pack content as a **workspace plugin** instead.

## What the launcher does

```sh
agy --add-dir <cwd>
```

agentpack injects `--add-dir` with the current working directory (where you invoked agentpack — not the pack root) when you don't supply it. Extra arguments are forwarded:

```sh
agentpack agy
agentpack --yolo agy       # adds --dangerously-skip-permissions
```

## The workspace plugin symlink

Pack content is staged into `<staging>/modes/<mode>/agy/agentpack-bundle/` with a root `plugin.json`. **Only** `agentpack agy` creates the workspace symlink that exposes it:

```text
./.agents/plugins/agentpack-bundle  ->  <staging>/.../agy/agentpack-bundle
```

This follows the same current-working-directory rule as Cursor's `--workspace`. The symlink is tracked in a per-project overlay manifest for safe cleanup — bare `sync` and the other launchers never create it and remove any stale one. Add `.agents/plugins/agentpack-bundle` to `.gitignore` if you don't want it tracked.

## Staged layout

```text
agy/agentpack-bundle/
  plugin.json
  skills/ agents/ commands/ rules/
  mcp_config.json      # remote MCP servers use serverUrl
```

## MCP servers and `agy mcp list`

The merged MCP servers are staged as the plugin's `mcp_config.json`, the place Antigravity documents for a plugin's servers ("active when the plugin is enabled"); `agy plugin validate .agents/plugins/agentpack-bundle` counts them (`mcpServers : N processed`).

`agy mcp list` does not show them, and cannot be made to without writing into your profile. Observed on 2026-10-13 with `agy` 1.3.4, in a temporary `HOME`:

- `agy mcp add`, `remove`, `enable`, `disable` and `list` read and write one file, `$HOME/.gemini/config/mcp_config.json`, the global configuration "applied to all sessions".
- With that file empty, `agy mcp list` prints `No MCP servers configured.` while the workspace holds both `.agents/mcp_config.json` and `.agents/plugins/agentpack-bundle/mcp_config.json`; `agy plugin list` prints `No plugins installed.` for the workspace plugin.
- No flag of `agy` and none of `GEMINI_HOME`, `GEMINI_CLI_HOME`, `GEMINI_CONFIG_DIR`, `AGY_HOME`, `AGY_CONFIG_DIR`, `ANTIGRAVITY_APP_DATA_DIR`, `ANTIGRAVITY_EXECUTABLE_DATA_DIR` moves that file; only `HOME` does, and `HOME` also holds the login and settings Antigravity shares with the desktop app.

So the only ways to make a server appear in `agy mcp list` are to write it into your real `~/.gemini/config/mcp_config.json`, where it would apply to every project, or to run `agy` under another `HOME`, where it has no login. agentpack does neither. To use one of the pack's servers in every Antigravity session, add it yourself with `agy mcp add`.

## Hooks are not staged

As with Grok, this is a verified limitation. Antigravity has a real hook engine and `agy plugin validate` statically accepts a Claude-shape `hooks.json`, but `agy` does **not** auto-load hooks from a workspace `.agents/plugins/<name>/` overlay — a live session never fires a `SessionStart` hook from there, and the binary documents `.agents/` as agent *metadata*, not a plugin source. With no config-root redirect, the only way to load hooks is `agy plugin import/install` into the real `~/.gemini` (pollution). Antigravity's hook support therefore stays unsupported.

## Attribution

No verified first-class attribution-off setting. agentpack stages an always-apply plugin rule (`rules/agentpack-no-attribution.md`) as prompt-level guidance and does not modify your real profile.

## Environment

| Variable | Effect |
|---|---|
| `AGY_PATH` | Path to the `agy` binary |
| `AGENTPACK_STAGING_ROOT` | Override the staging root |

See [Environment Variables](../reference/env-vars.md) for the complete list.
