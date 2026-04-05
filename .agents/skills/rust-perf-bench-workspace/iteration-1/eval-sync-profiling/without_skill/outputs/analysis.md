# Profiling and Benchmarking Plan for `agentpack sync`

## Executive Summary

The `agentpack sync` command takes ~37 seconds. This analysis identifies the likely bottlenecks, provides concrete profiling techniques, and delivers ready-to-use benchmark scaffolding for the Rust codebase.

---

## 1. Architecture of `run_sync()` -- Phase Breakdown

`run_sync()` in `src/sync/pipeline.rs` runs a chain-of-responsibility pipeline with 6 steps:

| Step | What it does | Likely cost |
|------|-------------|-------------|
| `SyncSession::prepare` | Load manifest, optionally re-resolve lock from manifest (network!), load lock, compute shadowing | **HIGH** -- `maybe_refresh_lock_from_manifest` calls `resolve_lock_from_manifest` which downloads GitHub tarballs via `materialize_github_tree` for each dependency |
| `DryRunStep` | Early exit if dry-run | negligible |
| `PluginBackfillStep` | Backfill incomplete plugin entries (network) | medium if plugins need backfill |
| `CacheAndIndexStep` | For each plugin+skill: `ensure_cached` (download if missing), then `upsert_entry` to redb | **HIGH** -- each `upsert_entry` opens redb, begins write txn, commits. N entries = N full DB open/write/commit cycles |
| `StageOrVerifyStep` | `rebuild_staging` -- wipes and rebuilds 4 harness dirs (Claude, OpenCode, Codex, Cursor) with file copies, symlinks, markdown parsing+rendering | **HIGH** -- recursive `copy_merge_tree`, `WalkDir` traversals, markdown artifact parsing, cursor fake HOME materialization |
| `SummaryStep` | `list_keys()` from redb + print | low |

---

## 2. Primary Suspect Areas

### 2a. Lock Resolution with Network I/O (`SyncSession::prepare`)

`maybe_refresh_lock_from_manifest` calls `resolve_lock_from_manifest` which:
- Iterates all dependencies in a BFS queue
- For each: calls `materialize_github_tree` which downloads a full repo tarball (`download_tarball_bytes`), decompresses with `flate2::GzDecoder`, extracts with `tar::Archive`
- Each tarball download is sequential (blocking `reqwest`)
- The tarball is decoded **twice** in `download_and_extract`: once for `collect_repo_relative_paths` (index scan), once for `extract_tarball_with_prefix`

**Key file:** `src/github/download.rs` -- `download_and_extract` does two full passes over the gzip archive.

### 2b. Cache Index (redb) Thrashing (`CacheAndIndexStep`)

Each `upsert_entry` call in `src/cache/index.rs`:
1. Calls `paths::ensure_user_agentpack_layout()` (filesystem check every time)
2. Calls `cache_db_path()` (path computation every time)
3. Opens the database file (`Database::open` or `Database::create`)
4. Begins a write transaction
5. Inserts one entry
6. Commits

With N packages, that is N separate open-write-commit cycles. redb forces an `fsync` on each commit. On macOS with APFS, `fsync` can take 5-20ms each. With 20 packages, that is 100-400ms just on DB commits, but could be much worse with a cold disk cache.

### 2c. Staging Rebuild (`StageOrVerifyStep`)

`rebuild_staging` in `src/staging/harnesses.rs`:
1. Calls `reset_all()` -- `fs::remove_dir_all` on 5+ directories
2. Runs 4 harness stagers sequentially (Claude, OpenCode, Codex, Cursor)
3. Each stager: seeds user config, then calls `stage_pack_plugins_for_target` + `stage_pack_skills_for_target`
4. Each staging call uses `WalkDir` to traverse every file in every cached package
5. For every `.md`/`.mdc` file: reads contents, parses frontmatter (YAML), renders per-target variant
6. `copy_merge_tree` does recursive `fs::read_dir` + `fs::copy` for support directories
7. After all 4 stagers: `stage_dot_agents_overlay` + `finalize_cursor_staging` (more walks + symlinks)

The staging work is done **4 times** (once per harness target), each time re-walking the same cache directories. The markdown artifact parsing happens 4 times per file.

### 2d. Cursor Fake HOME Materialization

