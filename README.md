<<<<<<< HEAD
# agentpack

`agentpack` is a Rust CLI that pins **GitHub-hosted skills** and **plugin directories** (`.claude-plugin` and/or `.cursor-plugin`) for a project. The manifest is **`agentpack.toml`**; resolved packages live in **`pack.lock`**.

See [**AGENTS.md**](AGENTS.md) for full behavior, env vars, and harness details.

## Install

### Homebrew (tap + formula)

```bash
brew tap OlegHQ/agentpack
brew install agentpack
brew upgrade agentpack
```

The tap repo is [**OlegHQ/homebrew-agentpack**](https://github.com/OlegHQ/homebrew-agentpack) (separate from this source repo). The formula builds from the tagged **source tarball** and requires Homebrew’s **`rust`** formula.

### From source

```bash
cargo install --path .
# or
make install          # release build + copy to ~/.local/bin (override: make install INSTALL_DIR=/usr/local/bin)
```

Binary: `target/release/agentpack` (unless installed via `make install`, which copies it to **`INSTALL_DIR`**, default **`~/.local/bin`**). Ensure that directory is on **`PATH`**.
=======
<p align="center">
  <h1 align="center">agentpack</h1>
  <p align="center">
    <strong>The package manager for AI coding agents</strong>
  </p>
  <p align="center">
    Pin, resolve, and sync skills & plugins across Claude Code, Cursor, OpenCode, and Codex — from one manifest.
  </p>
</p>

<p align="center">
  <a href="https://github.com/OlegHQ/agentpack/blob/dev/LICENSE"><img alt="MIT License" src="https://img.shields.io/badge/license-MIT-blue.svg"></a>
  <a href="https://github.com/OlegHQ/agentpack/releases"><img alt="Release" src="https://img.shields.io/github/v/release/OlegHQ/agentpack?include_prereleases"></a>
  <a href="https://github.com/OlegHQ/agentpack"><img alt="Built with Rust" src="https://img.shields.io/badge/built_with-Rust-dea584.svg"></a>
</p>

---

## The Problem

Every AI coding agent has its own way of managing skills, plugins, commands, and rules — different file layouts, different config formats, different discovery mechanisms. If you use more than one agent (or want your team to share a curated set of tools), you're stuck copying files around manually.

## The Solution

**agentpack** gives you one manifest (`agentpack.toml`) that declares your agent dependencies. It resolves versions, caches content, converts artifacts per-harness, and launches each agent with the right configuration — automatically.

```
agentpack.toml  ──>  pack.lock  ──>  staged bundles  ──>  launch
   (you write)        (pinned)        (per-harness)        (agent runs)
```

## Quick Start

```bash
# Install via Homebrew
brew tap OlegHQ/agentpack
brew install agentpack

# Or from source
cargo install --path .

# Initialize in your project
agentpack init

# Add a skill from GitHub
agentpack add anthropics/skills/skills/canvas-design

# Launch your agent with everything bundled
agentpack claude    # Claude Code
agentpack agent     # Cursor Agent
agentpack opencode  # OpenCode
agentpack codex     # Codex
```

## How It Works

1. **`agentpack.toml`** — Declare dependencies (GitHub repos, subdirectories, or local paths) with version constraints.
2. **`pack.lock`** — Deterministic lockfile pins every package to an exact commit + content hash.
3. **`sync`** — Downloads, caches, and converts artifacts into per-harness staging directories.
4. **Launchers** — `agentpack claude`, `agent`, `opencode`, `codex` — each starts the target agent with the staged bundle injected via the agent's native extension mechanism (plugin dirs, config roots, or HOME overrides).

### Cross-Harness Artifact Conversion

agentpack doesn't just copy files — it **converts** between formats:

| Source artifact | Claude Code | Cursor | OpenCode | Codex |
|---|---|---|---|---|
| **Commands** | Claude frontmatter | Plain markdown | OF frontmatter | Skill fallback |
| **Agents** | Claude agent MD | Cursor agent MD | OF agent MD | Skill fallback |
| **Skills** | Normalized skill | Normalized skill | Normalized skill | Codex skill |
| **Rules** | Skill fallback | `.mdc` preserved | Skill fallback | Skill fallback |

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

- **One manifest, four agents** — Write `agentpack.toml` once, launch Claude, Cursor, OpenCode, or Codex with the same skill set.
- **Deterministic lockfile** — `pack.lock` pins exact commits and content hashes. Reproducible across machines.
- **Transitive resolution** — Dependencies can declare their own `agentpack.toml`; agentpack resolves the full tree.
- **Content-addressed cache** — Downloaded packages are stored once in `$AGENTPACK_HOME/cache/` and shared across projects.
- **No workspace pollution** — Pack content lives in staging directories, not in your git repo. Nothing is materialized into `.cursor/` or `.claude/`.
- **Offline-first** — Local mirrors (`$AGENTPACK_HOME/local/`) and cached metadata reduce network calls. Git protocol fallback when REST API is throttled.
- **Fast launch path** — Launchers skip full re-sync when inputs haven't changed since the last successful run.
- **Project-local modes** — Define `[modes]` once, then launch with `agentpack --mode <name> ...` to selectively enable or disable package paths, `.agents` content, and MCP servers.
- **Project-local agent config** — Optional `.agents/` directory (dot-agents style) merges project-specific rules, skills, and agents into staged bundles.

## Comparison

| Feature | agentpack | Manual copy | Symlinks | Git submodules |
|---|---|---|---|---|
| Multi-agent support | Claude, Cursor, OpenCode, Codex | One at a time | One at a time | One at a time |
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
| `lock` | Resolve manifest and regenerate `pack.lock` |
| `sync` | Download, cache, and rebuild staging for all harnesses |
| `mode ...` | Create, inspect, edit, and interactively manage project-local modes |
| `claude` | Sync + launch Claude Code with `--plugin-dir` |
| `agent` | Sync + launch Cursor Agent with staged HOME |
| `opencode` | Sync + launch OpenCode with staged config root |
| `codex` | Sync + launch Codex with staged CODEX_HOME |
>>>>>>> 6a45e7a44a8548f19020f43e4bd187e73f0c5c7b

## Prerequisites

- **Rust** toolchain (edition 2021), or Homebrew `rust` when installing via brew
<<<<<<< HEAD
- For release automation: [**GitHub CLI**](https://cli.github.com/) (`brew install gh`) and `gh auth login`

## Releasing (maintainers)

Run these from **this repository’s root** (`agentpack`, not the tap repo).

| Goal | Command |
|------|---------|
| Bump **0.x.y → 0.(x+1).0** | `make` or `make minor` |
| Bump **0.x.y → 0.x.(y+1)** | `make patch` |
| Release minor (bump, commit, tag, push) | `make ship-minor` |
| Release patch (bump, commit, tag, push) | `make ship-patch` |
| Push branch + tag **without** bump | `make tag-push` |
| Create/update **GitHub Release** for current `Cargo.toml` version | `make gh-release` |
| Refresh **tap formula** `url` + `sha256` | `make brew-sync` |
| **Formula commit + push** in tap clone | `make brew-ship` |

Environment:

- **`RELEASE_BRANCH`** — branch to push (default **`dev`**).
- **`HOMEBREW_TAP_DIR`** — path to a clone of [`homebrew-agentpack`](https://github.com/OlegHQ/homebrew-agentpack) (default **`../homebrew-agentpack`** next to this repo).

Typical sequence after code is ready:

```bash
make ship-patch          # or ship-minor — requires clean git tree
make gh-release          # GitHub release notes for the new tag
make brew-ship           # updates tap formula (tag tarball must exist on GitHub)
```

`brew-sync` / `brew-ship` download `https://github.com/OlegHQ/agentpack/archive/refs/tags/v<VERSION>.tar.gz` and rewrite **`HOMEBREW_TAP_DIR/Formula/agentpack.rb`**. Run them only after the tag has been pushed to GitHub.

### First-time tap setup

Create the GitHub repo, clone it, and sync the packaged tap layout:

```bash
make tap-init
cd ../homebrew-agentpack   # or your HOMEBREW_TAP_DIR
git add -A
git status
git commit -m "init tap"
git push -u origin main
```

Then tag **`v0.1.0`** (or your current `Cargo.toml` version) on **this** repo, run **`make brew-ship`**, and users can `brew tap OlegHQ/agentpack && brew install agentpack`.

Canonical tap files in this repo live under **`packaging/homebrew-tap/`**.
=======
- [**GitHub CLI**](https://cli.github.com/) for release automation (optional)

## Documentation

See [**AGENTS.md**](AGENTS.md) for full technical details: environment variables, data layout, staging internals, harness-specific behavior, and the lockfile format.
>>>>>>> 6a45e7a44a8548f19020f43e4bd187e73f0c5c7b

## License

MIT — see [LICENSE](LICENSE).
