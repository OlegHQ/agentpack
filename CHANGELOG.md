# Changelog

All notable changes to `agentpack` are documented here. The format is based on
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/). `agentpack` is pre-release: see the
"Pre-release" note in `AGENTS.md` — breaking changes may land between versions without a migration
window.

## [0.3.3]

### Security
- **Archive path-traversal guard.** Tarball extraction now rejects any entry whose path contains
  `..`, an absolute root, or a drive prefix instead of joining it onto the destination, so a
  hand-crafted ("zip slip") archive can no longer write outside the content-addressed cache. GitHub
  git trees can't produce such entries, but agentpack extracts untrusted third-party archives, so
  the check is enforced regardless (`src/github/extract.rs`).

### Fixed
- **`add` with the canonical module-id form.** `agentpack add github.com/<owner>/<repo>/<path>` (the
  exact shape shown in docs, the manifest, and `pack.lock`) previously treated `github.com` as the
  owner and 404'd. The leading host segment is now stripped, so both that form and the bare
  `<owner>/<repo>/<path>` form resolve.
- **Case-sensitive in-repo paths.** `ModuleId::parse` lowercased the whole id, breaking `lock`/`sync`
  for any dependency whose in-repo path had uppercase letters (e.g. `.../PDF-Tools`). Only the
  `github.com/<owner>/<repo>` prefix is lowercased now; the path is preserved verbatim.
- **`add <owner>/<repo>/<path>@<ref>`.** A shorthand `@ref` was silently folded into the path
  segment (wrong fetch). It is now parsed, used to fetch the requested revision, and persisted into
  `agentpack.toml` so `lock`/`sync` re-resolve the same pin.
- **`lock --update` / `sync --update-lock` now bypass the GitHub metadata cache.** Floating pins no
  longer fail to advance within the ref/tag freshness window; the cached value is still used as a
  stale fallback when the network is unavailable.
- **Claude MCP servers marked `disabled` are dropped** from the staged `.mcp.json` (Claude's schema
  has no `disabled` field, so they were being launched anyway).
- **MCP pre-approval no longer depends on attribution.** With `AGENTPACK_KEEP_ATTRIBUTION=1`, the
  `enabledMcpjsonServers` allowlist is still written (the `--settings` overlay is created on demand),
  so Claude no longer drops staged MCP servers as untrusted.
- **A malformed markdown frontmatter in a pack no longer aborts `sync`.** The offending file is
  logged and skipped instead of failing staging for every harness.
- **dot-agents `agents/` and `commands/` now reach Codex.** They are rendered as Codex skills via the
  artifact pipeline (Codex only reads `skills/`), matching the documented behavior; the Claude bundle
  continues to receive them natively.
- **Frontmatter with a leading UTF-8 BOM** is parsed instead of being treated as body.
- **Atomic write for the shared Codex auth file** (`$AGENTPACK_HOME/shared/codex/auth.json`) — write
  to a per-process temp file then rename, avoiding a torn read when two launches materialize it
  concurrently.
- **`mcp remove <name>`** now errors when no such server exists instead of reporting a false success.
- **`mcp add --args`** accepts values that start with `-` (e.g. `--args -y pkg`) without requiring
  `--args=-y`.
- **Ambiguous single-segment `remove`** now errors and asks for a fuller `owner/repo/path` instead of
  removing an arbitrary match.
- **`pack.lock` is validated on load** — an unsupported `lockfile-version` is rejected with a clear
  message instead of being silently accepted.
- **Monorepo workspace overlays.** The launch fast-path digest now includes the resolved workspace
  directory for Cursor (`agent`) and Antigravity (`agy`), so `cd`-ing to a sibling subdirectory
  re-creates the `.cursor/agents` / `.agents/plugins/agentpack-bundle` overlay instead of skipping it.
- Mode TUI: neutral `package:` rows now render the correct base-derived glyph.
- Cursor (Linux): an empty `XDG_CONFIG_HOME` / `XDG_DATA_HOME` no longer mislocates the staged
  Electron-profile symlinks.

### Changed
- Internal: centralized the `github.com` host and default `HEAD` ref as `GITHUB_HOST` /
  `DEFAULT_GIT_REF` constants instead of scattered string literals.
- Docs: harness notes for Grok and Antigravity re-verified against `grok 0.2.14` and `agy 1.0.3`
  (assumptions unchanged; launcher behavior is identical).
