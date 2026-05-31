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
