# Manifest Schema

Complete reference for `agentpack.toml`.

## Top-level shape

```toml
name    = "my-project"   # required
version = "0.0.1"        # required

[dependencies]   # optional
[modes]          # optional
[mcp.servers]    # optional
```

Identity is two **top-level keys**, not a `[package]` table.

| Key | Type | Required | Description |
|---|---|---|---|
| `name` | string | yes | Project name; used in diagnostics and lock metadata |
| `version` | string | yes | SemVer string |

## `[dependencies]`

Each entry is a **key** (module ID) mapped to a **value** (short string or inline table).

### Key format

```text
github.com/<owner>/<repo>
github.com/<owner>/<repo>/<p1>/<p2>/...
```

Module IDs are lowercase Go-style paths. The host is always `github.com` for remote packages. A subdirectory inside a repo is part of the **key** — there is no field for it.

### Value: short string

A bare string is a version constraint, branch, tag, or commit:

```toml
"github.com/acme/rules" = "^1.2"
```

### Value: inline table

```toml
"github.com/acme/rules" = { version = "^1.2" }
```

| Field | Type | Description |
|---|---|---|
| `version` | string | SemVer constraint matched against the repo's tags |
| `branch` | string | Track a branch; resolves to its HEAD at lock time (not reproducible over time) |
| `tag` | string | Pin an exact tag |
| `commit` | string | Pin an exact commit SHA |
| `path` | string | **Local filesystem** directory relative to the project root (local development) |

### Local path dependency

A `path` field makes the dependency local — read from disk, never fetched from GitHub. No `version`/`branch`/`tag` is needed:

```toml
"local-rules" = { path = "../local-rules" }
```

The key is still used as the package's identity for deduplication.

## Version constraint syntax

| Syntax | Semantics |
|---|---|
| `"1.2.3"` | Exactly 1.2.3 |
| `"^1.2.3"` | `>=1.2.3, <2.0.0` |
| `"~1.2.3"` | `>=1.2.3, <1.3.0` |
| `">=1.0.0"` | At or above 1.0.0 |
| `">=1.0, <2.0"` | Explicit range |
| `"*"` | Any version |

## `[modes.<name>]`

Project-local staging presets. The reserved `default` mode applies when `--mode` is omitted. See [Modes](../concepts/modes.md).

```toml
[modes.default]
base = "all"

[modes.writing]
base    = "none"
enable  = ["package:github.com/s1mplesonny/technical-writing-skill/technical-writing"]
disable = ["mcp:linear"]
```

| Field | Type | Description |
|---|---|---|
| `base` | `"all"` or `"none"` | Baseline before selectors are applied |
| `enable` | string array | Selectors to turn on |
| `disable` | string array | Selectors to turn off |

Selectors: `package:<module>`, `package-path:<module>:<relative-path>`, `mcp:<name>`, `.agents:<relative-path>`.

## `[mcp.servers.<name>]`

Project-level MCP server definitions, merged into each harness's native MCP config. See [MCP Servers](../concepts/mcp.md).

```toml
[mcp.servers.retrieval]
command = "uvx"
args    = ["mcp-retrieval"]
env     = { API_KEY = "sk-..." }
```

| Field | Type | Description |
|---|---|---|
| `command` | string | Executable that launches the server |
| `args` | string array | Command arguments |
| `env` | string map | Environment variables for the server process |
| `disabled` | bool | Optional; skip this server when `true` |

## Complete example

```toml
name    = "acme-backend"
version = "0.5.0"

[dependencies]
"github.com/anthropics/skills/skills/canvas-design" = { branch = "main" }
"github.com/acme/shared-rules"                      = { tag = "v2.1.0" }
"github.com/acme/monorepo/packages/agent"           = "^2.0"
"local-dev" = { path = "../local-dev" }

[modes.default]
base    = "all"
disable = ["package-path:github.com/acme/shared-rules:commands/noisy.md"]

[modes.review]
base   = "none"
enable = ["package:github.com/acme/shared-rules", ".agents:rules/backend.mdc"]

[mcp.servers.filesystem]
command = "npx"
args    = ["-y", "@modelcontextprotocol/server-filesystem"]
```
