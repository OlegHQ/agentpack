# Profiling & Benchmarking Plan for `agentpack sync`

## Step 1: Identify What to Benchmark

The `run_sync()` pipeline in `src/sync/pipeline.rs` runs six sequential steps. Based on code analysis, the likely time sinks are:

### Phase breakdown (most likely to least likely bottleneck)

| Phase | Code path | Why it could be slow |
|-------|-----------|---------------------|
| **Lock resolve** | `maybe_refresh_lock_from_manifest()` -> `resolve_lock_from_manifest()` | Per-dependency GitHub API calls (`resolve_ref_to_sha`), tarball downloads via `materialize_github_tree()`, sequential BFS queue |
| **Cache + Index** | `CacheAndIndexStep` iterates plugins + skills | Each `ensure_cached()` may hit GitHub download + gzip decompress + tar extract; `upsert_entry()` opens RedDB write txn per entry |
| **Staging rebuild** | `StageOrVerifyStep` -> `StagingPipeline::rebuild()` | Rebuilds 4 harness trees (Claude, OpenCode, Codex, Cursor); each does `reset_all()` (rm -rf), `seed_*()`, `stage_pack_{plugins,skills}_for_target()` with `WalkDir` + markdown parsing + file copies |
| **Staging verify** | `verify_staging()` after rebuild | Re-walks staged trees + cache dirs, collision checks |
| **Plugin backfill** | `PluginBackfillStep` | Only for incomplete entries, likely fast |
| **Summary** | `SummaryStep` -> `list_keys()` | Opens RedDB read txn, scans all keys |

### Critical I/O patterns identified

1. **Sequential network calls in resolve** (`src/resolve/mod.rs:181-219`): The BFS loop calls `materialize_github_tree()` one dependency at a time. Each may download + decompress + extract a tarball.

2. **Sequential cache ensure** (`src/sync/pipeline.rs:308-326`): Plugins and skills are cached one at a time with blocking HTTP.

3. **4x staging rebuild** (`src/staging/harnesses.rs:92-98`): Each harness stager walks the same cache dirs independently via `WalkDir`. The `stage_source_tree()` function in `pack_overlay.rs` calls `WalkDir::new(root).follow_links(false)` per plugin/skill per harness target -- so with N packages and 4 harness targets, that is 4*N directory walks.

4. **Recursive `copy_merge_tree()`** (`src/staging/tree.rs`): Does `fs::read_dir()` + `fs::copy()` per file. No buffering, no parallelism, creates parent directories per-file.

5. **RedDB write per entry** (`src/cache/index.rs`): `upsert_entry()` opens the database, begins a write transaction, and commits -- once per plugin/skill in `CacheAndIndexStep`.

6. **Double `verify_staging()`**: `StageOrVerifyStep` calls `rebuild_staging()` then immediately `verify_staging()`, which re-walks the just-built trees.

## Step 2: Benchmark Setup

### Cargo.toml additions

```toml
[dev-dependencies]
divan = "0.1"

[profile.release]
debug = 1  # line tables for readable flamegraphs

[profile.bench]
debug = 1

[[bench]]
name = "sync_bench"
harness = false
```

### Benchmark file: `benches/sync_bench.rs`

