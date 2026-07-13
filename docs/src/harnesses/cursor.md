# Cursor Agent

`agentpack agent` launches the [Cursor](https://www.cursor.com/) CLI (`cursor-agent`) with your packages staged. The subcommand has the alias `cursor-agent`.

> The binary is `cursor-agent`, not `agent` — the bare name `agent` collides with other tools (Grok ships one). Override the path with `CURSOR_AGENT_PATH`.

## What the launcher does

Cursor resolves user-scoped skills, commands, and agents from a `HOME`-backed `.cursor` tree. So agentpack runs `cursor-agent` with a **synthetic `HOME`** at `<staging>/modes/<mode>/cursor-home`, whose `.cursor/` symlinks the staged pack assets alongside your real Cursor login and session paths.

It also sets a few environment variables on the child process:

- `CURSOR_CONFIG_DIR` → the fake home's `.cursor`.
- `CURSOR_DATA_DIR` → your **real** `~/.cursor` when unset, so workspace-trust state survives staging rebuilds.
- `CARGO_HOME`, `RUSTUP_HOME`, `DOCKER_CONFIG` → bridged from your real home (unless you already set them) so shell tooling keeps working inside the staged `HOME`.

On Linux, agentpack also links both Cursor's uppercase Electron profile (`$XDG_CONFIG_HOME/Cursor`) and the CLI's lowercase authentication profile (`$XDG_CONFIG_HOME/cursor`) into the synthetic home. This keeps `auth.json` durable across projects and staging rebuilds.

agentpack does not install pack content into your real `~/.cursor`. It may create the durable Cursor profile directories and staged symlinks needed for first-time login/session files to persist; Cursor manages the actual auth, workspace trust, and MCP approval contents.

## Workspace and the agents symlink

`--workspace` defaults to the **current working directory** where you invoked agentpack — not the pack root. In a monorepo where `agentpack.toml` lives in a parent directory, that distinction matters; pass `--workspace <path>` to override.

Cursor reads sub-agents only from `resolve(--workspace)/.cursor/agents`. So, **only** `agentpack agent` creates `./.cursor/agents` as a symlink into the staged pack `agents/` (when the pack ships agent markdown) and records it in a per-project overlay manifest for safe cleanup. Bare `sync` and the other launchers leave the workspace alone and remove any stale symlink from a previous `agent` run. If `./.cursor/agents` already exists as a real directory or file, agentpack leaves it and logs a warning. Add `.cursor/agents` to `.gitignore` if you don't want the symlink tracked.

```sh
agentpack agent
agentpack agent --workspace ./packages/api
agentpack --yolo agent              # adds --force
```

Cursor's `cursor-agent` only accepts `--trust` in `--print`/headless mode; agentpack prepends it automatically there.

## Staged layout

```text
modes/<mode>/
  cursor/                          # Cursor plugins layout
    .cursor-plugin/marketplace.json
    agentpack-bundle/
      .cursor-plugin/plugin.json
      commands/ agents/ skills/ rules/ hooks/ mcp.json
  cursor-home/                     # fake HOME
    .cursor/   # symlinks to pack dirs + your real cli-config/session files
    .config/cursor -> real Cursor Agent CLI profile (Linux auth)
```

## Artifact handling

| Artifact | Staged as |
|---|---|
| Rules | `.mdc` files, Cursor's native format (preserved) |
| Commands | Plain markdown |
| Agents / skills | Cursor agent/skill markdown |
| MCP | JSON `mcpServers`, merged with your real `~/.cursor/mcp.json` (your entries win) |

Attribution is forced off via a **real** `cli-config.json` file in the staged tree (not a symlink), so agentpack's writes never bleed back into your real Cursor profile.

## Environment

| Variable | Effect |
|---|---|
| `CURSOR_AGENT_PATH` | Path to the `cursor-agent` binary |
| `CURSOR_DATA_DIR` | Workspace-trust store; agentpack points it at real `~/.cursor` when unset |
| `AGENTPACK_STAGING_ROOT` | Override the staging root (use this for a stable path when your OS rotates temp dirs) |

See [Environment Variables](../reference/env-vars.md) for the complete list.
