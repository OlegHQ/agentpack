# Environment Variables

Set these in your shell profile (`.bashrc`, `.zshrc`, `config.fish`) or in CI.

## State and staging

### `AGENTPACK_HOME`

The user-wide root for cache, the metadata index, the `local/` mirror, and per-project bookkeeping.

**Default:** `$XDG_DATA_HOME/agentpack` (or `$HOME/.local/share/agentpack`) on Unix; `%LOCALAPPDATA%\agentpack` on Windows.

```sh
export AGENTPACK_HOME=/data/agentpack
```

Point it at a network mount for a shared cache, or somewhere project-specific for isolation.

### `AGENTPACK_STAGING_ROOT`

Root for per-harness, per-mode staging trees.

**Default:** `<temp_dir>/agentpack-<project-hash>`.

```sh
export AGENTPACK_STAGING_ROOT=/tmp/agentpack-staging
```

Set this for a stable path when your OS rotates temp directories, or to put staging on a fast local disk in CI.

## Behavior toggles

### `AGENTPACK_KEEP_ATTRIBUTION`

**Default:** unset. Set to `1` / `true` / `yes` to keep your existing AI-attribution settings (Co-Authored-By trailers, "Generated with X" footers) in staged harness configs. By default agentpack forces attribution off in staging. See [Overrides and Attribution](../guides/overrides.md).

### `AGENTPACK_DOT_AGENTS`

**Default:** enabled. Set to `0` to skip merging the project's `./.agents/` overlay into harness staging.

### `AGENTPACK_TUI_THEME`

**Default:** auto-detected from the terminal background (OSC 11 luma / `COLORFGBG`), falling back to `dark`. Set to `light` or `dark` to force the `agentpack mode tui` palette.

## GitHub access

### `GITHUB_TOKEN` / `GH_TOKEN`

**Default:** unset. When set, agentpack sends it as a bearer token for GitHub ref/tag lookups and authenticated downloads. Set one to resolve private repositories or to avoid anonymous API rate limits during heavy resolution. `GITHUB_TOKEN` takes precedence over `GH_TOKEN`.

## Binary paths

Override the path to each harness's executable when it isn't on `PATH` or you need a specific build:

| Variable | Binary |
|---|---|
| `CLAUDE_CODE_PATH` | `claude` |
| `OPENCODE_PATH` | `opencode` |
| `CODEX_PATH` | `codex` |
| `CURSOR_AGENT_PATH` | `cursor-agent` |
| `GROK_PATH` | `grok` |
| `AGY_PATH` | `agy` |

## Summary

| Variable | Default | Purpose |
|---|---|---|
| `AGENTPACK_HOME` | XDG / `%LOCALAPPDATA%` | Cache and state root |
| `AGENTPACK_STAGING_ROOT` | `<temp>/agentpack-<hash>` | Staging root |
| `AGENTPACK_KEEP_ATTRIBUTION` | unset | Keep AI attribution in staging |
| `AGENTPACK_DOT_AGENTS` | enabled | Merge `./.agents/` overlay |
| `AGENTPACK_TUI_THEME` | auto | Force `mode tui` palette |
| `GITHUB_TOKEN` / `GH_TOKEN` | unset | GitHub auth for private repos and rate limits |
| `CLAUDE_CODE_PATH` … `AGY_PATH` | — | Override harness binary paths |
