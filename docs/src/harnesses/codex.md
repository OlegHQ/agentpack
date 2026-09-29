# Codex

`agentpack codex` launches the [OpenAI Codex CLI](https://github.com/openai/codex) with a redirected home. Codex reads its config and skills from `CODEX_HOME` (default `~/.codex`), so agentpack stages a complete home and points the variable at it.

## What the launcher does

```sh
CODEX_HOME="<generation-home>" codex
```

Extra arguments are forwarded:

```sh
agentpack codex --model gpt-5-codex
agentpack --yolo codex      # adds --dangerously-bypass-approvals-and-sandbox
```

Each project mode gets a complete generation home. Agentpack builds and verifies a replacement before switching new launches to it. Running sessions keep their original home and daemon. The home is **seeded** from your real `~/.codex/` (`config.toml`, `skills`, `themes`) so user config keeps working under the redirect.

## Credential bridging

Codex stores OAuth/API material in `auth.json` or in the OS keychain, keyed by the canonical `CODEX_HOME` path — so a staged path would otherwise miss your keychain entry and force re-login. agentpack avoids copying credentials per project by linking each staged `auth.json` to a shared source:

- your real `~/.codex/auth.json` when that file already exists, or
- `$AGENTPACK_HOME/shared/codex/auth.json`, which agentpack materializes from the real `~/.codex` keychain entry (service `Codex Auth`) when credentials live in the keychain.

The staged `config.toml` is forced to `cli_auth_credentials_store = "file"`, so every project shares refresh-token updates through that one file.

MCP server OAuth is separate from the main Codex login. agentpack stores those tokens per project under `$AGENTPACK_HOME/projects/<hash>/codex-mcp-oauth/.credentials.json`, links every mode's staged home to that file, and forces `mcp_oauth_credentials_store = "file"`. The accompanying `mcp-oauth-locks/` directory is shared too, so concurrent modes serialize token refreshes correctly. MCP login therefore survives `add`, `sync`, staging rebuilds, and temp-directory cleanup without leaking credentials into another project.

After upgrading to this layout, an MCP credential that existed only in the OS keyring or Codex's encrypted secrets backend requires one final login. Legacy staged `.credentials.json` files are recovered automatically.

## Session history

The generated home is replaceable, but Codex resume state is not. agentpack links staged `sessions/`, `archived_sessions/`, and `history.jsonl` to their native locations under `~/.codex/`, and defaults `sqlite_home` to `~/.codex`. Sessions created through agentpack therefore appear in direct `codex resume` and survive staging cleanup, project/mode changes, and machine restarts.

On the first sync after upgrading, agentpack imports surviving history from every old staging mode before rebuilding it. Existing native files always win; differing collisions are retained under `$AGENTPACK_HOME/recovery/session-history/codex/` for manual inspection. An explicitly configured `sqlite_home` is preserved.

## Staged layout

```text
codex-home/
  auth.json -> ~/.codex/auth.json | $AGENTPACK_HOME/shared/codex/auth.json
  .credentials.json -> $AGENTPACK_HOME/projects/<hash>/codex-mcp-oauth/.credentials.json
  mcp-oauth-locks/ -> $AGENTPACK_HOME/projects/<hash>/codex-mcp-oauth/mcp-oauth-locks/
  sessions/ -> ~/.codex/sessions/
  archived_sessions/ -> ~/.codex/archived_sessions/
  history.jsonl -> ~/.codex/history.jsonl
  config.toml          # seeded + stripped legacy attribution + merged [mcp_servers]
  skills/
    <name>/SKILL.md
```

## Artifact handling

Codex gets the **portable skill subset** of pack content. agentpack does not synthesize Codex plugin marketplaces from Claude plugins.

| Artifact | Staged as |
|---|---|
| Skills | Codex skill under `skills/<name>/` |
| Commands | Skill fallback |
| Agents | Skill fallback |
| Rules | Skill fallback |
| MCP | Merged into `[mcp_servers]` in `config.toml` |

Legacy `commit_attribution` is stripped from the generated `config.toml` (removed in modern Codex versions). Agentpack no longer injects `--no-daemon` or disables daemon auto-start. A Codex daemon is tied to one canonical `CODEX_HOME` and process environment, so each generation has its own daemon. When a generation is replaced, agentpack retains it while a wrapped Codex process is running or its daemon has loaded threads. Once unused, agentpack stops the retired daemon and removes its home during a later sync or launch. Explicit `--remote` connections remain user-controlled. Set `AGENTPACK_KEEP_ATTRIBUTION=1` to preserve any existing `commit_attribution`.

Codex caches login state inside each daemon. A login or logout made in one generation updates the shared credential file, while an already running daemon in another generation may keep its prior account view until that session ends. New generations read the current credential file.

## Environment

| Variable | Effect |
|---|---|
| `CODEX_PATH` | Path to the `codex` binary |
| `AGENTPACK_HOME` | Cache/state root; also holds shared Codex login state and project-scoped MCP OAuth state |
| `AGENTPACK_STAGING_ROOT` | Override the staging root |

See [Environment Variables](../reference/env-vars.md) for the complete list.
