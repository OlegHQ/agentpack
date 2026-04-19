# Manifest Schema

Full reference for `agentpack.toml`.

## Top-level tables

```toml
[package]       # required
[dependencies]  # optional
[modes]         # optional
[mcp.servers]   # optional
```

---

## `[package]`

```toml
[package]
name    = "my-project"
version = "0.1.0"
```

| Field | Type | Required | Description |
|---|---|---|---|
| `name` | string | yes | Project name. Used in diagnostics and lock metadata. Must be non-empty. |
| `version` | string | yes | SemVer string (`MAJOR.MINOR.PATCH`). |

---

## `[dependencies]`

Each entry has a **key** (module ID) and a **value** (version constraint string or inline table).

### Key format

```
github.com/<owner>/<repo>
github.com/<owner>/<repo>/<subpath>
```

The key must be a valid module ID. Only `github.com` is supported as the host for remote packages.

### Value: version string

```toml
"github.com/acme/rules" = "^1.2"
```

Shorthand for `{ version = "^1.2" }`.

### Value: inline table

```toml
"github.com/acme/rules" = { version = "^1.2", path = "subdir" }
```

| Field | Type | Description |
|---|---|---|
| `version` | string | SemVer constraint. Required unless `branch` is set. |
| `path` | string | Subdirectory within the repository to treat as the package root. |
| `branch` | string | Git branch to track instead of a version tag. Non-reproducible; avoid in production. |

### Local path dependency

When `path` is a filesystem path (absolute or relative to the manifest), the dependency is resolved locally:

```toml
"github.com/acme/my-lib" = { path = "../my-lib" }
```

No `version` or `branch` field is needed for local path dependencies.

---

## Version constraint syntax

| Syntax | Semantics |
|---|---|
| `"1.2.3"` | Exact version 1.2.3 |
| `"^1.2.3"` | >=1.2.3 and <2.0.0 |
| `"~1.2.3"` | >=1.2.3 and <1.3.0 |
| `">=1.0.0"` | Any version at or above 1.0.0 |
| `">=1.0, <2.0"` | Explicit range |
| `"*"` | Any version |

---

## Complete example

```toml
[package]
name    = "acme-backend"
version = "0.5.0"

[dependencies]
"github.com/OlegHQ/paperclip-skills"    = "^0.3"
"github.com/acme/shared-rules"          = "~1.4"
"github.com/acme/monorepo"              = { version = "^2.0", path = "packages/agent" }
"github.com/acme/experimental"          = { branch = "main" }
"github.com/acme/local-dev"             = { path = "../local-dev" }

[modes.default]
base = "all"
disable = ["package-path:github.com/acme/shared-rules:commands/noisy.md"]

[modes.review]
base = "none"
enable = ["package:github.com/acme/shared-rules", ".agents:rules/backend.mdc"]
```

## `[modes.<name>]`

Modes are project-local staging presets. The reserved `default` mode is used whenever `--mode` is omitted.

```toml
[modes.default]
base = "all"
disable = ["mcp:filesystem"]

[modes.design]
base = "none"
enable = ["package:github.com/acme/shared-rules"]
```

| Field | Type | Description |
|---|---|---|
| `base` | `"all"` or `"none"` | Baseline capability state before selectors are applied |
| `enable` | string array | Selectors to turn on |
| `disable` | string array | Selectors to turn off |

Supported selectors:

- `package:<module>`
- `package-path:<module>:<relative-path>`
- `mcp:<name>`
- `.agents:<relative-path>`
