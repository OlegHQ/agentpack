# Go port parity ledger

This ledger is the cutover gate for the 1:1 Go port. A subsystem is complete
only when its Go unit tests pass, the relevant legacy integration cases pass
against the Go binary, and observable files/process behavior matches the Rust
oracle. Rust remains in the tree until every row is proven.

| Surface | Rust source of truth | Go target | Evidence required | Status |
|---|---|---|---|---|
| CLI parsing and dispatch | `src/cli`, `src/main.rs` | `cmd/agentpack`, `internal/cli` | help snapshots, command integration tests, exit/error parity | pending |
| Paths and user data layout | `src/paths.rs`, `src/slug.rs` | `internal/paths`, `internal/slug` | unit and cross-platform CI | in progress |
| Manifest editing | `src/manifest.rs` | `internal/manifest` | lossless fixture round trips and mode/MCP mutation tests | pending |
| Lockfile v2 | `src/lockfile.rs` | `internal/lockfile` | strict schema, sort, legacy rejection tests | pending |
| Resolution | `src/resolve` | `internal/resolve` | constraints, transitive graph, conflict and update tests | pending |
| GitHub acquisition | `src/github` | `internal/github` | URL/ref/tag/tar extraction tests and network integration | pending |
| Cache/index | `src/cache` | `internal/cache` | aliases, restoration, marketplace normalization, integrity tests | pending |
| Modes and TUI | `src/mode` | `internal/mode` | selector/filter tests plus terminal interaction coverage | pending |
| Artifact conversion | `src/artifacts` | `internal/artifacts` | exact rendered fixture parity for every harness | pending |
| Hooks | `src/hooks` | `internal/hooks` | parse/render/runtime event matrix and process/HTTP tests | pending |
| Shared staging | `src/staging` | `internal/staging` | overlay precedence, collision, guidance, MCP and verification tests | pending |
| Claude harness | `src/harness/claude` | `internal/harness/claude` | stage/verify/launch parity | pending |
| OpenCode harness | `src/harness/opencode` | `internal/harness/opencode` | stage/verify/launch parity | pending |
| Codex harness | `src/harness/codex` | `internal/harness/codex` | auth, MCP OAuth, history, hooks, stage/verify/launch parity | pending |
| Cursor harness | `src/harness/cursor` | `internal/harness/cursor` | fake home, overlay manifests, hooks, launch parity | pending |
| Grok harness | `src/harness/grok` | `internal/harness/grok` | auth/history/config/stage/verify/launch parity | pending |
| Antigravity harness | `src/harness/agy` | `internal/harness/agy` | overlay/stage/verify/launch parity | pending |
| Fast launch sync | `src/sync` | `internal/sync` | digest and integrity fast-path integration tests | pending |
| Claude proxy | `src/proxy`, `crates/claude-code-proxy-rs` | `internal/proxy` | golden request/SSE/tool/WebSocket/auth diagnostics tests | pending |
| Documentation/guidance | `README.md`, `docs`, `AGENTS.md`, `CLAUDE.md` | same | repository-wide Rust terminology audit | pending |
| Local build tooling | `Makefile`, `xtask` | `Makefile`, Go tooling | timed warm/cold fmt/vet/test/build gates | pending |
| CI | `.github/workflows/ci.yml` | Go CI | Linux/macOS/Windows green with cache and timing output | pending |
| Release/Homebrew | cargo-dist workflow/config | GoReleaser workflow/config | dry-run archives/checksums/formula plus tagged release | pending |

## Final removal gate

Removal of `Cargo.toml`, `Cargo.lock`, `src/**/*.rs`, `crates/`, `xtask/`,
`dist-workspace.toml`, cargo-dist configuration, and Rust-only guidance is the
last migration change. It requires all rows above to be complete, full Go CI
green, release packaging validated on every supported target, and a clean
repository search for obsolete Rust commands and paths.
