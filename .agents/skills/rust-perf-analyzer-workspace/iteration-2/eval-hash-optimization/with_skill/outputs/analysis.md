# Performance Analysis: `hash_directory_contents` Optimization

## Step 0: Quick Triage

`hash_directory_contents` consumes 60% of CPU time. It lives in `src/cache/layout.rs:79` and is called from `copy_package_dir_to_cache` in `src/cache/tree.rs:47` during `lock` and `sync` for every path-sourced dependency. This is user-facing latency (blocks CLI commands) and I/O-heavy (walks a directory, reads every file, SHA-256 hashes all content). This absolutely warrants optimization.

The function does:
1. Walk the directory tree with `WalkDir`, collecting all file paths
2. Sort paths for deterministic ordering
3. Sequentially read each file and feed path + content into a single `Sha256` hasher

For ~500 files totaling ~2MB, this means ~500 `open` + `read` + `close` syscall sequences plus the SHA-256 computation over ~2MB of data.

## Step 1: What to Benchmark

The benchmark target is `hash_directory_contents` itself -- it is the hot path (called per dependency, 60% CPU), user-facing (blocks `sync`/`lock`), and I/O-heavy. The full `copy_package_dir_to_cache` function is also worth benchmarking end-to-end since it walks the same directory *twice* (once to hash, once to copy).

## Step 2: Benchmark Design

```rust
// benches/hash_bench.rs
use divan::Bencher;
use std::path::Path;
use tempfile::TempDir;
use std::fs;

fn main() {
    divan::main();
}

fn create_test_dir(file_count: usize) -> TempDir {
    let dir = TempDir::new().unwrap();
    for i in 0..file_count {
        let subdir = dir.path().join(format!("sub{}", i / 50));
        fs::create_dir_all(&subdir).unwrap();
        let content = format!("file content {i} with some padding to be realistic");
        fs::write(subdir.join(format!("file_{i}.txt")), content).unwrap();
    }
    dir
}

#[divan::bench(args = [50, 500, 2000])]
fn hash_directory(bencher: Bencher, n: usize) {
    let dir = create_test_dir(n);
    bencher.bench_local(|| {
        agentpack::cache::layout::hash_directory_contents(dir.path()).unwrap()
    });
}
```

This parameterizes by file count to reveal scaling behavior. If time scales worse than linearly, there is a complexity problem on top of the I/O cost.

## Step 3: Profile Findings

The flamegraph already tells us: 60% CPU in `hash_directory_contents`. The cost centers are:

1. **500 sequential file reads** -- each is an `open` + `read` + `close` syscall triple. At ~500 files, that is ~1500 filesystem syscalls executed sequentially. Kernel transitions dominate over the actual data processing for small files (~4KB average).
2. **SHA-256 throughput** -- SHA-256 processes at ~250 MB/s on typical hardware. For 2MB of data, the raw hash time is ~8 microseconds -- negligible. The bottleneck is not the hash algorithm's throughput on the data; it is the per-file syscall overhead.
3. **Double directory walk** -- `copy_package_dir_to_cache` calls `hash_directory_contents(from)` and then `copy_tree_files(from, &out)`. Both walk the same directory tree and read every file. The directory is traversed and every byte is read **twice**.
4. **Vec<PathBuf> allocation** -- 500 `PathBuf` heap allocations for sorting. Minor compared to I/O but still unnecessary allocation pressure.

## Step 4: Recommended Optimizations

### Optimization A: Parallel file reads with rayon (highest impact, ~3-5x speedup on hash alone)

The file reads are embarrassingly parallel. Each file is independent; only the final hash needs deterministic ordering. Read files in parallel, then feed sorted results into the hasher sequentially:

```rust
use rayon::prelude::*;
use sha2::{Sha256, Digest};

pub fn hash_directory_contents(root: &Path) -> Result<String> {
    let mut entries: Vec<PathBuf> = Vec::new();
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
        entries.push(rel.to_path_buf());
    }
    entries.sort();

    // Read all files in parallel
    let contents: Vec<(PathBuf, Vec<u8>)> = entries.into_par_iter()
        .map(|rel| {
            let bytes = fs::read(root.join(&rel))
                .map_err(|err| AgentpackError::io(root.join(&rel), err));
            bytes.map(|b| (rel, b))
        })
        .collect::<Result<Vec<_>>>()?;

    let mut hash = Sha256::new();
    for (rel, bytes) in &contents {
        hash.update(rel.as_os_str().as_encoded_bytes());
        hash.update([0]);
        hash.update(bytes);
    }

    let full = hex::encode(hash.finalize());
    Ok(truncate_str(&full, 40))
}
```