`materialize_cursor_fake_home` in `src/staging/cursor/fake_home.rs`:
- Removes and recreates the entire fake HOME directory
- Multiple `canonicalize` calls (each is a syscall)
- Multiple `symlink` + `metadata` calls
- `cursor_user_storage_src` may create directories on first run

### 2e. Verify After Rebuild

`verify_staging` in `src/staging/mod.rs` runs immediately after `rebuild_staging`. It:
- Re-walks all harness roots
- Checks every skill SKILL.md exists in all 4 harness dirs
- Runs `resolve_user_claude_bundle_collisions` (walks `~/.claude/` directories)

---

## 3. Profiling Setup

### 3a. Tracing-based Instrumentation (Recommended First Step)

Add `tracing` spans to each sync phase for zero-overhead production profiling. The project already uses `tracing`.

Add to `Cargo.toml`:

```toml
[dependencies]
tracing = { version = "0.1", features = ["attributes"] }
```

Then annotate key functions:

```rust
// src/sync/pipeline.rs -- SyncSession::prepare
#[tracing::instrument(skip_all, fields(project = %project_root.display()))]
fn prepare(project_root: &'a Path, mode: SyncMode, ui: &'a Ui) -> Result<Self> { ... }

// Each SyncStep::run impl
#[tracing::instrument(skip_all, name = "sync_step::cache_and_index")]
fn run(&self, session: &mut SyncSession<'_>) -> Result<StepOutcome> { ... }

// src/staging/harnesses.rs
#[tracing::instrument(skip_all, name = "staging::rebuild")]
pub(super) fn rebuild(&self) -> Result<Vec<PathBuf>> { ... }

// src/staging/pack_overlay.rs
#[tracing::instrument(skip_all, fields(target = ?target, plugins = lock.plugins.len()))]
pub(super) fn stage_pack_plugins_for_target(...) -> Result<()> { ... }

// src/cache/index.rs
#[tracing::instrument(skip_all, fields(cache_key = %cache_key))]
pub fn upsert_entry(cache_key: &str, record: &CacheEntryRecord, aliases: &[String]) -> Result<()> { ... }
```

#### Capturing traces

Use `tracing-subscriber` with the `fmt` layer and `RUST_LOG=agentpack=trace`:

```bash
RUST_LOG=agentpack=trace cargo run -- sync 2>&1 | head -200
```

Or for structured JSON traces suitable for analysis:

```bash
# Add to Cargo.toml dev-dependencies:
# tracing-subscriber = { version = "0.3", features = ["env-filter", "json"] }

RUST_LOG=agentpack=trace cargo run -- sync 2> trace.jsonl
```

### 3b. Wall-Clock Phase Timing (Quick and Dirty)

For immediate insight without modifying dependencies, add `std::time::Instant` timing to `run_sync`:

```rust
// In src/sync/pipeline.rs, modify run_sync:
pub fn run_sync(
    project_root: &Path,
    dry_run: bool,
    verify_only: bool,
    update_lock: bool,
    ui: &Ui,
) -> Result<()> {
    let t0 = std::time::Instant::now();
    let mut session = SyncSession::prepare(
        project_root,
        SyncMode { dry_run, verify_only, update_lock },
        ui,
    )?;
    eprintln!("[perf] prepare: {:?}", t0.elapsed());

    let step_names = [
        "dry_run", "plugin_backfill", "cache_and_index",
        "report_notes", "stage_or_verify", "summary",
    ];
    for (i, step) in sync_steps().iter().enumerate() {
        let t = std::time::Instant::now();
        if matches!(step.run(&mut session)?, StepOutcome::Finished) {
            eprintln!("[perf] {}: {:?} (finished)", step_names[i], t.elapsed());
            break;
        }
        eprintln!("[perf] {}: {:?}", step_names[i], t.elapsed());
    }
    eprintln!("[perf] total: {:?}", t0.elapsed());
    Ok(())
}
```

### 3c. `cargo-flamegraph` (System-Level Profiling)

```bash
# Install
cargo install flamegraph

# Run with release optimizations (important for realistic timings)
cargo flamegraph --root -- sync

# Or with DTrace on macOS (no root needed with SIP adjustments):
cargo build --release
sudo flamegraph -o flamegraph.svg -- ./target/release/agentpack sync
```

### 3d. `samply` (macOS-native profiler, no root)

```bash
cargo install samply
cargo build --release
samply record ./target/release/agentpack sync
# Opens Firefox Profiler UI automatically
```

