# Modes Plan Specification

This document captures the agreed implementation plan for adding **modes** to `agentpack` before code changes begin.

## Goals

Add a project-local preset system that can selectively enable or disable anything `agentpack` stages or injects, then launch harnesses with that preset.

Examples:

- `agentpack claude` → runs the reserved `default` mode
- `agentpack --mode=design claude`
- `agentpack --yolo --mode=simplifier opencode`
- `agentpack mode ...` → regular CLI mode management
- `agentpack mode tui` → interactive mode editor

## Agreed Product Decisions

- Modes are **project-local only** and live in `agentpack.toml`
- Packages and `.agents` contribute **capabilities to toggle**, not reusable mode definitions
- `default` is **reserved** and **non-deletable**
- Every mode edit updates `agentpack.toml`
- Modes are the **only** selective enable/disable system
- Existing `overrides` should be **removed**, not migrated
- No backwards-compatibility work is required for `overrides`

## High-Level Design

Modes are a **staging and launch overlay**, not a dependency-resolution feature.

That means:

- `agentpack.toml` remains the source of truth
- `pack.lock` continues to describe resolved packages
- mode selection changes what gets staged and launched
- mode selection does **not** change how dependencies are resolved

## Manifest Changes

Add a top-level `[modes]` table to `agentpack.toml`.

Recommended shape:

```toml
name = "myproj"
version = "0.0.1"

[dependencies]
"github.com/acme/design-pack" = {}
"github.com/acme/simplify-pack" = {}

[mcp.servers.filesystem]
command = "npx"
args = ["-y", "@modelcontextprotocol/server-filesystem"]

[modes.default]
base = "all"
disable = [
  "package-path:github.com/acme/design-pack:commands/noisy.md",
  "package-path:github.com/acme/design-pack:hooks",
]

[modes.design]
base = "all"
disable = [
  "package:github.com/acme/simplify-pack",
  "mcp:filesystem",
]

[modes.simplifier]
base = "none"
enable = [
  "package:github.com/acme/simplify-pack",
  ".agents:agents/code-simplifier.md",
]
```

### Mode semantics

- `base = "all"` means everything is enabled unless disabled
- `base = "none"` means everything is disabled unless enabled
- `default` is used when `--mode` is omitted
- `default` cannot be deleted

### Manifest ownership

`agentpack.toml` becomes the sole persisted source of truth for:

- dependencies
- project MCP definitions
- modes

There should be no separate mode state file and no hidden TUI database.

## Remove `overrides`

Delete `overrides` from the design entirely.

Specifically:

- remove `[overrides]` from manifest parsing/writing
- remove override data structures from `src/manifest/mod.rs`
- remove override-based filtering from staging
- replace all old override behavior with `modes.default`

## Selector Grammar

Use one selector syntax for all toggles:

- `package:<module>`
- `package-path:<module>:<rel-path>`
- `mcp:<name>`
- `.agents:<rel-path>`

Examples:

- `package:github.com/acme/design-pack`
- `package-path:github.com/acme/design-pack:commands/review.md`
- `package-path:github.com/acme/design-pack:hooks`
- `mcp:filesystem`
- `.agents:rules/backend.mdc`

## Capability Coverage

Modes must be able to toggle every staged or injected capability, including:

- whole package
- package subtree or file:
  - commands
  - agents
  - skills
  - rules
  - hooks
  - support directories
  - root files like `mcp.json`
- `.agents` files and directories
- MCP servers from manifest and plugins

Derived behavior:

- disabling a rule also disables any injected guidance derived from that rule
- disabling hook paths prevents hook staging
- disabling package `mcp.json` removes those plugin MCP contributions

## Core Architecture

Add a dedicated mode layer:

- `src/mode/mod.rs`
- `src/mode/catalog.rs`
- `src/mode/selectors.rs`
- `src/mode/filter.rs`

Responsibilities:

- parse selectors
- discover togglable capabilities
- compute effective mode state from `base + enable + disable`
- validate references against current project state
- answer `is_enabled(...)` checks for staging and launch code

## Manifest Implementation Work

In `src/manifest/mod.rs`:

- remove `OverrideTable`
- remove `overrides` fields from manifest structs
- remove override helpers such as path-disable lookup
- add mode data structures
- add TOML-edit helpers for:
  - create mode
  - delete mode
  - rename mode
  - set base
  - add/remove selectors
  - list modes

