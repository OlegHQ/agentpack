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
  <a href="https://oleghq.github.io/agentpack"><img alt="Documentation" src="https://img.shields.io/badge/docs-oleghq.github.io-1f6feb.svg"></a>
  <a href="https://nexo.sh/posts/agentpack/"><img alt="Writeup" src="https://img.shields.io/badge/writeup-nexo.sh-ff6b6b.svg"></a>
  <a href="https://github.com/OlegHQ/agentpack/blob/dev/LICENSE"><img alt="MIT License" src="https://img.shields.io/badge/license-MIT-blue.svg"></a>
  <a href="https://github.com/OlegHQ/agentpack/releases"><img alt="Version" src="https://img.shields.io/badge/version-v0.3.13-blue.svg"></a>
  <a href="https://github.com/OlegHQ/agentpack"><img alt="Built with Go" src="https://img.shields.io/badge/built_with-Go-00ADD8.svg"></a>
</p>

<p align="center">
  <a href="https://oleghq.github.io/agentpack"><strong>Documentation</strong></a> ·
  <a href="https://nexo.sh/posts/agentpack/"><strong>Writeup</strong></a> ·
  <a href="#quick-start">Quick Start</a> ·
  <a href="#how-it-works">How It Works</a> ·
  <a href="https://oleghq.github.io/agentpack/reference/cli.html">CLI Reference</a>
</p>

```sh
brew install --cask OlegHQ/tap/agentpack
```

<p align="center"><sub>Prefer a prebuilt binary or source build? See <a href="#install">all install options</a>.</sub></p>

---

## The Problem

Every coding agent manages skills, plugins, commands, and rules its own way — different file layouts, config formats, and discovery rules. Run more than one agent, or share a curated toolset across a team, and you end up pasting the same files into each agent's config by hand and watching the copies drift apart. There's no lockfile to say which version everyone is actually on.

## What agentpack Does

Declare your agent dependencies once in `agentpack.toml`. agentpack resolves them to exact commits, pins those in a lockfile, caches the content, re-renders each artifact into the target agent's native format, and launches the agent with everything staged. Your repo stays clean: nothing is copied into it, and nothing is written to your global agent config.

```text
agentpack.toml  ──>  pack.lock  ──>  staged bundles  ──>  launch
   (you write)        (pinned)        (per-harness)        (agent runs)
```

## Install

Prebuilt binaries are published for every release. Pick whichever fits your platform.

### Homebrew — macOS & Linux (recommended)

```bash
brew install --cask OlegHQ/tap/agentpack
```

