# Installation

agentpack ships as a single binary. The recommended path is Homebrew; everything else builds from source with a Rust toolchain.

## Homebrew (macOS and Linux)

```sh
brew install OlegHQ/tap/agentpack
```

That one line taps `OlegHQ/homebrew-tap` and installs the formula. The two-step form works too:

```sh
brew tap OlegHQ/tap
brew install agentpack
```

Upgrade later with:

```sh
brew upgrade agentpack
```

## Prebuilt binaries and the shell installer

Each release publishes prebuilt binaries for macOS (arm64, x86_64), Linux (x86_64, arm64), and Windows (x86_64), built by [cargo-dist](https://github.com/axodotdev/cargo-dist). The release page carries a `curl | sh` installer:

```sh
curl --proto '=https' --tlsv1.2 -LsSf \
  https://github.com/OlegHQ/agentpack/releases/latest/download/agentpack-installer.sh | sh
```

Pick a specific version from the [releases page](https://github.com/OlegHQ/agentpack/releases) if you don't want `latest`.

## From source

You need a Rust toolchain (edition 2021). Install [`rustup`](https://rustup.rs) if you don't have one, then:

```sh
git clone https://github.com/OlegHQ/agentpack.git
cd agentpack
cargo install --path .
```

This drops the `agentpack` binary in `~/.cargo/bin/`; make sure that directory is on your `PATH`. The repo's `Makefile` also offers `make install`, which builds in release mode and copies the binary to `~/.local/bin` (override with `make install INSTALL_DIR=/usr/local/bin`).

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