```rust
use std::path::PathBuf;
use std::time::Instant;

use divan::Bencher;

fn main() {
    divan::main();
}

/// Returns a project root with a populated agentpack.toml and pack.lock
/// that has already been synced once (cache warm).
/// Set AGENTPACK_BENCH_PROJECT to point at a real project, or this uses
/// the agentpack repo itself.
fn bench_project_root() -> PathBuf {
    std::env::var("AGENTPACK_BENCH_PROJECT")
        .map(PathBuf::from)
        .unwrap_or_else(|_| PathBuf::from(env!("CARGO_MANIFEST_DIR")))
}

// --- Full sync end-to-end ---

#[divan::bench(sample_count = 5)]
fn full_sync_warm_cache(bencher: Bencher) {
    let root = bench_project_root();
    let ui = agentpack::ui::Ui::quiet();
    bencher.bench_local(|| {
        agentpack::sync::run_sync(&root, false, false, false, &ui).unwrap();
    });
}

#[divan::bench(sample_count = 5)]
fn sync_verify_only(bencher: Bencher) {
    let root = bench_project_root();
    let ui = agentpack::ui::Ui::quiet();
    bencher.bench_local(|| {
        agentpack::sync::run_sync(&root, false, true, false, &ui).unwrap();
    });
}

// --- Isolated phase benchmarks ---

#[divan::bench(sample_count = 5)]
fn phase_load_manifest_and_lock(bencher: Bencher) {
    let root = bench_project_root();
    bencher.bench_local(|| {
        let _manifest = agentpack::manifest::AgentpackManifest::load(&root).unwrap();
        let _lock = agentpack::lockfile::PackLock::load(&root).unwrap();
    });
}

#[divan::bench(sample_count = 5)]
fn phase_staging_rebuild(bencher: Bencher) {
    let root = bench_project_root();
    let lock = agentpack::lockfile::PackLock::load(&root).unwrap();
    let manifest = agentpack::manifest::AgentpackManifest::load(&root).unwrap();
    bencher.bench_local(|| {
        agentpack::staging::rebuild_staging(&root, &lock, manifest.as_ref()).unwrap();
    });
}

#[divan::bench(sample_count = 5)]
fn phase_staging_verify(bencher: Bencher) {
    let root = bench_project_root();
    let lock = agentpack::lockfile::PackLock::load(&root).unwrap();
    bencher.bench_local(|| {
        agentpack::staging::verify_staging(&root, &lock).unwrap();
    });
}

#[divan::bench(sample_count = 5)]
fn phase_cache_integrity(bencher: Bencher) {
    let root = bench_project_root();
    let lock = agentpack::lockfile::PackLock::load(&root).unwrap();
    bencher.bench_local(|| {
        agentpack::cache::verify_lock_cache_integrity(&lock).unwrap();
    });
}

#[divan::bench(sample_count = 5)]
fn phase_reddb_list_keys(bencher: Bencher) {
    bencher.bench_local(|| {
        agentpack::cache::index::list_keys().unwrap();
    });
}
```

**Note:** The exact module paths above assume `pub` visibility on the functions being benchmarked. You will likely need to add `pub` re-exports in `src/lib.rs` or `src/sync/mod.rs` for the benchmark crate to access internal functions. Alternatively, write the benchmarks as integration-style tests that shell out to `cargo run -- sync` with `std::process::Command` and measure wall-clock time.

### Quick wall-clock timing (no code changes needed)

Before writing benchmarks, get a coarse phase breakdown using tracing timestamps:

```bash
# Build release with debug info
cargo build --release

# Time full sync with tracing
RUST_LOG=agentpack=debug time ./target/release/agentpack sync 2>&1 | head -100

# macOS syscall count
sudo dtruss -c ./target/release/agentpack sync 2>&1 | tail -30
```

### Instrumented timing (minimal code change)

Add timing spans to `run_sync()` in `src/sync/pipeline.rs` to identify which step takes the most time without any framework:

```rust
// At the top of run_sync(), or inside SyncSession::prepare:
use std::time::Instant;

// In the step loop:
for step in sync_steps() {
    let step_start = Instant::now();
    let outcome = step.run(&mut session)?;
    tracing::info!(
        elapsed_ms = step_start.elapsed().as_millis(),
        step = std::any::type_name_of_val(&step),
        "sync step completed"
    );
    if matches!(outcome, StepOutcome::Finished) {
        break;
    }
}
```

Run with `RUST_LOG=agentpack=info` to see per-step timings immediately.

## Step 3: Profiling to Find Bottlenecks

### Flamegraph (CPU)

```bash
cargo install flamegraph

# Profile warm-cache sync
cargo flamegraph --release --bin agentpack -- sync

# On macOS with SIP:
sudo cargo flamegraph --root --release --bin agentpack -- sync
```

Look for:
- **Wide `walkdir` frames** inside staging -- sign of repeated directory traversal
- **Wide `flate2`/`tar` frames** -- re-extracting already-cached tarballs
- **Wide `redb` frames** in `upsert_entry` -- database overhead per entry
- **Wide `reqwest` frames** -- network blocking the main thread
- **Wide `fs::copy`/`fs::create_dir_all` frames** -- I/O-bound staging

