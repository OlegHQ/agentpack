# Cursor Agent Integration

agentpack supports [Cursor](https://www.cursor.com/) via the `agentpack agent` launcher.

## How it works

Cursor discovery for skills, commands, and agents is tied to the HOME-backed `.cursor` tree. agentpack therefore launches Cursor with a synthetic `HOME` containing a staged `.cursor/` directory that overlays packaged agent content on top of your real Cursor config and session state.

To keep external tools working inside Cursor, agentpack also bridges common tool-specific homes back to the real profile: `CARGO_HOME`, `RUSTUP_HOME`, and `DOCKER_CONFIG`.

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
HOME="$AGENTPACK_STAGING_ROOT/cursor-home"
```

Inside that synthetic home:

```
$AGENTPACK_STAGING_ROOT/cursor-home/
  .cursor/
    commands/
    agents/
    skills/
    rules/
    # symlinks to real ~/.cursor state where needed
```

## Cursor config dir override

You can still override the config directory Cursor sees by setting:

```sh
export AGENTPACK_CURSOR_CONFIG_DIR=/path/to/cursor-config
```

When unset, agentpack points `CURSOR_CONFIG_DIR` at the staged fake-home `.cursor` directory so Cursor resolves packaged skills and commands through the same path it expects normally.

## Staged layout

```
$AGENTPACK_STAGING_ROOT/cursor/
  .cursor-plugin/
    marketplace.json
  agentpack-bundle/
    .cursor-plugin/
      plugin.json
    rules/
      <packaged-rule>.mdc
```

Rules are `.mdc` files in Cursor's native format.

## Artifact types

| Artifact | Staged as |
|---|---|
| Rules | `agentpack-bundle/rules/<name>.mdc` |
| Commands | fake `HOME` `.cursor/commands/<name>.md` |
| Agents / skills | fake `HOME` `.cursor/agents/` and `.cursor/skills/` |

## Environment variables

| Variable | Description |
|---|---|
| `AGENTPACK_CURSOR_CONFIG_DIR` | Override the Cursor config directory |
| `AGENTPACK_STAGING_ROOT` | Override the staging root |
| `AGENTPACK_LAUNCH_FULL_SYNC` | Set to `1` to sync before launch |
