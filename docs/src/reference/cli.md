# CLI Commands

`agentpack <command> [args]`. Global flags may appear before or after the subcommand. Launcher arguments are forwarded unchanged; use `--` when an underlying agent flag overlaps an agentpack flag.

## Global flags

| Flag | Description |
|---|---|
| `--project-root <path>` | Legacy single root for `agentpack.toml`/`pack.lock` (default: search upward from cwd) |
| `--definition-root <path>` | External directory holding `agentpack.toml`/`pack.lock` (portable definition) |
| `--workspace <path>` | Checkout / agent cwd for `.agents` and overlays (default: cwd) |
| `--strict-external` | Refuse Cursor/Agy workspace overlay writes; prefer Claude/OpenCode |
| `--mode <name>` | Select a mode from `[modes]` (default: the reserved `default` mode) |
| `--yolo` | Forward each harness's "skip permission prompts" / full-access flag |
| `-q`, `--quiet` | Only print warnings and errors |
| `--no-progress` | Disable spinners and progress bars |
| `--debug` | Print launcher diagnostics (workspace paths, env overrides, fast-sync skip reason) |
| `--proxy` | Run `agentpack claude` through the supervised Anthropic-compatible Codex proxy |
| `-h`, `--help` | Print help |
| `-V`, `--version` | Print the agentpack version |

When `--env`, `--definition-root`, or an `env use`/`env bind` binding is set, lock/manifest operations use the definition root while `.agents` and overlays use the workspace. See [Portable Environments](../concepts/environments.md).

Help and errors use the terminal's native foreground and transparent background, with no semantic colors or theme detection. Contextual help is available at every level, for example `agentpack mcp add --help`.

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

Create `agentpack.toml` and an empty `pack.lock` in the project root, and ensure `AGENTPACK_HOME` exists. Fails if `agentpack.toml` already exists.

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
agentpack lock                        # keep commits and MCP versions already pinned
agentpack lock --update               # re-resolve floating pins from GitHub and npm dist-tags
agentpack lock --allow-unpinned-mcp   # record an MCP server as unpinned if its registry is unreachable
```

`lock` records a `content_hash` for every package (downloading any package it has no hash for) and a record for every MCP server. See [Integrity and Verification](../concepts/integrity.md).

### `agentpack sync`

Ensure the cache and rebuild staging for every harness. Recomputes `pack.lock` from the manifest when `[dependencies]` is non-empty.

```sh
agentpack sync
agentpack --mode writing sync
agentpack sync --dry-run        # report actions without writing
agentpack sync --verify-only    # check cache + staging integrity only
agentpack sync --update-lock    # re-resolve floating pins while syncing
agentpack sync --repair         # re-fetch cache entries that do not match pack.lock
```

Every sync compares each cache entry with the `content_hash` in `pack.lock` and exits non-zero on a mismatch without staging anything. A lock whose entries are malformed or inconsistent is refused the same way, and `sync` never changes the pins of entries that are already locked; see [what `sync` may write](../concepts/integrity.md#what-sync-may-write-to-packlock).

`--verify-only` runs the lock and cache checks without rebuilding, then hashes the staged files and fails, naming each file, if any was modified, added or removed since the last sync. A plain `sync` rebuilds staging and prints which changed files it replaced. `--repair` downloads the pinned commit again, verifies it, replaces the cache entry, and reports what it replaced. For a check that must not mutate lock, cache, staging, or the checkout, use `preflight` instead.

### `agentpack preflight`

Pure offline inspection of a locked environment. Does not download, repair, rewrite `pack.lock`, rebuild staging, or write workspace overlays. Prints a human summary or `--json` report with stable finding codes, severity, source, target, and remedy. Optional `--contract` / `contract.json` and `--receipt ID|PATH|latest` for observed coverage / freshness. `--mode` selects the same mode as restore and launch. Receipt policy and strictness must match the preflight invocation; stale or incomplete receipts fail closed. Strict external launches evaluate the default contract under their staging lease.

```sh
agentpack preflight --agent claude
agentpack preflight --agent opencode --json --policy ci
agentpack --env team --workspace . --strict-external preflight --agent claude
agentpack preflight --agent claude --receipt RECEIPT_ID --json
```

### `agentpack probe`

Explicit native observation (never inside preflight). Claude: `plugin list --json` + `plugin details` against the staged bundle. Codex: version, plus validated 0.159.2 positive skill discovery from native prompt input in a disposable static projection. Observations use explicit `controlled_projection` scope; they do not prove full interactive configuration or model use. Writes a receipt under `$AGENTPACK_HOME/projects/<hash>/probe-receipts/`.

```sh
agentpack sync
agentpack probe --agent claude
agentpack --mode review probe --agent codex --json
agentpack --strict-external probe --agent claude --policy ci
agentpack --strict-external preflight --agent claude --policy ci --receipt latest
```

### `agentpack env`

Manage portable external definitions under `$AGENTPACK_HOME/environments` (or an explicit `--dir`). `env bind` is an alias for `env use`. `env restore` is frozen: exact locked cache + staging rebuild, no `pack.lock` rewrite.

```sh
agentpack env init team --from .
agentpack env use team --project /path/to/checkout
agentpack env status
agentpack env export team.bundle --from team
agentpack env import team.bundle --name team
agentpack --env team --workspace /path/to/checkout --mode review env restore
agentpack env unuse
agentpack env list
```

### `agentpack support export`

Write a redacted diagnostic capsule from a probe receipt (`--output` required). Paths, free-form native messages, arbitrary values and secret-bearing digests are omitted; preview notes are printed before sharing.

```sh
agentpack support export RECEIPT_ID --output ./support-capsule.json
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

Manage `[mcp.servers]` in the manifest. `add`/`remove` update the server records in `pack.lock` and sync afterward unless `--no-sync`.

```sh
agentpack mcp add retrieval --command uvx --args mcp-retrieval --env API_KEY=sk-...
agentpack mcp add playwright --command npx --args @playwright/mcp@latest --allow-unpinned
agentpack mcp remove retrieval
agentpack mcp list      # all servers with provenance and lock status
```

`add` resolves an npm server to an exact version and fails if the registry is unreachable; `--allow-unpinned` records it as unpinned instead. See [MCP Servers](../concepts/mcp.md#what-packlock-records).

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

## `agentpack extra sync-claude`

Reconciles a project's `.claude/skills` and `.agents/skills` directories so a *local* skill
authored under either one reaches both. Claude Code only discovers project-local skills under
`.claude/skills`, while the dot-agents convention shares project-local content across every
harness under `.agents/skills`. This is unrelated to fetched pack content or `$STAGING` — it only
touches the two real directories in your project.

A skill present on only one side is copied to the other. A skill present on both sides with
differing content is reconciled toward whichever copy has the newer file modification time.

```sh
agentpack extra sync-claude             # reconcile in place
agentpack extra sync-claude --dry-run   # show what would change
```
