# Introduction

**agentpack** is a package manager for AI coding agent configurations. It lets you declare the skills, rules, agents, and commands your project needs, resolve and lock exact versions, and launch supported AI harnesses with everything pre-staged.

## The Problem

AI coding agents are increasingly driven by configuration: system prompts, slash-command definitions, rules files, and agent skill libraries. These artifacts are scattered across GitHub repositories and local directories, have no standardized versioning story, and must be manually adapted when you switch between agents (Claude Code, Cursor, OpenCode, Codex). Teams copy-paste configs into their repos, configs drift, and there is no lockfile to guarantee reproducibility.

## What agentpack Provides

| Capability | Description |
|---|---|
| Declarative manifest | `agentpack.toml` lists dependencies with version constraints |
| Deterministic lockfile | `pack.lock` pins exact commits and content hashes |
| Content-addressed cache | Downloaded content is stored once under `$AGENTPACK_HOME/cache/` |
| Per-harness staging | Artifacts are materialized into harness-specific staging directories outside your repo |
| Cross-harness conversion | Skills, commands, agents, and rules are translated to each harness's native format |
| Launchers | `agentpack claude`, `agentpack agent`, `agentpack opencode`, `agentpack codex` |

## Quick Example

```toml
# agentpack.toml
[package]
name = "my-project"
version = "0.1.0"

[dependencies]
"github.com/OlegHQ/paperclip-skills" = "^0.3"
"github.com/acme/shared-rules" = { version = "1.0", path = "rules" }
```

```
$ agentpack add github.com/OlegHQ/paperclip-skills
$ agentpack lock
$ agentpack claude
```

Your agent launches with every declared dependency staged and ready — no manual copying, no version drift.

## Source and License

agentpack is open source at [https://github.com/OlegHQ/agentpack](https://github.com/OlegHQ/agentpack).