Upgrade with `brew upgrade --cask agentpack`. Served from the shared tap
[OlegHQ/homebrew-tap](https://github.com/OlegHQ/homebrew-tap) (`brew tap OlegHQ/tap && brew install --cask agentpack` also works).

### Prebuilt binaries

Download an archive for your platform from the [latest release](https://github.com/OlegHQ/agentpack/releases/latest), extract it, and put `agentpack` on your `PATH`. Supported targets:

| Platform | Target |
| --- | --- |
| macOS (Apple Silicon) | `darwin_arm64` |
| macOS (Intel) | `darwin_amd64` |
| Linux (x86-64) | `linux_amd64` |
| Linux (ARM64) | `linux_arm64` |
| Windows (x86-64) | `windows_amd64` |

Each release includes a SHA-256 `checksums.txt` covering every archive.

### From source

```bash
go install ./cmd/agentpack
# or
make install   # release build to ~/.local/bin (override: make install INSTALL_DIR=/usr/local/bin)
```

**Prerequisites (source builds only):** Go 1.24 or newer.

## Quick Start

```bash
# Initialize agentpack in your project
agentpack init

# Add a skill from GitHub (resolves, locks, and syncs in one step)
agentpack add anthropics/skills/skills/canvas-design

# Pin to a branch, tag, or commit with an @ref, or edit the entry to a table like { version = "^1.0.0" }
agentpack add anthropics/claude-plugins-official/plugins/hookify@main

# Launch Claude Code with everything bundled
agentpack claude

# Or launch any other agent
agentpack agent     # Cursor Agent (alias: cursor-agent)
agentpack opencode  # OpenCode
agentpack codex     # Codex
agentpack grok      # Grok
agentpack agy       # Antigravity
```

### Experimental Claude Proxy Mode

`agentpack --proxy claude` starts Claude Code through an experimental, supervised
Anthropic-compatible proxy backed by OpenAI/Codex credentials. The proxy is started for that Claude
session, `ANTHROPIC_BASE_URL` and `ANTHROPIC_AUTH_TOKEN` are injected into the child process, and the
proxy is shut down when Claude exits.

```bash
agentpack --proxy claude
```

Authentication is resolved from `OPENAI_API_KEY` first, then from Codex auth (`~/.codex/auth.json`,
the Codex keychain entry, or agentpack's shared Codex auth file). The Go proxy core follows
the Codex/OpenAI path in [raine/claude-code-proxy](https://github.com/raine/claude-code-proxy);
thanks to [@raine](https://github.com/raine) for the original project and reference behavior.

Proxy diagnostics are written as JSONL under
`$AGENTPACK_HOME/projects/<project-hash>/proxy-logs/` by default. Set
`AGENTPACK_PROXY_LOG_DIR` to override the directory. Logs omit request payloads unless
`AGENTPACK_PROXY_LOG_PAYLOADS=1` is set.

## How It Works

1. **`agentpack.toml`** — Declare dependencies (GitHub repos, subdirectories, single-plugin marketplace repos, or local paths) with version constraints.
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
"github.com/anthropics/claude-plugins-official/plugins/hookify" = { branch = "main" }
"github.com/my-org/internal-tools/skills/deploy" = { tag = "v2.1.0" }

[modes.default]
base = "all"
disable = ["package-path:github.com/my-org/internal-tools/skills/deploy:commands/noise.md"]

[modes.design]
base = "all"
disable = ["mcp:filesystem"]
```

## Key Features

- **One manifest, six agents** — Write `agentpack.toml` once; launch Claude, Cursor, OpenCode, Codex, Grok, or Antigravity with the same skill set.
- **Deterministic lockfile** — `pack.lock` pins every package, direct and transitive, to an exact commit and content hash, so resolution matches across machines and CI.
- **Transitive resolution** — A package can declare its own `agentpack.toml`; agentpack walks the full graph and resolves conflicts.
- **Content-addressed cache** — Each package is fetched once into `$AGENTPACK_HOME/cache/<cache_key>/` and shared by every project on the machine.
- **No workspace pollution** — Pack content stays in staging trees outside your repo, and never symlinks into your real `~/.claude` or `~/.cursor`.
- **Resilient resolution** — Cached metadata and a `local/` mirror cut network calls; when the GitHub REST API is throttled, agentpack falls back to the Git protocol.
- **Fast launch path** — When the manifest, lock, and `./.agents/` are unchanged, launchers verify staging and skip the full re-sync.
- **Project-local modes** — `agentpack --mode <name>` enables or disables packages, individual files, and MCP servers per task.

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

Redirected harness homes do not make resume history temporary: Codex history is shared with `~/.codex`, Grok sessions with `~/.grok/sessions`, and legacy temp-staging history is imported on upgrade without overwriting native files.

## Documentation

Full docs live at **<https://oleghq.github.io/agentpack>** — getting started, core concepts (manifest, lockfile, resolution, cache, staging, modes, MCP), a guide per harness, and a complete CLI / manifest / environment reference.

The book source is in `docs/`. To preview changes locally, install [`mdbook`](https://rust-lang.github.io/mdBook/) and run:

```bash
mdbook serve docs   # http://localhost:3000
```

For internals, [AGENTS.md](AGENTS.md) is the contributor-facing source of truth: data layout, staging internals, per-harness mechanics, and the lockfile format.

## Releasing (maintainers)

Releases are automated by [GoReleaser](https://goreleaser.com): pushing a `v*` tag triggers
`.github/workflows/release.yml`, which builds per-platform binaries (macOS arm64/x86_64, Linux
x86_64/arm64, Windows x86_64), creates archives and checksums, publishes the GitHub Release, and
pushes the generated Homebrew cask to [OlegHQ/homebrew-tap](https://github.com/OlegHQ/homebrew-tap).

| Goal | Command |
|---|---|
| Bump minor + tag + push (→ CI release) | `make ship-minor` |
| Bump patch + tag + push (→ CI release) | `make ship-patch` |
| Bump version only (no release) | `make minor` / `make patch` |

Requires a repo secret **`HOMEBREW_TAP_TOKEN`** (a PAT with write access to `OlegHQ/homebrew-tap`) so
CI can push the cask across repos.

## License

MIT — see [LICENSE](LICENSE).
