<p align="center">
  <h1 align="center">agentpack</h1>
  <p align="center">
    <strong>The package manager for AI coding agents</strong>
  </p>
  <p align="center">
    Pin, resolve, and sync skills &amp; plugins across Claude Code, Cursor, OpenCode, Codex, Grok, and Antigravity — from one manifest.
  </p>
</p>

<p align="center">
  <a href="https://github.com/OlegHQ/agentpack/blob/dev/LICENSE"><img alt="MIT License" src="https://img.shields.io/badge/license-MIT-blue.svg"></a>
  <a href="https://github.com/OlegHQ/agentpack/releases"><img alt="Version" src="https://img.shields.io/badge/version-v0.3.1-blue.svg"></a>
  <a href="https://github.com/OlegHQ/agentpack"><img alt="Built with Rust" src="https://img.shields.io/badge/built_with-Rust-dea584.svg"></a>
</p>

---

## The Problem

Every AI coding agent has its own way of managing skills, plugins, commands, and rules — different file layouts, different config formats, different discovery mechanisms. If you use more than one agent (or want your team to share a curated set of tools), you are stuck copying files around manually.

## The Solution

**agentpack** gives you one manifest (`agentpack.toml`) that declares your agent dependencies. It resolves versions, caches content, converts artifacts per-harness, and launches each agent with the right configuration — automatically.

```
agentpack.toml  ──>  pack.lock  ──>  staged bundles  ──>  launch
   (you write)        (pinned)        (per-harness)        (agent runs)
```

## Install

### Homebrew (recommended)

```bash
brew install OlegHQ/tap/agentpack
```

To upgrade:

```bash
brew upgrade agentpack
```

