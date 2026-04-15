# OpenCode Integration

agentpack supports [OpenCode](https://github.com/sst/opencode) via the `agentpack opencode` launcher.

## How it works

OpenCode reads its configuration from the directory pointed to by `OPENCODE_CONFIG_DIR`. agentpack sets this variable to a staging directory containing all packaged content before exec-ing the `opencode` binary.

## Launching

```sh
agentpack opencode
```

Extra arguments are forwarded to `opencode`:

```sh
agentpack opencode --model anthropic/claude-sonnet-4-5
```

## OPENCODE_CONFIG_DIR override

When launched, agentpack sets:

```sh
OPENCODE_CONFIG_DIR="$AGENTPACK_STAGING_ROOT/opencode"
```

agentpack merges packaged configuration with any existing OpenCode configuration at this path.

## Staged layout

```
$AGENTPACK_STAGING_ROOT/opencode/
  config.json         # merged configuration
  agents/
    <name>.md
  instructions/
    <name>.md
```

## Artifact types

| Artifact | Staged as |
|---|---|
| Agents / skills | `agents/<name>.md` |
| Rules | `instructions/<name>.md` |
| Commands | Converted to agent instructions |

## Environment variables

| Variable | Description |
|---|---|
| `OPENCODE_CONFIG_DIR` | Set automatically by the launcher; override to customize |
| `AGENTPACK_STAGING_ROOT` | Override the staging root |
| `AGENTPACK_LAUNCH_FULL_SYNC` | Set to `1` to sync before launch |

## Checking the staged config

Inspect what agentpack will pass to OpenCode before launching:

```sh
agentpack sync
ls "$AGENTPACK_STAGING_ROOT/opencode/"
```
