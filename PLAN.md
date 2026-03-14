# Plan: Install Skill from GitHub URL

## Context
AgentPack needs its core feature: given a GitHub URL like `https://github.com/anthropics/skills/blob/main/skills/frontend-design/SKILL.md`, fetch the entire skill folder, cache it locally, track it in an embedded DB, and install it to `.claude/skills/`. This is the foundation everything else builds on.

## Module Architecture

```
src/
  main.rs                  # mod declarations + clap dispatch
  cli.rs                   # CLI subcommand definitions
  error.rs                 # thiserror error enum
  github/
    mod.rs                 # re-exports
    url_parser.rs          # Parse GitHub URLs → structured data
    fetcher.rs             # GitHub API client (Git Trees API)
  cache.rs                 # ~/.agentpack/cache/ management
  db.rs                    # JSON DB wrapper for tracking
  skill.rs                 # SkillFile struct, detection logic
  installers/
    mod.rs                 # Installer trait definition
    claude.rs              # Claude Code installer (copy to .claude/skills/)
```

## New Dependencies

| Crate | Purpose |
|-------|---------|
| `thiserror` | Module-level error types |
| `anyhow` | CLI error propagation |
| `reqwest` (blocking, json features) | GitHub API calls |
| `serde` + `serde_json` (derive) | JSON parsing |
| `url` | URL parsing |
| `dirs` | Resolve `~/.agentpack/` |
| `tempfile` (dev-dep) | Tests with temp directories |

**Decision: use `reqwest::blocking`** for MVP — avoids async coloring the whole codebase.

## Build Steps (in order)

### Step 1: Scaffolding
- [ ] Add all dependencies to `Cargo.toml`
- [ ] Change edition to `"2021"`
- [ ] Create empty module files with `mod` declarations in `main.rs`
- [ ] Verify: `cargo check` passes

### Step 2: Error types (`error.rs`)
- [ ] Define `AgentpackError` enum with `thiserror`: variants for UrlParse, GitHubApi, Cache, Database, Install, Io
- [ ] Verify: `cargo check`

### Step 3: URL Parser (`github/url_parser.rs`)
- [ ] Struct `GitHubSkillUrl { owner, repo, branch, skill_path, skill_name }`
- [ ] `parse(url: &str) -> Result<GitHubSkillUrl>` handling: blob URLs, tree URLs, raw URLs, repo root
- [ ] Skill name = last component of skill_path (strip SKILL.md if present)
- [ ] Verify: unit tests for all URL formats, edge cases

### Step 4: GitHub Fetcher (`github/fetcher.rs`)
- [ ] Serde structs for GitHub Git Trees API response
- [ ] `GitHubFetcher` with `reqwest::blocking::Client`
- [ ] `fetch_skill_tree(url: &GitHubSkillUrl) -> Result<Vec<SkillFile>>` — one Trees API call with `recursive=1`, filter by skill_path prefix, then download each blob via raw.githubusercontent.com
- [ ] Verify: `#[ignore]` integration test hitting real GitHub

### Step 5: Cache Manager (`cache.rs`)
- [ ] Cache at `~/.agentpack/cache/{owner}/{repo}/{branch}/{skill_path}/`
- [ ] `CacheManager` with methods: `is_cached`, `store`, `get_cached_files`
- [ ] Verify: unit tests with `tempfile`

### Step 6: DB Tracking (`db.rs`)
- [ ] `SkillRecord` document: name, source_url, owner, repo, branch, skill_path, cached_at, cache_location, installed_locations
- [ ] `AgentpackDb` wrapper: `open`, `record_cached`, `record_installed`, `find_by_url`, `list_installed`
- [ ] Uses a JSON file at `~/.agentpack/db.json`
- [ ] Verify: unit tests

### Step 7: Installer (`installers/`)
- [ ] Trait `Installer` with `install_skill`, `uninstall_skill`, `skill_is_installed`
- [ ] `ClaudeInstaller` — copies files to `{project}/.claude/skills/{name}/`
- [ ] **Copy, not symlink** (more robust, git-friendly)
- [ ] Verify: unit tests with `tempfile`

### Step 8: CLI Wiring (`cli.rs` + `main.rs`)
- [ ] Clap derive: `Install { url, project_dir }`, `List`
- [ ] Install flow: parse URL → check cache → fetch if needed → store in cache → record in DB → install → update DB → print success
- [ ] Verify: manual end-to-end test with real URL

## Key Design Decisions
- **Blocking HTTP** for MVP simplicity (async migration later for TUI)
- **Copy over symlink** for installs (portable, works if cache cleaned)
- **Git Trees API `recursive=1`** — one call gets full repo tree, filter client-side
- **Cache keyed by owner/repo/branch/path** — deterministic, human-readable
- **JSON file for DB** — simple, no extra dependencies, swap for redb later if needed

## Verification
1. `cargo check` passes after each step
2. `cargo test` passes unit tests per module
3. End-to-end: `cargo run -- install https://github.com/anthropics/skills/blob/main/skills/frontend-design/SKILL.md` successfully installs to `.claude/skills/frontend-design/`
4. Re-run same command — hits cache instead of re-downloading
5. `cargo run -- list` shows the installed skill
