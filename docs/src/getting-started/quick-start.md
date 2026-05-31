# Quick Start

Zero to a running agent in a few minutes. The short version: `init`, `add`, launch.

## 1. Initialize a manifest

From your project directory:

```sh
agentpack init
```

This writes a minimal `agentpack.toml` (identity is two top-level keys, not a `[package]` table) and an empty v2 `pack.lock`:

```toml
name = "my-project"
version = "0.0.1"

[dependencies]
```

`init` fails if `agentpack.toml` already exists, so it never clobbers your manifest.

## 2. Add a dependency

`add` takes a package spec. The most explicit form is a Go-style module ID, but `owner/repo/path` shorthand and GitHub tree/blob URLs work too:

```sh
agentpack add anthropics/skills/skills/canvas-design
```

agentpack resolves the spec, appends it under `[dependencies]`, refreshes `pack.lock`, and runs a sync — all in one command:

```toml
[dependencies]
"github.com/anthropics/skills/skills/canvas-design" = {}
```

To pin a constraint, attach a `@ref` to the spec or edit the entry into table form (`{ version = "^1.0.0" }`, `{ branch = "main" }`, `{ tag = "v2.1.0" }`). See [Your First Manifest](./first-manifest.md). To stage nothing yet, pass `--no-sync`.

## 3. Lock and sync explicitly (optional)

`add` already locked and synced. You only run these by hand for CI, or after editing the manifest directly:

```sh
agentpack lock    # re-resolve agentpack.toml → pack.lock (network)
agentpack sync    # fetch into the cache and rebuild every harness's staging
```

Commit both `agentpack.toml` and `pack.lock`. The first sync downloads everything; later syncs reuse the content-addressed cache and are fast.

## 4. Launch an agent

Each launcher syncs (fast path when nothing changed), stages for that harness, and execs the real binary:

```sh
agentpack claude     # Claude Code
agentpack agent      # Cursor Agent (alias: cursor-agent)
agentpack opencode   # OpenCode
agentpack codex      # Codex
agentpack grok       # Grok
agentpack agy        # Antigravity
```

Anything after the subcommand is forwarded to the underlying binary:

```sh
agentpack codex --model gpt-5-codex
```

## Next steps

- [Your First Manifest](./first-manifest.md) — version constraints, branches, local paths
- [Dependency Resolution](../concepts/resolution.md) — how a constraint becomes a pinned commit
- [Modes](../concepts/modes.md) — enable or disable subsets of your packages per task
- [Harness guides](../harnesses/claude.md) — what each launcher actually does
