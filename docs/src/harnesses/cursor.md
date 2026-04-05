# Cursor Agent Integration

agentpack supports [Cursor](https://www.cursor.com/) via the `agentpack agent` launcher.

## How it works

Cursor reads its agent configuration from directories under `HOME`. agentpack overrides the `HOME` environment variable to point at a staging directory that contains symlinks back to the real home directory, plus the packaged agent content layered on top.

This approach avoids modifying your actual `~/.cursor/` directory.

## Launching

```sh
agentpack agent
```

You can pass additional arguments to the underlying `cursor` binary:

```sh
agentpack agent --new-window /path/to/project
```

## HOME override

When launched, agentpack sets:

```sh
HOME="$AGENTPACK_STAGING_ROOT/cursor/home"
```

Inside this synthetic home:

```
cursor/home/
  .cursor/
    rules/
      packaged-rule.mdc
      another-rule.mdc
    # symlinks to other real ~/.cursor/ content
  # symlinks to the rest of the real $HOME
```

The symlink approach means Cursor sees your real settings, extensions, and keybindings while also seeing the packaged agent rules.

## Cursor config dir override

You can also override the Cursor config directory directly without the HOME trick by setting:

```sh
export AGENTPACK_CURSOR_CONFIG_DIR=/path/to/cursor-config
```

When set, agentpack uses this path as the Cursor config root instead of deriving it from the synthetic HOME.

## Staged layout

```
$AGENTPACK_STAGING_ROOT/cursor/
  home/
    .cursor/
      rules/
        <packaged-rule>.mdc
```

Rules are `.mdc` files in Cursor's native format.

## Artifact types

| Artifact | Staged as |
|---|---|
| Rules | `.cursor/rules/<name>.mdc` |
| Commands | Converted to rules or agent instructions |
| Agents / skills | Converted to rules |

## Environment variables

| Variable | Description |
|---|---|
| `AGENTPACK_CURSOR_CONFIG_DIR` | Override the Cursor config directory |
| `AGENTPACK_STAGING_ROOT` | Override the staging root |
| `AGENTPACK_LAUNCH_FULL_SYNC` | Set to `1` to sync before launch |
