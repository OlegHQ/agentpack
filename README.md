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

## Prerequisites

- **Rust** toolchain (edition 2021), or Homebrew `rust` when installing via brew
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

## License

MIT — see [LICENSE](LICENSE).