Served from the shared tap [OlegHQ/homebrew-tap](https://github.com/OlegHQ/homebrew-tap)
(`brew tap OlegHQ/tap && brew install agentpack` also works).

### From source

```bash
cargo install --path .
# or
make install   # release build to ~/.local/bin (override: make install INSTALL_DIR=/usr/local/bin)
```

**Prerequisites:** Rust toolchain (edition 2021). Optional: [GitHub CLI](https://cli.github.com/) for release automation.

## Quick Start

```bash
# Initialize agentpack in your project
agentpack init

# Add a skill from GitHub (resolves, locks, and syncs in one step)
agentpack add anthropics/skills/skills/canvas-design

# Pin to a tag with an @ref, or edit the entry to { version = "^1.0.0" }
agentpack add anthropics/claude-plugins-official/plugins/hookify@v1.0.0

# Launch Claude Code with everything bundled
agentpack claude

# Or launch any other agent
agentpack agent     # Cursor Agent (alias: cursor-agent)
agentpack opencode  # OpenCode
agentpack codex     # Codex
agentpack grok      # Grok
agentpack agy       # Antigravity
```

## How It Works

1. **`agentpack.toml`** — Declare dependencies (GitHub repos, subdirectories, or local paths) with version constraints.
2. **`pack.lock`** — Deterministic lockfile pins every package to an exact commit + content hash.
3. **`sync`** — Downloads, caches, and converts artifacts into per-harness staging directories.
4. **Launchers** — `agentpack claude`, `agent`, `opencode`, `codex`, `grok`, `agy` each start the target agent with the staged bundle injected via that agent's native extension mechanism.

### Cross-Harness Artifact Conversion

agentpack does not just copy files — it **parses** each artifact and **re-renders** it in the target format:

| Source artifact | Claude Code | Cursor | OpenCode | Codex |
|---|---|---|---|---|
| **Commands** | Claude frontmatter | Plain markdown | OpenCode frontmatter | Skill fallback |
| **Agents** | Claude agent MD | Cursor agent MD | OpenCode agent MD | Skill fallback |
| **Skills** | Normalized skill | Normalized skill | Normalized skill | Codex skill |
| **Rules** | Skill fallback | `.mdc` preserved | Skill fallback | Skill fallback |

Grok mirrors Claude's command/agent formats; Antigravity preserves rules natively like Cursor. See the [conversion guide](https://github.com/OlegHQ/agentpack/blob/dev/docs/src/harnesses/conversion.md) for the full matrix.

## Manifest Example

```toml
name = "my-project"
version = "0.1.0"

[dependencies]
"github.com/anthropics/skills/skills/canvas-design" = { branch = "main" }
"github.com/anthropics/claude-plugins-official/plugins/hookify" = { version = "^1.0.0" }
"github.com/my-org/internal-tools/skills/deploy" = { tag = "v2.1.0" }

[modes.default]
base = "all"
disable = ["package-path:github.com/my-org/internal-tools/skills/deploy:commands/noise.md"]

[modes.design]
base = "all"
disable = ["mcp:filesystem"]
```

## Key Features

- **One manifest, six agents** — Write `agentpack.toml` once, launch Claude, Cursor, OpenCode, Codex, Grok, or Antigravity with the same skill set.
- **Deterministic lockfile** — `pack.lock` pins exact commits and content hashes. Reproducible across machines.
- **Transitive resolution** — Dependencies can declare their own `agentpack.toml`; agentpack resolves the full tree.
- **Content-addressed cache** — Downloaded packages stored once in `$AGENTPACK_HOME/cache/` and shared across projects.
- **No workspace pollution** — Pack content lives in staging directories, not in your git repo.
- **Offline-first** — Local mirrors and cached metadata reduce network calls; git protocol fallback when REST API is throttled.
- **Fast launch path** — Launchers skip full re-sync when inputs have not changed since the last successful run.
- **Project-local modes** — `agentpack --mode <name>` to selectively enable or disable package paths and MCP servers.

## Comparison

| Feature | agentpack | Manual copy | Symlinks | Git submodules |
|---|---|---|---|---|
| Multi-agent support | Claude, Cursor, OpenCode, Codex, Grok, Antigravity | One at a time | One at a time | One at a time |
| Version pinning | Semver, branch, tag, commit | None | None | Commit only |
| Transitive deps | Yes | No | No | Manual |
| Artifact conversion | Automatic per-harness | Manual | No | No |
| Cache / dedup | Content-addressed | None | N/A | Partial |
| Workspace isolation | Full (staging dirs) | Files in repo | Symlinks in repo | Dirs in repo |
| Lockfile | Deterministic | None | None | `.gitmodules` |

## Commands

| Command | Description |
|---|---|
| `init` | Create `agentpack.toml` and `pack.lock` in the current project |
| `add <spec>` | Add a dependency, resolve, lock, and sync |
| `remove <spec>` | Remove a dependency and re-sync |
| `lock` | Resolve manifest and regenerate `pack.lock` (`--update` advances floating pins) |
| `sync` | Download, cache, and rebuild staging for all harnesses |
| `mode ...` | Create, inspect, edit, and manage project-local modes |
| `mcp ...` | Add, remove, and list MCP servers |
| `claude` | Sync + launch Claude Code with `--plugin-dir` |
| `agent` | Sync + launch Cursor Agent with staged `HOME` (alias: `cursor-agent`) |
| `opencode` | Sync + launch OpenCode with `OPENCODE_CONFIG_DIR` |
| `codex` | Sync + launch Codex with `CODEX_HOME` |
| `grok` | Sync + launch Grok with `GROK_HOME` |
| `agy` | Sync + launch Antigravity with the project as a workspace dir |

## Documentation

The **[documentation book](docs/src/SUMMARY.md)** under `docs/` is the place to start — getting started, core concepts (manifest, lockfile, resolution, cache, staging, modes, MCP), a guide per harness, and a full CLI/manifest/env reference. Build it locally with [`mdbook`](https://rust-lang.github.io/mdBook/):

```bash
mdbook serve docs   # http://localhost:3000
```

[AGENTS.md](AGENTS.md) is the contributor-facing source of truth for internal behavior: data layout, staging internals, per-harness mechanics, and the lockfile format.

## Releasing (maintainers)

Releases are automated by [cargo-dist](https://github.com/axodotdev/cargo-dist): pushing a `v*` tag
triggers `.github/workflows/release.yml`, which builds per-platform binaries (macOS arm64/x86_64,
Linux x86_64/arm64, Windows x86_64), creates the GitHub Release with those assets + a `curl | sh`
installer, and pushes the generated Homebrew formula to [OlegHQ/homebrew-tap](https://github.com/OlegHQ/homebrew-tap).

| Goal | Command |
|---|---|
| Bump minor + tag + push (→ CI release) | `make ship-minor` |
| Bump patch + tag + push (→ CI release) | `make ship-patch` |
| Bump version only (no release) | `make minor` / `make patch` |

Requires a repo secret **`HOMEBREW_TAP_TOKEN`** (a PAT with write access to `OlegHQ/homebrew-tap`) so
CI can push the formula across repos.

## License

MIT — see [LICENSE](LICENSE).