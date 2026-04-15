# Codex Integration

agentpack supports [OpenAI Codex CLI](https://github.com/openai/codex) via the `agentpack codex` launcher.

## How it works

Codex reads its configuration and agent instructions from the directory pointed to by `CODEX_HOME`. agentpack stages packaged content into a Codex-compatible directory structure and sets `CODEX_HOME` before exec-ing the `codex` binary.

Codex caches login state either in `auth.json` or in the OS credential store. Because the keyring slot is derived from the canonical `CODEX_HOME` path, agentpack does not copy credentials into each per-project staged home. Instead, staged `auth.json` is linked to a shared source:

- `~/.codex/auth.json` when the user already stores credentials in a file
- `$AGENTPACK_HOME/shared/codex/auth.json` when the user stores credentials in the OS keychain

When the shared file does not exist yet, agentpack materializes it from the real `~/.codex` keychain entry. The staged `config.toml` is then forced to `cli_auth_credentials_store = "file"` so every project sees the same refreshed token state.

## Launching

```sh
agentpack codex
```

Extra arguments are forwarded to `codex`:

```sh
agentpack codex --model o4-mini
```

## CODEX_HOME override

When launched, agentpack sets:

```sh
CODEX_HOME="$AGENTPACK_STAGING_ROOT/codex-home"
```

## Staged layout

```
$AGENTPACK_STAGING_ROOT/codex-home/
  auth.json -> ~/.codex/auth.json | $AGENTPACK_HOME/shared/codex/auth.json
  config.toml
  skills/
    <name>/
      SKILL.md
```

## Artifact types

| Artifact | Staged as |
|---|---|
| Rules | `instructions/<name>.md` |
| Agents / skills | `agents/<name>.md` |
| Commands | Converted to agent instructions |

## Environment variables

| Variable | Description |
|---|---|
| `CODEX_HOME` | Set automatically by the launcher; override to customize |
| `AGENTPACK_HOME` | Root for agentpack cache/state, including shared Codex auth when keychain bridging is needed |
| `AGENTPACK_STAGING_ROOT` | Override the staging root |
| `AGENTPACK_LAUNCH_FULL_SYNC` | Set to `1` to sync before launch |

## Checking staged content

```sh
agentpack sync
ls "$AGENTPACK_STAGING_ROOT/codex-home/"
```
