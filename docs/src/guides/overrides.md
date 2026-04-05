# Overrides and Customization

agentpack provides several mechanisms to override or extend default behavior without modifying shared packages.

## Local path overrides

Replace a remote dependency with a local copy during development. Edit `agentpack.toml`:

```toml
[dependencies]
# Temporarily override with local version
"github.com/acme/shared-rules" = { path = "../shared-rules" }
```

Run `agentpack sync` to stage from the local path. When development is done, restore the version constraint and run `agentpack lock`.

This is the recommended approach for developing a package and testing it in a consumer project simultaneously.

## Personal agent overlay (`AGENTPACK_DOT_AGENTS`)

Add personal agent files that are staged on your machine but never committed to the project repository:

```sh
# In your shell profile
export AGENTPACK_DOT_AGENTS="$HOME/.my-agents"
```

Place any artifact files in that directory. agentpack will include them in every harness staging alongside the project's declared dependencies. Files in the overlay do not need a manifest.

```
~/.my-agents/
  commands/
    my-personal-command.md
  rules/
    my-preferences.md
```

## Staging root override

Redirect all staging to a custom location (e.g. for testing or CI isolation):

```sh
export AGENTPACK_STAGING_ROOT="/tmp/agentpack-test-staging"
agentpack sync
```

## Harness-specific config dir overrides

Each launcher respects a dedicated override variable:

| Harness | Variable | Default behavior |
|---|---|---|
| Cursor | `AGENTPACK_CURSOR_CONFIG_DIR` | Synthetic HOME approach |
| OpenCode | `OPENCODE_CONFIG_DIR` (set by launcher) | `staging/opencode` |
| Codex | `CODEX_HOME` (set by launcher) | `staging/codex` |

Set these in your shell profile to point at a pre-existing config directory rather than the auto-generated staging location:

```sh
export AGENTPACK_CURSOR_CONFIG_DIR="$HOME/.cursor"
```

## Pinning a dependency to a specific commit

If you need an exact commit that does not correspond to a published tag, use `branch` with a commit SHA (not a real branch, but git accepts SHAs as "branches" in some contexts). For clean reproducibility, the recommended approach is to ask the package author to tag the desired commit.

## Disabling auto-sync on launch

By default, launchers use the cached state without making network calls. If you have set `AGENTPACK_LAUNCH_FULL_SYNC=1` globally and want to disable it for a single invocation:

```sh
AGENTPACK_LAUNCH_FULL_SYNC=0 agentpack claude
```

## Overriding `AGENTPACK_HOME` per project

Isolate a project's cache completely from other projects:

```sh
AGENTPACK_HOME="./.agentpack" agentpack sync
AGENTPACK_HOME="./.agentpack" agentpack claude
```

This keeps all state inside the project directory. Add `.agentpack/` to `.gitignore`.
