# Codex Integration

agentpack supports [OpenAI Codex CLI](https://github.com/openai/codex) via the `agentpack codex` launcher.

## How it works

Codex reads its configuration and agent instructions from the directory pointed to by `CODEX_HOME`. agentpack stages packaged content into a Codex-compatible directory structure and sets `CODEX_HOME` before exec-ing the `codex` binary.

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
CODEX_HOME="$AGENTPACK_STAGING_ROOT/codex"
```

## Staged layout

```
$AGENTPACK_STAGING_ROOT/codex/
  instructions/
    <name>.md
  agents/
    <name>.md
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
| `AGENTPACK_STAGING_ROOT` | Override the staging root |
| `AGENTPACK_LAUNCH_FULL_SYNC` | Set to `1` to sync before launch |

## Checking staged content

```sh
agentpack sync
ls "$AGENTPACK_STAGING_ROOT/codex/"
```
