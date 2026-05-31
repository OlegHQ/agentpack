# OpenCode

`agentpack opencode` launches [OpenCode](https://github.com/sst/opencode) with a redirected config root. Unlike Claude's additive plugin dir, OpenCode is configured by pointing it at a different config directory entirely.

## What the launcher does

OpenCode searches its config root — `opencode.json` plus `agents/`, `commands/`, `modes/`, `plugins/`, `skills/` — like the standard `.opencode` / `~/.config/opencode` locations. agentpack stages a complete root and runs:

```sh
OPENCODE_CONFIG_DIR="<staging>/modes/<mode>/opencode" opencode
```

Extra arguments are forwarded:

```sh
agentpack opencode --model anthropic/claude-sonnet-4-6
agentpack --yolo opencode      # stages opencode.json with "permission": "allow"
```

## What the root contains

```text
opencode/
  opencode.json          # seeded config + merged MCP + attribution instruction
  agents/   commands/   skills/    # converted pack artifacts
  plugins/  modes/
```

The root is **seeded** from your real `~/.config/opencode/` (`opencode.json`, `agents`, `commands`, `modes`, `plugins`, `skills`) so provider and auth configuration keep working even though the config root is redirected. Converted pack content is then overlaid into OpenCode's native markdown locations.

## Artifact handling

| Artifact | Staged as |
|---|---|
| Commands | OpenCode command markdown |
| Agents | OpenCode agent markdown |
| Skills | OpenCode skill |
| Rules | Best-effort skill fallback |
| MCP | Merged into `opencode.json` |

## Attribution

OpenCode has no first-class attribution setting. agentpack writes an `agentpack-no-attribution.md` system-prompt file and adds it to `instructions[]` in the staged `opencode.json` — prompt-level guidance, the best available lever. Set `AGENTPACK_KEEP_ATTRIBUTION=1` to skip it.

## Inspecting the staged root

```sh
agentpack sync
ls "$AGENTPACK_STAGING_ROOT/modes/default/opencode/"   # if AGENTPACK_STAGING_ROOT is set
```

## Environment

| Variable | Effect |
|---|---|
| `OPENCODE_PATH` | Path to the `opencode` binary |
| `AGENTPACK_STAGING_ROOT` | Override the staging root |

See [Environment Variables](../reference/env-vars.md) for the complete list.
