# CLI Commands

`agentpack <command> [args]`. Global flags may appear before or after the subcommand. Launcher arguments are forwarded unchanged; use `--` when an underlying agent flag overlaps an agentpack flag.

## Global flags

| Flag | Description |
|---|---|
| `--project-root <path>` | Project root holding `agentpack.toml`/`pack.lock` (default: search upward from cwd) |
| `--mode <name>` | Select a mode from `[modes]` (default: the reserved `default` mode) |
| `--yolo` | Forward each harness's "skip permission prompts" / full-access flag |
| `-q`, `--quiet` | Only print warnings and errors |
| `--no-progress` | Disable spinners and progress bars |
| `--debug` | Print launcher diagnostics (workspace paths, env overrides, fast-sync skip reason) |
| `--proxy` | Run `agentpack claude` through the supervised Anthropic-compatible Codex proxy |
| `-h`, `--help` | Print help |
| `-V`, `--version` | Print the agentpack version |

Help and errors use the terminal's color profile when interactive and automatically fall back to plain text when redirected. Contextual help is available at every level, for example `agentpack mcp add --help`.

## Shell completion

Generate a completion script using the built-in `completion` command:

```sh
agentpack completion bash
agentpack completion zsh
agentpack completion fish
agentpack completion powershell
```

The command prints the script to stdout so you can source it directly or save it in your shell's completion directory.

## Lifecycle commands

### `agentpack init`

Create `agentpack.toml` and a v2 `pack.lock` in the project root, and ensure `AGENTPACK_HOME` exists. Fails if `agentpack.toml` already exists.

```sh
agentpack init
agentpack init --name my-project --version 0.1.0
```

### `agentpack add <spec>`

Resolve a package spec, append it under `[dependencies]`, refresh `pack.lock`, then sync.

```sh
agentpack add anthropics/skills/skills/canvas-design
agentpack add github.com/acme/monorepo/packages/rules@v1.2.0
agentpack add ./local-rules
agentpack add anthropics/skills/skills/canvas-design --no-sync
```

A spec may be a module ID, `owner/repo[/path]` shorthand, a GitHub tree/blob URL, a single-segment local/alias name, or a filesystem path. An optional `@ref` pins a branch, tag, or commit. `--no-sync` skips the sync step. (Requires a manifest.)

### `agentpack remove <spec>`

Drop a matching `[dependencies]` entry, prune mode selectors that targeted it, refresh `pack.lock`, then sync unless `--no-sync`.

```sh
agentpack remove github.com/acme/monorepo/packages/rules
```

### `agentpack lock`

Resolve `agentpack.toml` and rewrite `pack.lock` (direct + transitive). Network calls for ref/tag resolution; no content download.

```sh
agentpack lock            # keep commits already pinned
agentpack lock --update   # re-resolve floating pins from GitHub
```

### `agentpack sync`

Ensure the cache and rebuild staging for every harness. Recomputes `pack.lock` from the manifest when `[dependencies]` is non-empty.

```sh
agentpack sync
agentpack --mode writing sync
agentpack sync --dry-run        # report actions without writing
agentpack sync --verify-only    # check cache + staging integrity only
agentpack sync --update-lock    # re-resolve floating pins while syncing
```

## Launchers

Each launcher runs a fast pre-sync, stages for its harness, and execs the binary. Trailing arguments are forwarded.

| Command | Launches | Mechanism |
|---|---|---|
| `agentpack claude` | Claude Code | `--plugin-dir` + `--settings` |
| `agentpack agent` (alias `cursor-agent`) | Cursor Agent | synthetic `HOME` |
| `agentpack opencode` | OpenCode | `OPENCODE_CONFIG_DIR` |
| `agentpack codex` | Codex | `CODEX_HOME` |
| `agentpack grok` | Grok | `GROK_HOME` (+ injected `--cwd`) |
| `agentpack agy` | Antigravity | injected `--add-dir` |

```sh
agentpack claude --model opus
agentpack --proxy claude
agentpack --yolo codex
agentpack --mode writing claude
```

See the [harness guides](../harnesses/claude.md) for what each one stages.

## `agentpack mcp`

Manage `[mcp.servers]` in the manifest. `add`/`remove` sync afterward unless `--no-sync`.

```sh
agentpack mcp add retrieval --command uvx --args mcp-retrieval --env API_KEY=sk-...
agentpack mcp remove retrieval
agentpack mcp list      # all servers with provenance (manifest / plugin / .agents)
```

## `agentpack mode`

Manage `[modes]` in the manifest.

```sh
agentpack mode list
agentpack mode show writing
agentpack mode create review
agentpack mode base review none
agentpack mode enable review package:github.com/acme/shared-rules
agentpack mode disable review mcp:filesystem
agentpack mode delete review        # `default` is reserved
agentpack mode tui                  # interactive editor
```

See [Modes](../concepts/modes.md) for selector syntax.
