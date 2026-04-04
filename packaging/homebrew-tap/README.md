# homebrew-agentpack

Homebrew tap for [**agentpack**](https://github.com/OlegHQ/agentpack): pin GitHub-hosted skills and plugin directories for Claude, Cursor, OpenCode, Codex, and related agent CLIs.

## Install

```bash
brew tap OlegHQ/agentpack
brew install agentpack
brew upgrade agentpack
```

The binary builds from source and requires Xcode CLI tools / a Rust toolchain via Homebrew’s `rust` formula.

## Maintenance

This repository is updated when new **git tags** `v*` are published on `OlegHQ/agentpack`. Maintainers run `make brew-sync` from the agentpack checkout (see upstream README).