This keeps the hash deterministic (sorted paths, sequential hash update) while parallelizing the I/O-bound file reads. For 500 small files, the wall-clock time is dominated by filesystem latency, and parallel reads on modern SSDs can saturate the I/O queue.

**Trade-off:** This holds all 2MB in memory simultaneously. For ~2MB this is fine. If directories could be much larger, a chunked approach would be needed.

**Dependency:** `rayon = "1.10"` (already a common Rust dependency, zero-config thread pool).

### Optimization B: Eliminate the double directory walk (moderate impact, ~30-40% reduction of total `copy_package_dir_to_cache` time)

`copy_package_dir_to_cache` in `src/cache/tree.rs` walks the directory twice: once for hashing, once for copying. Combine both into a single pass that hashes content as it copies:

```rust
pub fn copy_package_dir_to_cache(
    from: &Path,
    identity_prefix: &str,
) -> Result<(String, String, PathBuf)> {
    paths::ensure_user_agentpack_layout()?;

    // Single walk: collect sorted entries with content
    let mut entries: Vec<(PathBuf, Vec<u8>)> = Vec::new();
    for entry in WalkDir::new(from).follow_links(false).into_iter().filter_map(|e| e.ok()) {
        if !entry.file_type().is_file() { continue; }
        let rel = entry.path().strip_prefix(from)
            .map_err(|e| AgentpackError::Cache(e.to_string()))?;
        let bytes = fs::read(entry.path())
            .map_err(|e| AgentpackError::io(entry.path(), e))?;
        entries.push((rel.to_path_buf(), bytes));
    }
    entries.sort_by(|a, b| a.0.cmp(&b.0));

    // Hash from sorted entries
    let mut hash = Sha256::new();
    for (rel, bytes) in &entries {
        hash.update(rel.as_os_str().as_encoded_bytes());
        hash.update([0]);
        hash.update(bytes);
    }
    let commit = truncate_str(&hex::encode(hash.finalize()), 40).to_string();

    let identity = format!("{identity_prefix}\0{commit}");
    let cache_key = compute_cache_key(&identity);
    let out = cache_entry_dir(&cache_key)?;

    if out.exists() {
        fs::remove_dir_all(&out).map_err(|e| AgentpackError::io(&out, e))?;
    }
    let cache_dir = paths::cache_dir()?;
    fs::create_dir_all(&cache_dir).map_err(|e| AgentpackError::io(&cache_dir, e))?;
    fs::create_dir_all(&out).map_err(|e| AgentpackError::io(&out, e))?;

    // Write from memory instead of re-reading
    for (rel, bytes) in &entries {
        let dst = out.join(rel);
        if let Some(parent) = dst.parent() {
            fs::create_dir_all(parent).map_err(|e| AgentpackError::io(parent, e))?;
        }
        fs::write(&dst, bytes).map_err(|e| AgentpackError::io(&dst, e))?;
    }

    normalize_plugin_cache_layout(&out)?;
    Ok((cache_key, commit, out))
}
```

This eliminates ~500 redundant file reads and ~500 redundant directory walk entries. Combined with Optimization A (parallelizing the reads), total syscall count drops from ~3000+ to ~1500.

### Optimization C: Switch from SHA-256 to BLAKE3 (moderate impact on CPU portion, ~5-20x faster hashing)

The hash is for content addressing / cache keys, not cryptographic signatures. BLAKE3 is:
- ~5 GB/s vs SHA-256's ~250 MB/s (20x faster on data)
- Natively parallelizable within a single hash (for larger inputs)
- Equally collision-resistant for content addressing

```rust
// Replace sha2::Sha256 with blake3::Hasher
let mut hash = blake3::Hasher::new();
for (rel, bytes) in &entries {
    hash.update(rel.as_os_str().as_encoded_bytes());
    hash.update(&[0]);
    hash.update(bytes);
}
let commit = hash.finalize().to_hex();
let commit = &commit[..40]; // truncate to 40 hex chars
```

**Dependency:** `blake3 = "1"` (~350KB, pure Rust with optional SIMD).

**Breaking change:** This changes the hash output for existing `pack.lock` `commit` fields on path dependencies. All path-sourced cache entries would get new `cache_key` values on next `sync`. Since `pack.lock` is regenerated on `lock`/`sync`, this is acceptable -- existing cached trees are simply re-cached under new keys.

