# Your First Manifest

`agentpack.toml` is the manifest file that lives at the root of your project. It declares who you are and what packages you depend on.

## Minimal manifest

```toml
[package]
name = "my-project"
version = "0.1.0"
```

## Adding dependencies

Dependencies are declared in the `[dependencies]` table. Module IDs follow Go-style path conventions:

```toml
[dependencies]
# Latest compatible with ^0.3
"github.com/OlegHQ/paperclip-skills" = "^0.3"

# Exact version
"github.com/acme/lint-rules" = "=1.2.0"

# A specific subdirectory of a repo
"github.com/acme/monorepo/agents/python" = "^2.0"
```

## Inline table form

When you need additional options — such as pinning to a branch or pointing at a local path — use the inline table form:

```toml
[dependencies]
# Pin to a branch (not recommended for production; use version tags)
"github.com/acme/experimental" = { branch = "main" }

# Local path dependency (useful during development)
"github.com/acme/my-rules" = { path = "../my-rules" }

# Specific subdirectory within a repo
"github.com/acme/monorepo" = { version = "^1.0", path = "packages/agent-rules" }
```

## Version constraint syntax

agentpack uses semantic versioning with a subset of the standard constraint operators:

| Constraint | Meaning |
|---|---|
| `"1.2.3"` | Exactly 1.2.3 |
| `"^1.2.3"` | >=1.2.3, <2.0.0 (compatible) |
| `"~1.2.3"` | >=1.2.3, <1.3.0 (patch updates) |
| `">=1.0, <2.0"` | Explicit range |
| `"*"` | Any version |

## Full example

```toml
[package]
name = "acme-backend"
version = "0.2.1"

[dependencies]
"github.com/OlegHQ/paperclip-skills" = "^0.3"
"github.com/acme/shared-agent-rules"  = "~1.4"
"github.com/acme/monorepo"            = { version = "^2.0", path = "packages/coding-agent" }
```

After editing the manifest, run `agentpack lock` to regenerate `pack.lock`, then `agentpack sync` to pull the new content.

See the [Manifest Schema reference](../reference/manifest-schema.md) for the complete field listing.
