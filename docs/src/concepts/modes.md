# Modes

A mode is a named, project-local preset that selects which staged content is active. The same `pack.lock` can be staged several ways — a lean `writing` profile, a full `default`, a `review` profile that drops a noisy MCP server — without touching your dependencies.

Modes are declared under `[modes]` in `agentpack.toml` and selected with the global `--mode` flag:

```sh
agentpack --mode writing claude
agentpack --mode review sync
```

When `--mode` is omitted, the reserved `default` mode applies. Each mode stages into its own subtree, so switching modes never rebuilds another mode's staging.

## Anatomy of a mode

```toml
[modes.default]
base = "all"

[modes.writing]
base    = "none"
enable  = [
  "package:github.com/mccteam/minecraft-console-client/.skills/humanizer",
  "package:github.com/s1mplesonny/technical-writing-skill/technical-writing",
]
disable = ["mcp:linear"]
```

| Field | Type | Meaning |
|---|---|---|
| `base` | `"all"` or `"none"` | The starting point before selectors apply: everything on, or everything off |
| `enable` | string array | Selectors to turn **on** |
| `disable` | string array | Selectors to turn **off** |

The model is straightforward: start from `base`, then apply `enable` and `disable` on top. A `base = "all"` mode with a short `disable` list is a denylist; a `base = "none"` mode with an `enable` list is an allowlist.

## Selectors

Selectors name what to toggle:

| Selector | Targets |
|---|---|
| `package:<module-id>` | A whole package |
| `package-path:<module-id>:<relative-path>` | One file inside a package (e.g. a single command) |
| `mcp:<name>` | An MCP server by name |
| `.agents:<relative-path>` | A file from the project's `./.agents/` overlay |

```toml
[modes.lean]
base    = "all"
disable = [
  "package-path:github.com/acme/heavy-pack:commands/noise.md",
  "mcp:filesystem",
]
```

## Managing modes from the CLI

You can edit `[modes]` by hand, or use the `mode` subcommands, which preserve TOML formatting:

```sh
agentpack mode list                 # all declared modes (default is always present)
agentpack mode show writing         # base + enable + disable for one mode
agentpack mode create review
agentpack mode base review none
agentpack mode enable review package:github.com/acme/shared-rules
agentpack mode disable review mcp:filesystem
agentpack mode delete review        # `default` is reserved and cannot be deleted
agentpack mode tui                  # interactive editor
```

`agentpack mode tui` opens a terminal UI for toggling selectors visually. It uses the terminal's native foreground and background, so it stays monochrome and readable with light, dark, and custom terminal themes.