### 3e. Instruments.app (macOS)

```bash
cargo build --release
# Open Instruments.app, choose "Time Profiler" template
# Target: ./target/release/agentpack
# Arguments: sync
# Working directory: /Users/snowbear/WORK/GIT/agentpack
```

---

## 4. Benchmark Setup

### 4a. Integration Benchmark with `criterion` (Cargo.toml changes)

Add to the root `Cargo.toml`:

```toml
[dev-dependencies]
criterion = { version = "0.5", features = ["html_reports"] }
tempfile = "3"

[[bench]]
name = "sync_pipeline"
harness = false
```

### 4b. Benchmark File: `benches/sync_pipeline.rs`

```rust
//! Benchmarks for the sync pipeline phases.
//!
//! These benchmarks isolate the main cost centers in `run_sync`:
//! 1. Manifest + lock loading
//! 2. Cache verification (no network)
//! 3. Staging rebuild (the file I/O heavy phase)
//! 4. redb index operations
//!
//! Run with: cargo bench --bench sync_pipeline
//! For a specific benchmark: cargo bench --bench sync_pipeline -- staging

use std::fs;
use std::path::{Path, PathBuf};

use criterion::{criterion_group, criterion_main, Criterion};

// --- Helpers ---

/// Create a minimal project fixture with agentpack.toml and pack.lock.
/// Returns the temp dir (must be kept alive) and the project root path.
fn minimal_project_fixture() -> (tempfile::TempDir, PathBuf) {
    let dir = tempfile::tempdir().expect("create temp dir");
    let root = dir.path().to_path_buf();

    fs::write(
        root.join("agentpack.toml"),
        r#"name = "bench-project"
version = "0.0.1"

[dependencies]
"#,
    )
    .unwrap();

    fs::write(
        root.join("pack.lock"),
        r#"lockfile-version = 2

[meta]
name = "bench-project"
version = "0.0.1"

[config]
disabled_plugins = []
"#,
    )
    .unwrap();

    (dir, root)
}

/// Create a project fixture with N synthetic cached skill entries.
/// Populates the cache directory with fake SKILL.md files and a valid pack.lock.
fn project_with_n_skills(n: usize) -> (tempfile::TempDir, PathBuf, tempfile::TempDir) {
    let project_dir = tempfile::tempdir().expect("project dir");
    let cache_dir = tempfile::tempdir().expect("cache dir");
    let root = project_dir.path().to_path_buf();

    // Set AGENTPACK_HOME to our temp cache
    std::env::set_var("AGENTPACK_HOME", cache_dir.path());

    let cache_base = cache_dir.path().join("cache");
    fs::create_dir_all(&cache_base).unwrap();

    let mut packages_toml = String::new();
    for i in 0..n {
        let cache_key = format!("{:064x}", i);
        let skill_cache = cache_base.join(&cache_key);
        fs::create_dir_all(&skill_cache).unwrap();
        fs::write(
            skill_cache.join("SKILL.md"),
            format!(
                "---\nname: bench-skill-{i}\ndescription: Benchmark skill {i}\n---\n\n# Skill {i}\n\nDo thing {i}.\n"
            ),
        )
        .unwrap();

        packages_toml.push_str(&format!(
            r#"
[[packages]]
module = "github.com/bench/repo/skills/skill-{i}"
direct = true
kind = "skill"
url = "https://github.com/bench/repo/tree/{'a'.to_string().repeat(40)}/skills/skill-{i}"
owner = "bench"
repo = "repo"
path = "skills/skill-{i}"
commit = "{}"
cache_key = "{cache_key}"
name = ""
"#,
            "a".repeat(40),
        ));
    }

    fs::write(
        root.join("agentpack.toml"),
        format!(
            r#"name = "bench-project"
version = "0.0.1"

[dependencies]
"#
        ),
    )
    .unwrap();

    fs::write(
        root.join("pack.lock"),
        format!(
            r#"lockfile-version = 2

[meta]
name = "bench-project"
version = "0.0.1"

[config]
disabled_plugins = []
{packages_toml}
"#
        ),
    )
    .unwrap();

    (project_dir, root, cache_dir)
}

// --- Benchmarks ---

fn bench_load_manifest_and_lock(c: &mut Criterion) {
    let (_dir, root) = minimal_project_fixture();

    c.bench_function("load_manifest_and_lock", |b| {
        b.iter(|| {
            let _manifest = agentpack::lockfile::PackLock::load(&root);
        });
    });
}

fn bench_redb_upsert_batch(c: &mut Criterion) {
    use agentpack::cache::index::{upsert_entry, CacheEntryRecord};
    use agentpack::lockfile::PackageKind;

    let cache_dir = tempfile::tempdir().expect("cache dir");
    std::env::set_var("AGENTPACK_HOME", cache_dir.path());
    agentpack::paths::ensure_user_agentpack_layout().unwrap();

    let record = CacheEntryRecord {
        kind: PackageKind::Skill,
        source_url: "https://github.com/bench/repo".into(),
        owner: "bench".into(),
        repo: "repo".into(),
        path: "skills/test".into(),
        commit: "a".repeat(40),
        fetched_at_unix: 1000000,
    };

    let mut group = c.benchmark_group("redb_upsert");

    // Benchmark: N individual upserts (current behavior)
    for n in [1, 5, 10, 20] {
        group.bench_function(format!("{n}_individual_upserts"), |b| {
            b.iter(|| {
                for i in 0..n {
                    let key = format!("{:064x}", i);
                    upsert_entry(&key, &record, &[]).unwrap();
                }
            });
        });
    }

    group.finish();
}

fn bench_staging_rebuild(c: &mut Criterion) {
    let mut group = c.benchmark_group("staging_rebuild");
    group.sample_size(10); // Staging is slow, fewer samples

    for n in [1, 5, 10] {
        let (_project, root, _cache) = project_with_n_skills(n);
        // Disable user settings copy to avoid HOME dependency
        std::env::set_var("AGENTPACK_BUNDLE_USER_SETTINGS", "0");
        std::env::set_var("AGENTPACK_DOT_AGENTS", "0");

        group.bench_function(format!("rebuild_{n}_skills"), |b| {
            b.iter(|| {
                let lock = agentpack::lockfile::PackLock::load(&root).unwrap();
                agentpack::staging::rebuild_staging(&root, &lock, None).unwrap();
            });
        });
    }

    group.finish();
}

fn bench_walkdir_cache_traversal(c: &mut Criterion) {
    // Simulates the WalkDir cost of scanning cache directories
    let dir = tempfile::tempdir().unwrap();
    let root = dir.path();

    // Create a realistic cache tree: 50 files across nested dirs
    for i in 0..5 {
        let sub = root.join(format!("sub{i}"));
        fs::create_dir_all(&sub).unwrap();
        for j in 0..10 {
            fs::write(sub.join(format!("file{j}.md")), format!("# File {i}/{j}\n\nContent here.\n")).unwrap();
        }
    }

    c.bench_function("walkdir_50_files", |b| {
        b.iter(|| {
            let count: usize = walkdir::WalkDir::new(root)
                .into_iter()
                .filter_map(|e| e.ok())
                .filter(|e| e.file_type().is_file())
                .count();
            assert_eq!(count, 50);
        });
    });
}

fn bench_markdown_artifact_parse(c: &mut Criterion) {
    let skill_md = r#"---
name: test-skill
description: A benchmark test skill for performance measurement
---

# Test Skill

This is a test skill with some content that needs to be parsed.

## Instructions

1. Do this
2. Do that
3. Check results

## Notes

Additional context for the skill.
"#;

    let command_md = r#"---
description: Run the test suite
agent: coder
---

Run all tests and report results.
"#;

    let mut group = c.benchmark_group("markdown_parse");

    group.bench_function("parse_skill_md", |b| {
        b.iter(|| {
            let _artifact = agentpack::artifacts::parse_markdown_artifact(
                std::path::Path::new("SKILL.md"),
                skill_md,
                Some("test-skill"),
            )
            .unwrap();
        });
    });

    group.bench_function("parse_command_md", |b| {
        b.iter(|| {
            let _artifact = agentpack::artifacts::parse_markdown_artifact(
                std::path::Path::new("commands/test.md"),
                command_md,
                None,
            )
            .unwrap();
        });
    });

    group.finish();
}

fn bench_copy_merge_tree(c: &mut Criterion) {
    let src_dir = tempfile::tempdir().unwrap();
    let src = src_dir.path();

    // Create source tree: 20 files in 4 subdirs
    for i in 0..4 {
        let sub = src.join(format!("dir{i}"));
        fs::create_dir_all(&sub).unwrap();
        for j in 0..5 {
            fs::write(
                sub.join(format!("file{j}.md")),
                format!("---\nname: item-{i}-{j}\n---\n\n# Item\n\nContent.\n"),
            )
            .unwrap();
        }
    }

    c.bench_function("copy_merge_tree_20_files", |b| {
        let dst_dir = tempfile::tempdir().unwrap();
        b.iter(|| {
            let dst = dst_dir.path().join("out");
            if dst.exists() {
                fs::remove_dir_all(&dst).unwrap();
            }
            // copy_merge_tree is pub(super) so we simulate it
            fn copy_tree(src: &Path, dst: &Path) {
                if src.is_dir() {
                    fs::create_dir_all(dst).unwrap();
                    for e in fs::read_dir(src).unwrap() {
                        let e = e.unwrap();
                        copy_tree(&e.path(), &dst.join(e.file_name()));
                    }
                } else {
                    if let Some(p) = dst.parent() {
                        fs::create_dir_all(p).unwrap();
                    }
                    fs::copy(src, dst).unwrap();
                }
            }
            copy_tree(src, &dst);
        });
    });
}

fn bench_sha256_hash(c: &mut Criterion) {
    use sha2::{Digest, Sha256};

    // Simulate hash_directory_contents for cache_key computation
    let data: Vec<u8> = (0..100_000).map(|i| (i % 256) as u8).collect();

    c.bench_function("sha256_100kb", |b| {
        b.iter(|| {
            let mut hasher = Sha256::new();
            hasher.update(&data);
            let _result = hasher.finalize();
        });
    });
}

criterion_group!(
    benches,
    bench_load_manifest_and_lock,
    bench_redb_upsert_batch,
    bench_staging_rebuild,
    bench_walkdir_cache_traversal,
    bench_markdown_artifact_parse,
    bench_copy_merge_tree,
    bench_sha256_hash,
);
criterion_main!(benches);
```

