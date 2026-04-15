# Manifest (agentpack.toml)

The manifest is a TOML file named `agentpack.toml` that lives at the root of your project. It is the single source of truth for your project's identity and its declared dependencies.

## `[package]` table

```toml
[package]
name    = "my-project"   # required; used in lock metadata and error messages
version = "0.1.0"        # required; SemVer string
```

| Field | Type | Required | Description |
|---|---|---|---|
| `name` | string | yes | Human-readable project name |
| `version` | string | yes | SemVer version of this project |

## `[dependencies]` table

Each key is a **module ID** — a slash-delimited path that uniquely identifies a package and an optional subdirectory within a repository.

### Short form (version string only)

```toml
[dependencies]
"github.com/OlegHQ/paperclip-skills" = "^0.3"
```

### Long form (inline table)

```toml
[dependencies]
"github.com/acme/repo" = { version = "^1.0", path = "subdir", branch = "main" }
```

| Field | Type | Description |
|---|---|---|
| `version` | string | SemVer constraint. Omit if using `branch` |
| `path` | string | Subdirectory within the repository to use as the package root |
| `branch` | string | Git branch to track. Not recommended for reproducible builds |

## Module ID format

Module IDs mirror Go module paths:

```
github.com/<owner>/<repo>
github.com/<owner>/<repo>/<subdir>
github.com/<owner>/<repo>/<nested>/<subdir>
```

The host is always `github.com` for remote packages. Local path dependencies use a path string relative to the manifest:

```toml
"github.com/acme/my-lib" = { path = "../my-lib" }
```

When `path` is an absolute or relative filesystem path the dependency is resolved locally and not fetched from GitHub. The module ID is still used for dependency deduplication across the graph.

## Commit the manifest

`agentpack.toml` should always be committed to version control alongside `pack.lock`. Together they give every contributor and CI run identical dependency resolution.
