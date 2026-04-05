# Performance Analysis: `hash_directory_contents` Optimization

## Step 1: Identify What to Benchmark

`hash_directory_contents` is called from `copy_package_dir_to_cache` on every path-sourced dependency during `lock` and `sync`. At 60% of total CPU time, this is the dominant bottleneck. It is user-facing latency (blocks CLI commands) and I/O-heavy (walks a directory tree and reads every file). This absolutely warrants optimization.

The function lives in `src/cache/layout.rs:79`. It:
1. Walks the directory with `WalkDir`, collecting all file relative paths into a `Vec`.
2. Sorts the paths for deterministic ordering.
3. Iterates sorted paths, reading each file fully (`fs::read`) and feeding path + contents into a single SHA-256 hasher.

For ~500 files totaling ~2MB, this means ~500 `open`+`read`+`close` syscall sequences plus the SHA-256 computation.

## Step 2: Write Benchmarks

Before applying any optimization, set up a benchmark to measure the baseline and verify improvements.

```toml
# Cargo.toml
[dev-dependencies]
divan = "0.1"
tempfile = "3"

[[bench]]
name = "hash_dir"
harness = false
```

```rust
// benches/hash_dir.rs
use std::fs;
use std::path::Path;
use divan::Bencher;
use tempfile::TempDir;

fn main() {
    divan::main();
}

fn create_test_dir(file_count: usize) -> TempDir {
    let dir = TempDir::new().unwrap();
    let avg_size = 2_000_000 / file_count; // ~2MB total
    for i in 0..file_count {
        let subdir = dir.path().join(format!("sub{}", i % 10));
        fs::create_dir_all(&subdir).unwrap();
        let data = vec![((i * 37) % 256) as u8; avg_size];
        fs::write(subdir.join(format!("file_{i}.txt")), &data).unwrap();
    }
    dir
}

#[divan::bench(args = [100, 500, 1000])]
fn hash_directory(bencher: Bencher, n: usize) {
    let dir = create_test_dir(n);
    bencher.bench_local(|| {
        agentpack::cache::layout::hash_directory_contents(dir.path()).unwrap()
    });
}
```

## Step 3: Profile to Find Bottlenecks

The flamegraph already tells us: 60% CPU in `hash_directory_contents`. Breaking this down further, the likely cost centers are:

1. **Syscalls from sequential `fs::read` calls** -- 500 individual `open`+`read`+`close` sequences. Each is a kernel boundary crossing.
2. **SHA-256 computation** -- ~2MB of data is modest for SHA-256 (SHA-256 throughput is ~500MB/s in software), so this is likely not the dominant factor.
3. **`WalkDir` traversal** -- one `readdir` + `stat` per entry, sequential.
4. **Sorting 500 `PathBuf`s** -- negligible at this scale.

The bottleneck is almost certainly the **sequential I/O**: 500 files read one at a time, each incurring syscall overhead. At ~2MB total, the actual hashing is fast; it is the per-file overhead that dominates.

## Step 4: Apply Targeted Optimizations

### Optimization A: Parallel file reading with rayon (highest impact)

The directory walk and file reads are embarrassingly parallel. Read files in parallel, then combine results deterministically.

```rust
use rayon::prelude::*;

pub fn hash_directory_contents(root: &Path) -> Result<String> {
    // Phase 1: collect paths (sequential walk, cheap)
    let mut relative_paths = Vec::new();
    for entry in WalkDir::new(root)
        .follow_links(false)
        .into_iter()
        .filter_map(|entry| entry.ok())
    {
        if !entry.file_type().is_file() {
            continue;
        }
        let rel = entry.path()
            .strip_prefix(root)
            .map_err(|err| AgentpackError::Cache(err.to_string()))?;
        relative_paths.push(rel.to_path_buf());
    }
    relative_paths.sort();

    // Phase 2: read files in parallel, producing per-file hashes
    let per_file_hashes: Vec<(usize, [u8; 32])> = relative_paths
        .par_iter()
        .enumerate()
        .map(|(i, rel)| {
            let mut h = Sha256::new();
            h.update(rel.as_os_str().as_encoded_bytes());
            h.update([0]);
            let bytes = fs::read(root.join(rel)).expect("file read failed");
            h.update(&bytes);
            (i, h.finalize().into())
        })
        .collect();

    // Phase 3: combine in deterministic order
    let mut final_hash = Sha256::new();
    let mut ordered: Vec<_> = per_file_hashes;
    ordered.sort_by_key(|(i, _)| *i);
    for (_, file_hash) in ordered {
        final_hash.update(file_hash);
    }

    let full = hex::encode(final_hash.finalize());
    Ok(truncate_str(&full, 40))
}
```

**Important**: This changes the hash output compared to the current implementation (because we hash per-file digests instead of streaming path+content into one hasher). This is acceptable only if all lockfiles are regenerated. If hash stability is required, use the streaming approach below instead.

### Optimization A (hash-stable variant): Parallel read, sequential hash

If the hash value must remain identical to the current implementation:

