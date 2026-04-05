# Environment Variables

agentpack's behavior can be controlled through the following environment variables. Set them in your shell profile (`.bashrc`, `.zshrc`, `config.fish`, etc.) or in CI environment configuration.

---

## `AGENTPACK_HOME`

**Default:** `~/.agentpack`

Root directory for all agentpack state: the content-addressed cache, registry metadata, and derived staging directories (when `AGENTPACK_STAGING_ROOT` is not set).

```sh
export AGENTPACK_HOME="$HOME/.agentpack"
```

Change this to use a shared cache on a network mount, a non-home-directory location, or a per-project isolated store.

---

## `AGENTPACK_STAGING_ROOT`

**Default:** `$AGENTPACK_HOME/staging/<project-hash>`

Root directory where harness-specific staging subdirectories are created. agentpack creates one subdirectory per supported harness (`claude/`, `cursor/`, `opencode/`, `codex/`) under this root.

```sh
export AGENTPACK_STAGING_ROOT="/tmp/agentpack-staging"
```

Useful in CI to ensure staging is on a fast local disk.

---

## `AGENTPACK_LAUNCH_FULL_SYNC`

**Default:** unset (equivalent to `0`)

When set to `1`, each launcher command (`claude`, `agent`, `opencode`, `codex`) runs a full network sync before exec-ing the underlying binary. This ensures the latest locked content is present but adds latency on every launch.

```sh
export AGENTPACK_LAUNCH_FULL_SYNC=1
agentpack claude
```

When unset or `0`, launchers stage from the local cache only (fast, offline-capable).

---

## `AGENTPACK_DOT_AGENTS`

**Default:** unset

Path to a directory of local agent/skill files to always include in every harness staging, regardless of `agentpack.toml`. Useful for personal overlays that should not be committed.

```sh
export AGENTPACK_DOT_AGENTS="$HOME/.my-agents"
```

---

## `AGENTPACK_CURSOR_CONFIG_DIR`

**Default:** derived from synthetic HOME

Overrides the Cursor configuration directory used by the `agentpack agent` launcher. When set, agentpack stages packaged Cursor artifacts here instead of under the synthetic HOME.

```sh
export AGENTPACK_CURSOR_CONFIG_DIR="$HOME/.cursor"
```

---

## Summary table

| Variable | Default | Description |
|---|---|---|
| `AGENTPACK_HOME` | `~/.agentpack` | Cache and state root |
| `AGENTPACK_STAGING_ROOT` | `$AGENTPACK_HOME/staging/<hash>` | Staging directory root |
| `AGENTPACK_LAUNCH_FULL_SYNC` | `0` | Sync from network on every launch |
| `AGENTPACK_DOT_AGENTS` | unset | Always-included local agent overlay directory |
| `AGENTPACK_CURSOR_CONFIG_DIR` | derived | Override Cursor config directory |