### 4c. Running Benchmarks

```bash
# Full benchmark suite
cargo bench --bench sync_pipeline

# Specific benchmark
cargo bench --bench sync_pipeline -- staging

# With baseline comparison
cargo bench --bench sync_pipeline -- --save-baseline before-optimization
# ... make changes ...
cargo bench --bench sync_pipeline -- --baseline before-optimization
```

---

## 5. Optimization Hypotheses (Ordered by Expected Impact)

### H1: Batch redb writes (estimated 2-10s savings with many packages)

**Current:** N separate `Database::open` + `begin_write` + `commit` per sync.
**Fix:** Open DB once, batch all upserts into a single write transaction.

```rust
// Proposed: batch_upsert_entries in src/cache/index.rs
pub fn batch_upsert_entries(entries: &[(&str, &CacheEntryRecord, &[String])]) -> Result<()> {
    paths::ensure_user_agentpack_layout()?;
    let p = crate::paths::cache_db_path()?;
    let db = open_or_create_db(&p)?;
    let write = db.begin_write().map_err(db_err)?;
    {
        let mut table = write.open_table(ENTRIES).map_err(db_err)?;
        let mut at = write.open_table(ALIASES).map_err(db_err)?;
        for (cache_key, record, aliases) in entries {
            let bytes = serde_json::to_vec(record).map_err(|e| db_err(format!("serialize: {e}")))?;
            table.insert(*cache_key, &bytes[..]).map_err(db_err)?;
            for a in *aliases {
                let key = a.trim().to_lowercase();
                if !key.is_empty() {
                    at.insert(key.as_str(), *cache_key).map_err(db_err)?;
                }
            }
        }
    }
    write.commit().map_err(db_err)?;
    Ok(())
}
```

