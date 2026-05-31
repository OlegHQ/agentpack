# Manifest (agentpack.toml)

`agentpack.toml` is the human-edited source of truth for a project. It declares direct dependencies, project-local [modes](./modes.md), and [MCP servers](./mcp.md) — and nothing else. Transitive dependencies, resolved commits, and cache keys all live in the generated [`pack.lock`](./lockfile.md).

## Project identity

Two top-level keys, written before any table:

```toml
name    = "my-project"   # used in lock metadata and diagnostics
version = "0.0.1"        # SemVer string
```

There is no `[package]` wrapper. `init` fills both from the directory name and a default version; you can edit them freely.

## `[dependencies]`

Each key is a **module ID**; each value is either a short string or a table.

```toml
[dependencies]
# Short form: a version constraint, branch, tag, or commit as a bare string
"github.com/acme/lint-rules" = "^1.2"

# Table form: anything richer
"github.com/acme/shared-rules" = { tag = "v2.1.0" }
"local-tools"                  = { path = "../local-tools" }
```

### Table fields

| Field | Type | Description |
|---|---|---|
| `version` | string | SemVer constraint matched against the repo's tags |
| `branch` | string | Track a branch; resolves to its HEAD at lock time (not reproducible over time) |
| `tag` | string | Pin an exact tag |
| `commit` | string | Pin an exact commit SHA |
| `path` | string | **Local filesystem** directory, relative to the project root — for local development, *not* for selecting a repo subdirectory |

## Module ID format

Module IDs are lowercase Go-style paths. The in-repo location of the package is encoded directly in the ID:

```text
github.com/<owner>/<repo>                 # repository root
github.com/<owner>/<repo>/<p1>            # the <p1> subdirectory
github.com/<owner>/<repo>/<p1>/<p2>/...   # nested subdirectory
```

The host is always `github.com` for remote packages. To depend on a subdirectory of a monorepo, put the subpath in the key:

```toml
"github.com/acme/monorepo/packages/agent-rules" = "^1.0"
```

A human-typed spec may carry an optional `@ref` suffix (`...@v1.2`, `...@main`), but identity and `cache_key` always come from the resolved commit, never the ref name.

### Remote vs. local

When a dependency has a `path` field, it is resolved from your filesystem and never fetched from GitHub. The module ID is still used as the package's identity for deduplication across the dependency graph.

## Commit it

Always commit `agentpack.toml` together with `pack.lock`. The pair gives every contributor and every CI run identical resolution. Editing the manifest by hand is fine — run `agentpack lock` afterward to reconcile the lockfile.
