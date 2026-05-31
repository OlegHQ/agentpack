# Introduction

**agentpack** pins the skills, plugins, commands, and rules your project feeds to AI coding agents. You declare them once in `agentpack.toml`, lock them to exact commits in `pack.lock`, and launch any supported agent with everything already staged in its native format.

Think of it as `cargo` or `uv` for agent configuration: a manifest, a lockfile, a content-addressed cache, and reproducible builds — except the artifacts are skills and prompts instead of crates.

## The problem it solves

Agent configuration has become real configuration. A project's behavior under Claude Code, Cursor, or Codex depends on slash commands, rules files, sub-agent definitions, MCP servers, and skill libraries. Today those artifacts live scattered across GitHub repos and local folders, with no shared versioning story, and each agent expects a different file layout.

So teams copy-paste. The same skill gets pasted into `.claude/`, then adapted by hand for `.cursor/`, then forgotten when a third agent shows up. Versions drift between machines. Nothing records which commit of a shared rule set you actually ran. There is no lockfile.

agentpack replaces the copy-paste with a dependency graph.

## What it gives you

| Capability | What it means |
|---|---|
| Declarative manifest | `agentpack.toml` lists direct dependencies with version constraints, modes, and MCP servers |
| Deterministic lockfile | `pack.lock` (v2) pins every package — direct and transitive — to an exact commit and `cache_key` |
| Content-addressed cache | Each package is fetched once into `$AGENTPACK_HOME/cache/<cache_key>/` and shared across projects |
| Per-harness staging | Artifacts are materialized into per-harness staging trees **outside** your repo — never committed, never symlinked into your real `~/.claude` or `~/.cursor` |
| Cross-harness conversion | A skill, command, agent, or rule is re-rendered into each harness's native format, not blindly copied |
| Six launchers | `claude`, `agent` (Cursor), `opencode`, `codex`, `grok`, `agy` (Antigravity) — sync then exec the real binary |

## A first taste

```toml
# agentpack.toml
name = "my-project"
version = "0.1.0"

[dependencies]
"github.com/anthropics/skills/skills/canvas-design" = { branch = "main" }
"github.com/anthropics/claude-plugins-official/plugins/hookify" = { branch = "main" }
```

```sh
agentpack add anthropics/skills/skills/canvas-design   # resolve, lock, sync
agentpack claude                                       # launch Claude Code with both staged
```

The agent starts with every declared dependency in place — no manual copying, no drift, and nothing leaked into your global agent config.

## How the pieces fit

```text
agentpack.toml  ──>  pack.lock  ──>  cache  ──>  staged bundles  ──>  launch
  (you write)        (pinned)      (fetched)     (per-harness)       (agent runs)
```

Read on through [Core Concepts](./concepts/manifest.md) to understand each step, or jump to the [Quick Start](./getting-started/quick-start.md) to get something running first.

## Source and license

agentpack is MIT-licensed and developed at [github.com/OlegHQ/agentpack](https://github.com/OlegHQ/agentpack). It is pre-release: the CLI, lockfile shape, staging layout, and defaults may change without a migration path until a stable release is declared.
