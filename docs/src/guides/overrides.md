# Overrides and Attribution

How to bend agentpack's defaults without editing shared packages, plus how attribution is handled in staged configs.

## Override a dependency with a local copy

To develop a package and test it in a consumer at the same time, swap the remote dependency for a `path`:

```toml
[dependencies]
"shared-rules" = { path = "../shared-rules" }
```

`sync` re-copies the directory on every run, so edits show up immediately. Restore the version constraint and run `agentpack lock` when you're done.

## Pin an exact commit

When you need a commit that isn't behind a tag, use the `commit` field directly — no branch tricks required:

```toml
[dependencies]
"github.com/acme/shared-rules" = { commit = "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2" }
```

`tag` and `branch` are the other exact-pin options; see [Your First Manifest](../getting-started/first-manifest.md).

## Project overlay and per-mode toggles

For project-specific content that isn't a published package, use the committed `./.agents/` overlay ([Team Workflows](./team-workflows.md)). To turn whole packages, individual files, or MCP servers on and off per task, use [Modes](../concepts/modes.md). Both are first-class — reach for them before environment hacks.

## Relocate state and staging

```sh
export AGENTPACK_STAGING_ROOT="/tmp/agentpack-staging"   # stable staging path
export AGENTPACK_HOME="./.agentpack"                     # project-isolated cache/state
```

A project-local `AGENTPACK_HOME` keeps all cached content and bookkeeping inside the repo directory; add `.agentpack/` to `.gitignore`. See [Environment Variables](../reference/env-vars.md) for defaults.

## Attribution

By default, `sync` forces AI attribution **off** in every staged harness config — Co-Authored-By trailers and "Generated with X" footers — so your project doesn't pick up agent credit lines unintentionally. Your real `~/.claude`, `~/.codex`, `~/.cursor`, and `~/.config/opencode` are never modified; only the staged copies and the Claude overlay file at `$AGENTPACK_HOME/claude-settings.json` are touched.

Set `AGENTPACK_KEEP_ATTRIBUTION=1` (or `true`/`yes`) to keep your existing values.

How each harness is handled:

| Harness | Mechanism |
|---|---|
| Claude Code | `$AGENTPACK_HOME/claude-settings.json` via `--settings`: `includeCoAuthoredBy = false`, empty commit/PR attribution (loads at `flagSettings` scope) |
| Codex | `commit_attribution = ""` in the staged `config.toml` |
| Cursor | `attributeCommitsToAgent = false`, `attributePRsToAgent = false` in the staged `cli-config.json` (a real file, never your real one) |
| OpenCode | No first-class setting; a system-prompt file is added to `instructions[]` |
| Grok | No verified setting; prompt-level guidance only |
| Antigravity | No verified setting; an always-apply plugin rule |

Claude, Codex, and Cursor have real attribution settings, so those are exact. OpenCode, Grok, and Antigravity expose no such setting, so agentpack falls back to prompt-level guidance — best-effort, not guaranteed.