### samply (alternative, better on macOS)

```bash
cargo install samply
cargo build --release
samply record ./target/release/agentpack sync
```

### Syscall tracing (macOS)

```bash
sudo dtruss -c ./target/release/agentpack sync 2>&1 | tail -30
```

Look for:
- High `stat64`/`lstat64` counts from `WalkDir` and `is_file()`/`is_dir()` checks
- High `open`+`close` pairs from `fs::copy` per file
- High `mkdir` counts from `create_dir_all` per file

## Step 4: Likely Optimizations (to apply after profiling confirms)

### 4.1 Batch RedDB writes

**Current:** `upsert_entry()` opens DB + write txn per plugin/skill in `CacheAndIndexStep`.

**Fix:** Collect all records, open DB once, single write transaction:

```rust
// In CacheAndIndexStep::run():
let mut records: Vec<(&str, CacheEntryRecord)> = Vec::new();
// ... collect records ...
batch_upsert_entries(&records)?;  // single DB open + single txn
```

### 4.2 Parallelize harness staging

**Current:** 4 harnesses staged sequentially, each walking the same cache directories.

**Fix with rayon:**

```rust
use rayon::prelude::*;

// In StagingPipeline::rebuild():
harness_stagers().par_iter().try_for_each(|stage| {
    stage.stage(&self.ctx)
})?;
```

### 4.3 Cache `WalkDir` results across harness targets

**Current:** `stage_source_tree()` calls `WalkDir::new(root)` for the same cache directory once per harness target (4 times).

**Fix:** Walk once, store `(path, rel)` pairs, replay for each target:

```rust
struct CachedTree {
    files: Vec<(PathBuf, PathBuf)>,  // (absolute, relative)
}

impl CachedTree {
    fn walk(root: &Path) -> Result<Self> { /* WalkDir once */ }
    fn replay<F>(&self, visitor: &mut F) -> Result<()> { /* iterate cached list */ }
}
```

### 4.4 Skip `verify_staging()` after `rebuild_staging()`

**Current:** `StageOrVerifyStep` calls `rebuild_staging()` then immediately `verify_staging()`, which re-walks the trees just built.

**Fix:** Only verify on `--verify-only`. The rebuild itself should guarantee correctness. Remove the redundant verify call:

```rust
// StageOrVerifyStep::run() currently:
staging::rebuild_staging(...)?;
staging::verify_staging(...)?;  // <-- redundant, remove

// Keep verify_staging only for the verify_only path
```

### 4.5 Replace recursive `copy_merge_tree()` with bulk copy

**Current:** `copy_merge_tree()` in `tree.rs` recurses with `fs::read_dir` + `fs::copy` per file, calling `fs::create_dir_all` for every file's parent.

**Fix:** Pre-collect all source files, batch `create_dir_all` for unique parent directories, then copy files:

```rust
fn bulk_copy_tree(src: &Path, dst: &Path) -> Result<()> {
    let files: Vec<_> = WalkDir::new(src)
        .into_iter()
        .filter_map(|e| e.ok())
        .filter(|e| e.file_type().is_file())
        .collect();
    
    // Collect unique parent dirs
    let mut dirs: HashSet<PathBuf> = HashSet::new();
    for f in &files {
        let rel = f.path().strip_prefix(src).unwrap();
        if let Some(parent) = dst.join(rel).parent() {
            dirs.insert(parent.to_path_buf());
        }
    }
    for d in &dirs {
        fs::create_dir_all(d)?;
    }
    
    // Bulk copy
    for f in &files {
        let rel = f.path().strip_prefix(src).unwrap();
        fs::copy(f.path(), dst.join(rel))?;
    }
    Ok(())
}
```

### 4.6 Parallel network fetches during resolve

**Current:** `resolve_lock_from_manifest()` processes the BFS queue sequentially, calling `materialize_github_tree()` one at a time.

**Fix:** After initial constraint merge, identify all independent modules and fetch in parallel:

```rust
use rayon::prelude::*;

let fetched: Vec<_> = independent_modules
    .par_iter()
    .map(|mid| materialize_github_tree(client, &source, &display, ui))
    .collect::<Result<Vec<_>>>()?;
```

