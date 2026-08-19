# Codex

`agentpack codex` launches the [OpenAI Codex CLI](https://github.com/openai/codex) with a redirected home. Codex reads its config and skills from `CODEX_HOME` (default `~/.codex`), so agentpack stages a complete home and points the variable at it.

## What the launcher does

```sh
CODEX_HOME="<staging>/modes/<mode>/codex-home" codex
```

Extra arguments are forwarded:

```sh
agentpack codex --model gpt-5-codex
agentpack --yolo codex      # adds --dangerously-bypass-approvals-and-sandbox
```

The staged home is **seeded** from your real `~/.codex/` (`config.toml`, `skills`, `themes`) so user config keeps working under the redirect.

## Credential bridging

Codex stores OAuth/API material in `auth.json` or in the OS keychain, keyed by the canonical `CODEX_HOME` path — so a staged path would otherwise miss your keychain entry and force re-login. agentpack avoids copying credentials per project by linking each staged `auth.json` to a shared source:

- your real `~/.codex/auth.json` when that file already exists, or
- `$AGENTPACK_HOME/shared/codex/auth.json`, which agentpack materializes from the real `~/.codex` keychain entry (service `Codex Auth`) when credentials live in the keychain.

The staged `config.toml` is forced to `cli_auth_credentials_store = "file"`, so every project shares refresh-token updates through that one file.

## Session history

The staged home is disposable, but Codex resume state is not. agentpack links staged `sessions/`, `archived_sessions/`, and `history.jsonl` to their native locations under `~/.codex/`, and defaults `sqlite_home` to `~/.codex`. Sessions created through agentpack therefore appear in direct `codex resume` and survive staging cleanup, project/mode changes, and machine restarts.

On the first sync after upgrading, agentpack imports surviving history from every old staging mode before rebuilding it. Existing native files always win; differing collisions are retained under `$AGENTPACK_HOME/recovery/session-history/codex/` for manual inspection. An explicitly configured `sqlite_home` is preserved.

## Staged layout

```text
codex-home/
  auth.json -> ~/.codex/auth.json | $AGENTPACK_HOME/shared/codex/auth.json
  sessions/ -> ~/.codex/sessions/
  archived_sessions/ -> ~/.codex/archived_sessions/
  history.jsonl -> ~/.codex/history.jsonl
  config.toml          # seeded + attribution off + merged [mcp_servers]
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

Attribution is forced off via `commit_attribution = ""` in the staged `config.toml`. Set `AGENTPACK_KEEP_ATTRIBUTION=1` to keep your value.

## Environment

| Variable | Effect |
|---|---|
| `CODEX_PATH` | Path to the `codex` binary |
| `AGENTPACK_HOME` | Cache/state root; also holds the shared Codex auth file when keychain bridging is needed |
| `AGENTPACK_STAGING_ROOT` | Override the staging root |

See [Environment Variables](../reference/env-vars.md) for the complete list.
