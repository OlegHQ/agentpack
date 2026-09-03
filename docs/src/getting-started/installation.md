# Installation

agentpack ships as a single binary. The recommended path is Homebrew; source builds use the Go toolchain.

## Homebrew (macOS and Linux)

```sh
brew install --cask OlegHQ/tap/agentpack
```

That one line taps `OlegHQ/homebrew-tap` and installs the cask. The two-step form works too:

```sh
brew tap OlegHQ/tap
brew install --cask agentpack
```

Upgrade later with:

```sh
brew upgrade --cask agentpack
```

## Prebuilt binaries

Each release publishes GoReleaser archives for macOS (arm64, x86_64), Linux (x86_64, arm64), and Windows (x86_64), plus a SHA-256 `checksums.txt` file.

Pick a specific version from the [releases page](https://github.com/OlegHQ/agentpack/releases) if you don't want `latest`.

## From source

You need Go 1.24 or newer, then:

```sh
git clone https://github.com/OlegHQ/agentpack.git
cd agentpack
go install ./cmd/agentpack
```

This installs `agentpack` under `GOBIN` (or the default Go bin directory); make sure that directory is on your `PATH`. The repo's `Makefile` also offers `make install`, which builds a stripped binary and copies it to `~/.local/bin` (override with `make install INSTALL_DIR=/usr/local/bin`).

## Verify

```sh
agentpack --version
```

## Where agentpack keeps its state

All cached content, the metadata index, and per-project bookkeeping live under a single user-wide directory, **not** inside your repo. When `AGENTPACK_HOME` is unset, the default is:

- **Linux/macOS** — `$XDG_DATA_HOME/agentpack` if `XDG_DATA_HOME` is set, otherwise `$HOME/.local/share/agentpack`
- **Windows** — `%LOCALAPPDATA%\agentpack`

Override it by exporting the variable in your shell profile:

```sh
export AGENTPACK_HOME="$HOME/.local/share/agentpack"
```

A custom `AGENTPACK_HOME` is useful for a shared cache on a network mount, or for isolating a project's state. See [Environment Variables](../reference/env-vars.md) for the complete list.