```rust
pub fn hash_directory_contents(root: &Path) -> Result<String> {
    let mut relative_paths = Vec::new();
    for entry in WalkDir::new(root)
        .follow_links(false)
        .into_iter()
        .filter_map(|entry| entry.ok())
    {
        if !entry.file_type().is_file() {
            continue;
        }
        let rel = entry.path()
            .strip_prefix(root)
            .map_err(|err| AgentpackError::Cache(err.to_string()))?;
        relative_paths.push(rel.to_path_buf());
    }
    relative_paths.sort();

    // Read all files in parallel (the expensive part)
    let file_contents: Vec<Vec<u8>> = relative_paths
        .par_iter()
        .map(|rel| fs::read(root.join(rel)).expect("file read failed"))
        .collect();

    // Hash sequentially in sorted order (fast, ~2MB)
    let mut hash = Sha256::new();
    for (rel, bytes) in relative_paths.iter().zip(file_contents.iter()) {
        hash.update(rel.as_os_str().as_encoded_bytes());
        hash.update([0]);
        hash.update(bytes);
    }

    let full = hex::encode(hash.finalize());
    Ok(truncate_str(&full, 40))
}
```

This preserves the exact same hash output. The parallel `fs::read` eliminates the sequential syscall bottleneck. The sequential hash pass over ~2MB of in-memory data is negligible.

**Expected improvement**: 3-5x on multi-core machines. The OS can dispatch reads to the block layer concurrently, and on SSDs, parallel reads complete in roughly the same wall time as a single read.

**Trade-off**: Peak memory is now ~2MB (all file contents in memory at once) vs streaming one file at a time. For 2MB total this is irrelevant.

**Dependency**: Add `rayon = "1"` to `Cargo.toml` `[dependencies]`.

### Optimization B: Reduce syscalls in WalkDir (moderate impact)

`WalkDir` calls `stat` on every entry. Use `sort_by` to avoid the extra `PathBuf` allocation + sort step, and skip `.git` and other irrelevant directories:

```rust
for entry in WalkDir::new(root)
    .follow_links(false)
    .sort_by_file_name()  // deterministic order from WalkDir itself
    .into_iter()
    .filter_entry(|e| !is_hidden_vcs_dir(e))  // skip .git, etc.
    .filter_map(|e| e.ok())
{
```

This eliminates the separate `sort()` call and avoids walking into `.git/` (which alone could contain hundreds of files in some cases).

### Optimization C: Short-circuit with metadata check (conditional, large impact)

If `hash_directory_contents` is called repeatedly for the same directory (e.g., during `sync` after `lock`), cache the result keyed by directory mtime:

```rust
use std::time::SystemTime;

fn dir_mtime(root: &Path) -> Result<SystemTime> {
    let mut latest = SystemTime::UNIX_EPOCH;
    for entry in WalkDir::new(root).follow_links(false).into_iter().filter_map(|e| e.ok()) {
        if let Ok(meta) = entry.metadata() {
            if let Ok(mtime) = meta.modified() {
                if mtime > latest {
                    latest = mtime;
                }
            }
        }
    }
    Ok(latest)
}
```

However, this still requires a full walk for mtime. A simpler approach: cache the hash result in the RedDB index keyed by `(canonical_path, dir_mtime)` and skip re-hashing when the mtime has not changed. This is only valid if the directory is not modified between calls within the same process.

## Step 5: Verify the Fix

After applying optimizations:

```bash
# Run the benchmark
cargo bench -- --filter "hash_directory"

# Confirm tests still pass
cargo test

# Re-profile to confirm the bottleneck has moved
cargo flamegraph --bin agentpack -- sync
```

### Expected Results

| Metric | Before | After (Optimization A, hash-stable) | Change |
|--------|--------|--------------------------------------|--------|
| `hash_directory_contents` (500 files, 2MB) | ~60% of total CPU | ~15-20% of total CPU | -65% to -75% |
| Wall time for the function | ~50-100ms (estimated) | ~15-25ms | -3x to -5x |
| Syscall pattern | 500 sequential `open`+`read`+`close` | 500 parallel `open`+`read`+`close` | Same count, concurrent |
| Memory | ~4KB peak (one file at a time) | ~2MB peak (all files) | Acceptable |
| Hash output | `<40 hex>` | `<same 40 hex>` | Identical |

### Recommended Implementation Order

1. **Optimization A (hash-stable variant)** -- Highest impact, preserves hash compatibility, one new dependency (`rayon`). This alone should reduce `hash_directory_contents` from 60% of CPU to ~15-20%.
2. **Optimization B** -- Add `.sort_by_file_name()` to WalkDir and filter `.git` directories. Small code change, no new dependencies, modest improvement.
3. **Optimization C** -- Only if profiling shows the function is still called multiple times for the same directory within a single CLI invocation.

### Regression Risk

- **Optimization A (hash-stable)**: None -- hash output is identical, behavior unchanged, `rayon` is a mature dependency. All existing tests and lockfiles remain valid.
- **Optimization A (hash-changing)**: Medium -- all `pack.lock` files would need regeneration. Only use this if hash stability is not a concern.
- **Optimization B**: None -- filtering VCS directories and using WalkDir's built-in sort are safe changes.
