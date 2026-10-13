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

## Hooks

Hooks are staged as `hooks.json` at the root of the staged plugin, in Antigravity's own format: one named hook, `agentpack`, whose keys are Antigravity's events. Antigravity loads a plugin's hooks only from `<plugin>/hooks.json` (its plugin guide: "native plugins … must place `hooks.json` directly at the plugin root"), not from `hooks/hooks.json`, and not in Claude Code's shape; `agy plugin validate` reports `hooks : 1 processed` for the staged bundle (agy 1.3.4, 2026-10-13).

| Hook written for | Staged as | Notes |
|---|---|---|
| `SessionStart` | `SessionStart` | `additionalContext` is injected as a system message |
| `PreToolUse`, `PermissionRequest` | `PreToolUse`, matcher `*` | each hook's own matcher is applied by agentpack; Claude Code tool names match Antigravity's (`Bash` = `run_command`, `Read` = `view_file`, `Write`/`Edit` = `write_to_file`/`replace_file_content`, `Grep` = `grep_search`, `Glob` = `find_by_name`, `WebFetch` = `read_url_content`, `WebSearch` = `search_web`) |
| `PostToolUse` | `PostToolUse`, matcher `*` | |
| `UserPromptSubmit` | `PreInvocation` | Antigravity has no prompt-submit event; the hook runs before each model call, with a warning |
| `Stop` | `Stop` | a blocking result becomes `"decision": "continue"` |
| any other event | skipped | with a warning that names the event |

Every entry calls `agentpack hook-exec dispatch`, which runs the package's handlers in a staged copy of the package and answers in Antigravity's output contract. A hook that does not object returns no decision: agentpack never answers `"allow"` for a tool on a hook's behalf, because for Antigravity that approves the tool without asking you.

Nothing is written to `~/.gemini`. The plugin reaches Antigravity through the workspace link `./.agents/plugins/agentpack-bundle`, as its skills and MCP servers do, and Antigravity loads workspace hooks once the folder is trusted. What `agy plugin validate` confirms is that the file is found and parsed; that a session fires the hooks of a workspace plugin can only be seen in a session. An observation with agy 1.0.3 and a Claude-shape `hooks/hooks.json` found that it did not; that file was in the wrong place and shape, so it does not settle the question for the file staged now.

## Attribution

No verified first-class attribution-off setting. agentpack stages an always-apply plugin rule (`rules/agentpack-no-attribution.md`) as prompt-level guidance and does not modify your real profile.

## Environment

| Variable | Effect |
|---|---|
| `AGY_PATH` | Path to the `agy` binary |
| `AGENTPACK_STAGING_ROOT` | Override the staging root |

See [Environment Variables](../reference/env-vars.md) for the complete list.