Recommendation:

- treat `default` as implicit for reads if absent
- once the user edits modes, materialize `default` explicitly in `agentpack.toml`

## CLI Plan

Add a global flag:

- `--mode <name>`

Add a new command tree:

- `agentpack mode list`
- `agentpack mode show <name>`
- `agentpack mode create <name>`
- `agentpack mode delete <name>`
- `agentpack mode enable <name> <selector>...`
- `agentpack mode disable <name> <selector>...`
- `agentpack mode base <name> <all|none>`
- `agentpack mode tui [name]`

Command behavior:

- mode management commands update `agentpack.toml` directly
- deleting `default` must error
- selecting an unknown mode must error clearly

## Staging Plan

Thread an `EffectiveMode` through staging entry points, including:

- `src/staging/harnesses.rs`
- `src/staging/pack_overlay.rs`
- `src/staging/mcp.rs`
- `src/staging/dot_agents.rs`
- `src/staging/guidance.rs`
- `src/hooks/stage.rs`

Behavior by area:

### Pack overlay

- before staging any package file or subtree, ask the mode filter whether it is enabled
- use `package:` and `package-path:` selectors

### MCP merge

- skip disabled MCP entries via `mcp:<name>`
- preserve existing source precedence for entries that remain enabled

### `.agents`

- filter copied `.agents` content using `.agents:<rel-path>` selectors

### Guidance

- only collect enabled rules

### Hooks

- only collect enabled hook files and package hook trees

## Launch and Sync Plan

Mode affects staged output, so it must be part of launch sync identity.

Update:

- `src/sync/run.rs`
- `src/sync/launch_fingerprint.rs`
- launcher function signatures

Required behavior:

- `sync_for_launch(project_root, mode, ui)`
- launch fingerprint includes selected mode and effective mode config
- staging paths should be mode-specific to avoid collisions between modes

Recommendation:

- use a per-mode staging subtree under the project staging root
- plain launcher commands use the `default` subtree

## Dependency Tree for TUI

Current constraint: `pack.lock` is flat and does not persist dependency edges.

Plan:

- do **not** change lockfile schema in v1
- reconstruct the dependency tree on demand from:
  - direct dependencies in `agentpack.toml`
  - cached nested `agentpack.toml` files already used during resolution

This is sufficient for the TUI capability tree without coupling modes to lockfile evolution.

## TUI Plan

Command:

- `agentpack mode tui`

Recommended implementation:

- `ratatui`
- `crossterm`

Layout:

- left pane: mode list
- center pane: dependency/capability tree
- right pane: details, selector preview, help

Capabilities:

- create mode
- rename mode
- delete mode
- set base to all/none
- enable all under a node
- disable all under a node
- toggle individual nodes
- save updates to `agentpack.toml`

Rules:

- `default` is visible but non-deletable
- TUI edits are manifest edits
- no separate persistence outside `agentpack.toml`

## Execution Order

Mode application order should be:

1. baseline project/lock/config behavior
2. selected mode filtering
3. harness-specific runtime patches like `--yolo`

This keeps `--mode` orthogonal to `--yolo`.

## Testing Plan

Add tests for:

- manifest parse/save without overrides
- mode CRUD writing correct TOML
- selector parsing
- `default` mode semantics
- package/package-path filtering during staging
- `.agents` filtering
- MCP filtering
- guidance filtering
- launch fingerprint changes across modes
- mode-specific staging root selection
- TUI state/reducer behavior and manifest writeback

## Documentation Plan

Update documentation to remove mentions of `overrides` and replace them with modes.

Files likely affected:

- `AGENTS.md`
- `README.md`
- manifest schema/reference docs
- CLI docs
- harness launch examples

Document:

- the `[modes]` schema
- reserved `default`
- selector syntax
- `--mode` launch usage
- `agentpack mode` and `agentpack mode tui`

## Recommended Implementation Order

1. Remove `overrides` from manifest and staging
2. Add `[modes]` manifest schema and TOML editing helpers
3. Add selector parsing and effective mode engine
4. Make staging mode-aware
5. Add global `--mode` and launcher wiring
6. Add `agentpack mode` CLI
7. Add TUI
8. Update docs and tests

## Non-Goals for v1

- package-defined reusable mode presets
- lockfile schema expansion for dependency edges
- override migration or compatibility shims
- hidden state outside `agentpack.toml`
