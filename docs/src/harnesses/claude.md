# Claude Code

`agentpack claude` launches [Claude Code](https://www.claude.com/product/claude-code) with your packages staged as a plugin directory. Claude's `--plugin-dir` flag is additive, so this layers on top of your normal configuration rather than replacing it.

## What the launcher does

`sync` builds a single merged plugin bundle, then the launcher runs:

```sh
claude --plugin-dir "<staging>/modes/<mode>/plugins/agentpack-bundle" \
       --settings '{"includeCoAuthoredBy":false,"attribution":{"commit":"","pr":""},"enabledMcpjsonServers":[]}'
```

- `--plugin-dir` points at the staged `agentpack-bundle`.
- `--settings` receives inline JSON from the project/mode-specific staged settings. It contains attribution controls and the enabled MCP server names. Keeping attribution on removes only the attribution overrides.

Extra arguments are forwarded to `claude`:

```sh
agentpack claude --model opus
agentpack --yolo claude          # adds --dangerously-skip-permissions
```

`CLAUDE_CONFIG_DIR` is intentionally **never** set. Claude derives its keychain credential namespace from that variable, so a per-project value would forget your login on every project switch. Your real `~/.claude.json`, `~/.claude/settings.json`, and keychain entry are reused as-is.

## What the bundle contains

```text
agentpack-bundle/
  .claude-plugin/plugin.json
  commands/   agents/   skills/    # converted markdown artifacts
  hooks/  matchers/  core/  ...     # raw Claude support directories
  .mcp.json                         # merged MCP servers (see MCP Servers)
  agentpack-settings.json            # project/mode-specific launch settings
```

agentpack does **not** copy your user-scoped `~/.claude/commands`, `agents`, or `skills` into the bundle. Claude already reads those from `$HOME`, and copying them would produce duplicate slash commands (e.g. both `/code-tutor` and `/agentpack-bundle:code-tutor`).

## Artifact handling

| Artifact | In the bundle |
|---|---|
| Commands | `commands/<name>.md`, Claude command frontmatter |
| Agents | `agents/<name>.md`, Claude agent frontmatter |
| Skills | `skills/<name>/`, normalized skill |
| Rules | Best-effort skill fallback (Claude has no first-class rule files) |
| Hooks | Supported — rendered into the bundle's `hooks/` |
| MCP | Merged into `.mcp.json` at the bundle root |

Plugin-provided guidance is surfaced through a `SessionStart` hook that emits it as Claude's `additionalContext`, so the model sees it at the start of every session. See [Cross-Harness Conversion](./conversion.md) for the full mapping.

## Attribution

By default `sync` writes `agentpack-settings.json` inside the staged bundle with AI attribution forced off (`includeCoAuthoredBy = false`, empty commit/PR attribution). The launcher reads this project/mode-specific file and passes its JSON via `--settings`, which loads at `flagSettings` scope — above your user, project, and local settings. Inline settings avoid shared mutable allowlists and project-dependent settings filenames in credential handling. No global `$AGENTPACK_HOME/claude-settings.json` is read or rewritten. Your real config files are never modified. Set `AGENTPACK_KEEP_ATTRIBUTION=1` to keep your existing values. See [Overrides and Attribution](../guides/overrides.md).

## Environment

| Variable | Effect |
|---|---|
| `CLAUDE_CODE_PATH` | Path to the `claude` binary |
| `AGENTPACK_STAGING_ROOT` | Override the staging root |
| `AGENTPACK_HOME` | Override the cache/state root |

See [Environment Variables](../reference/env-vars.md) for the complete list.
