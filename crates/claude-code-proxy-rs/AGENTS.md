# AGENTS.md

This repository is a Rust port of the Codex/OpenAI path from
`reference/claude-code-proxy`.

## Project Rules

- Build a pure Rust library first. Do not add a CLI, HTTP server, or daemon unless
  the user explicitly asks for that runtime layer.
- Treat `reference/claude-code-proxy/src/providers/codex` and shared
  `anthropic`, `sse`, and `providers/translate` helpers as the source of truth.
- Prefer compatibility over local taste. If the TypeScript reference has defined
  behavior, port that behavior before improving it.
- Keep modules provider-neutral where practical: Anthropic schemas, SSE parsing,
  auth lifecycle, and provider traits should not depend on a server framework.
- Any conversion change must add or update a golden fixture that compares
  TypeScript reference output with Rust output.

## Required Checks

Run these before considering work complete:

```sh
cargo fmt --check
cargo clippy --all-targets --all-features -- -D warnings
cargo test --all-features
```

The golden tests must not call live OpenAI or ChatGPT services.
