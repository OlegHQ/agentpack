# Installation

## Homebrew (macOS and Linux)

The easiest way to install agentpack on macOS or Linux is via Homebrew:

```sh
brew tap OlegHQ/agentpack
brew install agentpack
```

To upgrade later:

```sh
brew upgrade agentpack
```

## From crates.io

If you have a Rust toolchain installed (`rustup`), install the published crate:

```sh
cargo install agentpack
```

## From Source

Clone the repository and build with Cargo:

```sh
git clone https://github.com/OlegHQ/agentpack.git
cd agentpack
cargo install --path .
```

This places the `agentpack` binary in `~/.cargo/bin/`. Make sure that directory is on your `PATH`.

## Verify the Installation

```sh
agentpack --version
```

You should see the version string printed to stdout.

## Setting AGENTPACK_HOME

By default agentpack stores its cache and state under `~/.agentpack`. You can override this by setting `AGENTPACK_HOME` in your shell profile:

```sh
export AGENTPACK_HOME="$HOME/.agentpack"
```

See [Environment Variables](../reference/env-vars.md) for the full list of supported variables.