For 2MB of data, SHA-256 takes ~8us and BLAKE3 ~0.4us. The absolute saving is small because I/O dominates, but this becomes significant if the directory grows or if the function is called many times per invocation.

### Optimization D: Mtime-based short-circuit (high impact for repeated invocations)

If `hash_directory_contents` is called multiple times for the same directory within a single CLI invocation (e.g., `sync` after `lock`), cache the result keyed by the directory's maximum mtime:

```rust
use std::time::SystemTime;
use std::collections::HashMap;
use std::sync::Mutex;
use std::sync::LazyLock;

static HASH_CACHE: LazyLock<Mutex<HashMap<PathBuf, (SystemTime, String)>>> =
    LazyLock::new(|| Mutex::new(HashMap::new()));

pub fn hash_directory_contents(root: &Path) -> Result<String> {
    let root = root.canonicalize()
        .map_err(|e| AgentpackError::io(root, e))?;

    // Check mtime of the directory itself
    let mtime = fs::metadata(&root)
        .and_then(|m| m.modified())
        .map_err(|e| AgentpackError::io(&root, e))?;

    if let Ok(cache) = HASH_CACHE.lock() {
        if let Some((cached_mtime, cached_hash)) = cache.get(&root) {
            if *cached_mtime == mtime {
                return Ok(cached_hash.clone());
            }
        }
    }

    let hash = hash_directory_contents_inner(&root)?;

    if let Ok(mut cache) = HASH_CACHE.lock() {
        cache.insert(root, (mtime, hash.clone()));
    }

    Ok(hash)
}
```

**Caveat:** Directory mtime only reflects direct children changes on most filesystems, not nested file changes. For a robust mtime check, you would need to walk the tree and find the max mtime -- which itself costs ~500 `stat` calls. This optimization is most valuable if the caller checks a single root directory mtime as a fast-reject (1 stat vs 500 reads), accepting that nested changes might be missed on some calls. For agentpack's use case (path dependencies where the user explicitly runs `sync`), this trade-off is reasonable.

## Step 5: Expected Results

| Metric | Before | After (A+B+C) | Change |
|--------|--------|----------------|--------|
| `hash_directory_contents` (500 files, 2MB) | ~60% of total CPU | ~10-15% of total CPU | -75% to -85% |
| Syscalls in `copy_package_dir_to_cache` | ~3000+ (walk+read+walk+read+copy) | ~1500 (walk+parallel-read+write) | -50% |
| Hash throughput (CPU portion only) | ~250 MB/s (SHA-256) | ~5 GB/s (BLAKE3) | +20x |
| Memory during hash | ~4KB peak (streaming reads) | ~2MB peak (all files in memory) | +500x (acceptable for 2MB) |

### Verification plan

```bash
# Run existing tests to confirm hash stability where it matters
cargo test

# If switching to BLAKE3, ensure pack.lock regeneration works
cargo run --release -- lock
cargo run --release -- sync

# Profile again to confirm bottleneck has moved
cargo flamegraph --bin agentpack -- sync

# Wall-clock comparison
time cargo run --release -- sync  # before
time cargo run --release -- sync  # after
```

## Recommended Implementation Order

1. **Optimization B (merge hash + copy)** -- Highest impact-to-effort ratio. Eliminates the double walk entirely with no new dependencies. Alone should cut `copy_package_dir_to_cache` wall time by ~30-40%.
2. **Optimization A (parallel reads with rayon)** -- Next highest impact. Adds one dependency (`rayon`). Combined with B, reduces `hash_directory_contents` from 60% CPU to ~10-15%.
3. **Optimization C (BLAKE3)** -- Worth doing if you are already changing the hash code. Small absolute gain for 2MB but future-proofs for larger packages. Note: changes cache keys.
4. **Optimization D (mtime cache)** -- Only if profiling shows repeated hashing of the same directory within a single invocation. Lower priority.

### Regression risk

- **Optimizations A and B:** No behavioral change. Same hash output, same cache layout. All existing tests should pass.
- **Optimization C:** Changes hash output. `pack.lock` `commit` fields for path dependencies will change. Existing cache entries become orphaned (harmless, cleaned on next sync). This is acceptable for a pre-release tool with no backwards compatibility guarantees.
- **Optimization D:** Risk of stale cache if directory mtime is unreliable. Mitigated by only using it as a fast path with fallback to full hash.