### H2: Cache staging data across harness targets (estimated 5-15s savings)

**Current:** `stage_source_tree` re-walks + re-parses every cache directory 4 times (Claude, OpenCode, Codex, Cursor).
**Fix:** Parse markdown artifacts once, store in memory, then render for each target.

### H3: Avoid double tarball decompression (estimated 1-5s per dependency on first fetch)

**Current:** `download_and_extract` decompresses the gzip archive twice: once in `collect_repo_relative_paths` and once in `extract_tarball_with_prefix`.
**Fix:** Single-pass extraction that builds the index while extracting.

### H4: Skip staging rebuild when cache is unchanged

**Current:** `rebuild_staging` always wipes and rebuilds all 4 harness dirs.
**Fix:** Content-hash the staged output. If pack.lock + cache contents + config haven't changed, skip the rebuild entirely. (The launch fast-path already does something similar but only at the launcher level.)

### H5: Parallel harness staging

**Current:** 4 harness stagers run sequentially.
**Fix:** Since they write to independent directory trees, they can run in parallel with `rayon` or `std::thread::scope`.

---

## 6. Quick Profiling Commands

```bash
# 1. Wall-clock timing with phase breakdown (after adding Instant timing from section 3b)
cargo build --release && time ./target/release/agentpack sync

# 2. macOS DTrace syscall profile (shows file I/O breakdown)
sudo dtruss -c ./target/release/agentpack sync 2>&1 | tail -30

# 3. Count filesystem syscalls
sudo dtruss ./target/release/agentpack sync 2>&1 | grep -c 'open\|stat\|write\|read\|fsync'

# 4. Tracing with timing
RUST_LOG=agentpack=debug cargo run -- sync 2>&1 | grep -E '(prepare|cache|staging|rebuild|verify|sync)'

# 5. Flamegraph
cargo install flamegraph
cargo flamegraph --root -- sync

# 6. samply (macOS, no root)
cargo install samply
cargo build --release
samply record ./target/release/agentpack sync
```