Note: `reqwest::blocking::Client` is `Clone + Send + Sync`, so this is safe.

### 4.7 Use `clonefile()` on macOS

For staging file copies, macOS supports CoW cloning which is near-instant:

```rust
#[cfg(target_os = "macos")]
fn fast_copy(src: &Path, dst: &Path) -> std::io::Result<()> {
    use std::ffi::CString;
    let src_c = CString::new(src.to_str().unwrap()).unwrap();
    let dst_c = CString::new(dst.to_str().unwrap()).unwrap();
    let ret = unsafe { libc::clonefile(src_c.as_ptr(), dst_c.as_ptr(), 0) };
    if ret == 0 { Ok(()) } else { fs::copy(src, dst).map(|_| ()) }
}
```

## Step 5: Verification Plan

After each optimization:

```bash
# Run benchmarks
cargo bench

# Confirm correctness
cargo test

# Re-profile to see if bottleneck shifted
cargo flamegraph --release --bin agentpack -- sync
```

### Reporting template

```
## Benchmark Results: <optimization>

### Before
- full_sync_warm_cache: Xms
- phase_staging_rebuild: Xms
- Total wall-clock: 37s

### After
- full_sync_warm_cache: Xms  [-%]
- phase_staging_rebuild: Xms [-%]
- Total wall-clock: Xs       [-%]

### What changed
- (describe syscall/alloc/network reduction)

### Regression risk
- (none / describe)
```

## Immediate Next Steps

1. **Add tracing timestamps** to each sync step (5 min, zero dependency change) to identify which of the 6 steps dominates the 37s.
2. **Run `sudo dtruss -c`** to get syscall counts and identify I/O patterns.
3. **Run `cargo flamegraph`** on a warm-cache sync to see CPU distribution.
4. **Write divan benchmarks** for the identified bottleneck phase(s).
5. **Apply the optimization** that addresses the dominant phase, benchmark to verify.

The most impactful optimization is almost certainly in one of these three areas:
- **Network I/O** in resolve/cache steps (if dependencies are being re-resolved or re-downloaded)
- **Filesystem I/O** in staging rebuild (4 harnesses x N packages x WalkDir)
- **Redundant verify** after rebuild (walks everything twice)

The tracing timestamps from step 1 will tell you which within minutes.

## CI Regression Testing

```yaml
# .github/workflows/bench.yml
name: Benchmark
on: [pull_request]
jobs:
  bench:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: dtolnay/rust-toolchain@stable
      - name: Run benchmarks
        run: cargo bench -- --output-format bencher 2>&1 | tee bench_output.txt
      - name: Compare with baseline
        uses: benchmark-action/github-action-benchmark@v1
        with:
          tool: cargo
          output-file-path: bench_output.txt
          alert-threshold: '120%'
          comment-on-alert: true
```

## Reference: Key Files

| File | Role in sync |
|------|-------------|
| `src/sync/pipeline.rs` | Orchestrates 6 sync steps sequentially |
| `src/sync/run.rs` | Entry points for add/remove/lock/sync-for-launch |
| `src/resolve/mod.rs` | BFS dependency resolution with GitHub materialization |
| `src/cache/restore.rs` | `ensure_lock_{skill,plugin}_cached()` -- download or local restore |
| `src/cache/index.rs` | RedDB upsert/read per cache entry |
| `src/cache/materialize.rs` | `materialize_github_tree()` -- tarball fetch + extract |
| `src/github/download.rs` | `download_tarball_bytes()` + `extract_tarball_with_prefix()` |
| `src/staging/harnesses.rs` | `StagingPipeline` -- reset + 4 harness stagers + finalize |
| `src/staging/pack_overlay.rs` | `stage_pack_{plugins,skills}_for_target()` -- WalkDir + artifact parse |
| `src/staging/tree.rs` | `copy_merge_tree()` -- recursive fs::copy |
| `src/staging/seed.rs` | User config seeding for each harness |
| `src/staging/dot_agents.rs` | `.agents/` overlay merge |
| `src/staging/cursor.rs` | Cursor-specific staging + fake HOME |
