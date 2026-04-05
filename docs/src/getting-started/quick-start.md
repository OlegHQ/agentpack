# Quick Start

This guide walks you from zero to a running agent in under five minutes.

## 1. Initialize a manifest

Inside your project directory, run:

```sh
agentpack init
```

This creates `agentpack.toml` with a minimal `[package]` section:

```toml
[package]
name = "my-project"
version = "0.1.0"
```

## 2. Add a dependency

Add a package using its module ID (Go-style path):

```sh
agentpack add github.com/OlegHQ/paperclip-skills
```

agentpack resolves the latest compatible version and writes it into `[dependencies]` in `agentpack.toml`:

```toml
[dependencies]
"github.com/OlegHQ/paperclip-skills" = "^0.3.0"
```

## 3. Lock dependencies

Generate (or update) the lockfile:

```sh
agentpack lock
```

This writes `pack.lock` with exact commit SHAs and content hashes for every dependency. Commit both `agentpack.toml` and `pack.lock` to version control.

## 4. Sync the cache

Pull all locked content into the local cache:

```sh
agentpack sync
```

The first run downloads everything. Subsequent runs are instant if the cache is already warm.

## 5. Launch your agent

```sh
# Claude Code
agentpack claude

# Cursor
agentpack agent

# OpenCode
agentpack opencode

# Codex
agentpack codex
```

Each launcher stages the dependencies for that harness, sets the appropriate environment variables, and execs the underlying agent binary.

## Next Steps

- Learn about the [manifest format](./first-manifest.md)
- Understand [dependency resolution](../concepts/resolution.md)
- Explore [harness-specific guides](../harnesses/claude.md)
