# Claude Code Integration

agentpack supports [Claude Code](https://claude.ai/code) via the `agentpack claude` launcher.

## How it works

Claude Code supports loading additional plugin directories via the `--plugin-dir` flag. agentpack stages all declared dependencies into a harness-specific directory and passes that directory to Claude Code at launch.

The integration is **additive**: your existing `~/.claude/` configuration is untouched. Packaged commands and agents are layered on top.

## Launching

```sh
agentpack claude
```

This is equivalent to:

```sh
claude --plugin-dir "$AGENTPACK_STAGING_ROOT/claude"
```

Any extra arguments you pass are forwarded to `claude`:

```sh
agentpack claude --no-auto-update
```

## Staged layout

```
$AGENTPACK_STAGING_ROOT/claude/
  .claude/
    commands/
      my-command.md
      another-command.md
    agents/
      my-agent.md
```

Commands are markdown files following Claude Code's command format. Agents are markdown files following Claude Code's agent format.

## Artifact types

| Artifact | Staged as |
|---|---|
| Commands | `.claude/commands/<name>.md` |
| Agents / skills | `.claude/agents/<name>.md` |
| Rules | Prepended to the session system prompt |

## Full sync on launch

By default, agentpack stages from the local cache without making network calls. To force a full network sync before launching:

```sh
AGENTPACK_LAUNCH_FULL_SYNC=1 agentpack claude
```

## Environment variables

| Variable | Description |
|---|---|
| `AGENTPACK_LAUNCH_FULL_SYNC` | Set to `1` to sync from network before launching |
| `AGENTPACK_STAGING_ROOT` | Override the staging root directory |
| `AGENTPACK_HOME` | Override the cache/config root |

See [Environment Variables](../reference/env-vars.md) for details.
