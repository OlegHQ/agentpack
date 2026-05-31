# Your First Manifest

`agentpack.toml` lives at your project root and declares two things: the project's identity and its direct dependencies. It is the source of truth for *what* to install; `pack.lock` records the resolved result.

## Minimal manifest

Identity is two top-level keys. There is no `[package]` table.

```toml
name = "my-project"
version = "0.0.1"
```

## Module IDs encode the path inside a repo

A dependency key is a **module ID** — a Go-style path. The in-repo location of the package is part of the ID, not a separate field:

```toml
[dependencies]
# Repository root
"github.com/acme/lint-rules" = "^1.2"

# A subdirectory inside the repo (note the trailing path segments)
"github.com/acme/monorepo/agents/python" = "^2.0"
```

`github.com/acme/monorepo/agents/python` means *the `agents/python` directory of `github.com/acme/monorepo`*. This is the single most common point of confusion: to pull a subdirectory, put it in the key — do **not** reach for a `path` field (that field means something else; see below).

## Pinning: short string vs. table

The value can be a short string (a branch, tag, or version constraint) or a table for anything richer:

```toml
[dependencies]
# Semver constraint against the repo's tags
"github.com/acme/lint-rules" = "^1.2"

# Track a branch — resolves to its HEAD at lock time (not reproducible over time)
"github.com/acme/experimental" = { branch = "main" }

# Pin an exact tag
"github.com/acme/shared-rules" = { tag = "v2.1.0" }

# Pin an exact commit
"github.com/acme/shared-rules" = { commit = "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2" }
```

A table accepts `version`, `branch`, `tag`, `commit`, and `path`. Identity and `cache_key` always derive from the **resolved commit SHA**, never from a branch or tag name.

## Local path dependencies

The `path` field points at a **directory on your filesystem**, relative to the project root. This is for developing a package against a consumer, not for selecting a subdirectory of a GitHub repo:

```toml
[dependencies]
"mcp-retrieval" = { path = "../mcp-retrieval" }
```

On every `lock`/`sync`, path dependencies are re-copied from source and re-hashed, so local edits are picked up. Sync will error on a machine where the path is missing and the cache slot is empty.

## Version constraint syntax

Constraints are matched against the dependency's SemVer tags:

| Constraint | Meaning |
|---|---|
| `"1.2.3"` | Exactly 1.2.3 |
| `"^1.2.3"` | `>=1.2.3, <2.0.0` (compatible) |
| `"~1.2.3"` | `>=1.2.3, <1.3.0` (patch updates) |
| `">=1.0, <2.0"` | Explicit range |
| `"*"` | Any version |

## A fuller example

```toml
name = "acme-backend"
version = "0.2.1"

[dependencies]
"github.com/anthropics/skills/skills/canvas-design"      = { branch = "main" }
"github.com/anthropics/claude-plugins-official/plugins/hookify" = { branch = "main" }
"github.com/acme/monorepo/packages/coding-agent"         = "^2.0"
"local-rules" = { path = "../local-rules" }
```

After editing the manifest by hand, run `agentpack lock` to refresh `pack.lock`, then `agentpack sync` to fetch and stage. (`agentpack add` does both for you.)

See the [Manifest Schema](../reference/manifest-schema.md) for every field, plus the [`[modes]`](../concepts/modes.md) and [`[mcp.servers]`](../concepts/mcp.md) sections.