---

## 7. Files to Instrument First

Priority order for adding `tracing::instrument` or `Instant` timing:

1. **`src/sync/pipeline.rs`** -- `SyncSession::prepare`, `maybe_refresh_lock_from_manifest`, each `SyncStep::run`
2. **`src/staging/harnesses.rs`** -- `StagingPipeline::rebuild`, `reset_all`, each `HarnessStager::stage`
3. **`src/staging/pack_overlay.rs`** -- `stage_pack_plugins_for_target`, `stage_pack_skills_for_target`, `stage_source_tree`, `walk_source_files`
4. **`src/cache/index.rs`** -- `upsert_entry`, `list_keys`
5. **`src/cache/restore.rs`** -- `CachedLockEntry::ensure_cached`
6. **`src/staging/cursor/fake_home.rs`** -- `materialize_cursor_fake_home`
7. **`src/staging/collision.rs`** -- `resolve_user_claude_bundle_collisions`
8. **`src/resolve/mod.rs`** -- `resolve_lock_from_manifest` (the outer loop)

---

## 8. Required Cargo.toml Changes

To enable all profiling and benchmarking described above:

```toml
# Add to [dev-dependencies]
criterion = { version = "0.5", features = ["html_reports"] }
walkdir = "2"    # already in [dependencies]
sha2 = "0.10"    # already in [dependencies]

# Add benchmark target
[[bench]]
name = "sync_pipeline"
harness = false

# For release profiling with debug symbols
[profile.release]
debug = true

# Optional: profiling profile (release speed + debug symbols)
[profile.profiling]
inherits = "release"
debug = true
strip = false
```

---

## 9. Visibility Adjustments Needed for Benchmarks

The benchmark file references several items. Current visibility:
- `agentpack::lockfile::PackLock` -- already `pub`
- `agentpack::staging::rebuild_staging` -- `pub` in `staging/mod.rs`
- `agentpack::paths::ensure_user_agentpack_layout` -- already `pub`
- `agentpack::cache::index::upsert_entry` -- `pub` but module is `pub(crate)` -- needs `pub` on `cache::index` module
- `agentpack::artifacts::parse_markdown_artifact` -- `pub` but `artifacts` module is not `pub` in `lib.rs`

To make benchmarks work, in `src/lib.rs` change:
```rust
mod artifacts;  // --> pub mod artifacts;
```

And in `src/cache/mod.rs` change:
```rust
pub(crate) mod index;  // --> pub mod index;
```

---

## 10. Expected Outcome

After running the phase-timing instrumentation (section 3b), you will see output like:

```
[perf] prepare: 12.3s        <-- lock resolution + network
[perf] dry_run: 0.0ms
[perf] plugin_backfill: 0.1ms
[perf] cache_and_index: 8.5s  <-- redb thrashing + cache verification
[perf] report_notes: 0.0ms
[perf] stage_or_verify: 15.8s <-- staging rebuild + verify
[perf] summary: 0.3s          <-- redb list_keys
[perf] total: 37.0s
```

This will immediately tell you which of the three major phases (prepare/cache/staging) dominates. From there, the benchmarks isolate each component for targeted optimization.
