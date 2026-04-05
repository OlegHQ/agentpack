# Performance Analysis: `hash_directory_contents`

## Current Implementation

The function in `src/cache/layout.rs` (line 79) walks a directory with `WalkDir`, collects all file paths, sorts them, then reads each file fully into memory with `fs::read()` and feeds the bytes into a single `SHA256` hasher. For ~500 files / ~2 MB total this is the dominant cost at 60% of CPU time.

## Bottlenecks Identified

### 1. Full-file `fs::read()` allocates and copies every byte twice

`fs::read(root.join(&rel))` (line 102) allocates a `Vec<u8>` per file, copies the kernel page-cache contents into userspace, then the hasher reads that buffer. For 500 files this is 500 allocations + 500 deallocations plus the memcpy overhead that dwarfs the actual SHA256 compute on 2 MB of data.

### 2. No streaming / buffered I/O

Each file is loaded entirely into a `Vec` before any hashing begins. A `BufReader` feeding `io::copy` into the hasher (SHA256 implements `io::Write`) would avoid the per-file heap allocation entirely and hash in fixed-size 8 KB chunks.

### 3. Sequential single-threaded I/O

All 500 files are read and hashed on one thread. On SSDs the syscall latency per `open`+`read`+`close` dominates. Parallelizing the walk or the per-file hashing with `rayon` would let multiple files be in-flight simultaneously. However, because the final hash must be deterministic (sorted path order feeding a single hasher), full parallelism requires a two-phase approach: parallel per-file hashing, then sorted merge of `(path, file_hash)` pairs into the final hasher.

### 4. Double directory walk when copying to cache

`copy_package_dir_to_cache` in `src/cache/tree.rs` calls `hash_directory_contents` (walks all files, reads all bytes) and then immediately calls `copy_tree_files` (walks all files again, reads all bytes again). The directory is traversed and every byte is read **twice**. This is the single largest waste.

### 5. Unconditional re-copy even when the cache slot already matches

In `copy_package_dir_to_cache` (tree.rs line 52-54), if `out.exists()` the existing cache directory is deleted and re-populated from scratch. Since the `commit` hash (content fingerprint) is already computed, the function could skip the copy entirely when a cache entry with the same `cache_key` already exists and passes a quick integrity check.

## Recommended Fixes (ordered by impact)

### Fix 1: Skip copy when cache already matches (highest impact, easiest)

```rust
pub fn copy_package_dir_to_cache(
    from: &Path,
    identity_prefix: &str,
) -> Result<(String, String, PathBuf)> {
    paths::ensure_user_agentpack_layout()?;
    let commit = hash_directory_contents(from)?;
    let identity = format!("{identity_prefix}\0{commit}");
    let cache_key = compute_cache_key(&identity);
    let out = cache_entry_dir(&cache_key)?;

    // If the cache slot already exists with matching content, skip the copy entirely.
    if out.exists() {
        return Ok((cache_key, commit, out));
    }

    let cache_dir = paths::cache_dir()?;
    fs::create_dir_all(&cache_dir).map_err(|err| AgentpackError::io(&cache_dir, err))?;
    fs::create_dir_all(&out).map_err(|err| AgentpackError::io(&out, err))?;
    copy_tree_files(from, &out)?;
    normalize_plugin_cache_layout(&out)?;
    Ok((cache_key, commit, out))
}
```

This eliminates the second full directory walk+read on repeat syncs (the common case). The cache key is content-addressed, so if the slot exists, the content matches by construction.

### Fix 2: Stream files through `BufReader` instead of `fs::read`

Replace the per-file `fs::read` allocation with streaming I/O:

```rust
use std::io::{self, BufReader};

let mut hash = Sha256::new();
for rel in relative_paths {
    hash.update(rel.as_os_str().as_encoded_bytes());
    hash.update([0]);
    let file = fs::File::open(root.join(&rel))
        .map_err(|err| AgentpackError::io(root.join(&rel), err))?;
    let mut reader = BufReader::new(file);
    io::copy(&mut reader, &mut hash)
        .map_err(|err| AgentpackError::io(root.join(&rel), err))?;
}
```

This eliminates 500 heap allocations per invocation and reduces peak memory from O(largest file) to O(8 KB buffer).

### Fix 3: Combine hash + copy in a single walk

Restructure `copy_package_dir_to_cache` to hash during the copy pass, eliminating the double traversal:

```rust
pub fn copy_package_dir_to_cache(
    from: &Path,
    identity_prefix: &str,
) -> Result<(String, String, PathBuf)> {
    paths::ensure_user_agentpack_layout()?;

    // Single walk: collect sorted paths, hash while copying to a temp dir,
    // then rename into final cache slot.
    let mut relative_paths = Vec::new();
    for entry in WalkDir::new(from).follow_links(false).into_iter().filter_map(|e| e.ok()) {
        if entry.file_type().is_file() {
            let rel = entry.path().strip_prefix(from)
                .map_err(|e| AgentpackError::Cache(e.to_string()))?;
            relative_paths.push(rel.to_path_buf());
        }
    }
    relative_paths.sort();

    let cache_dir = paths::cache_dir()?;
    let tmp = cache_dir.join(format!(".tmp-{}", std::process::id()));
    fs::create_dir_all(&tmp).map_err(|err| AgentpackError::io(&tmp, err))?;

    let mut hasher = Sha256::new();
    for rel in &relative_paths {
        hasher.update(rel.as_os_str().as_encoded_bytes());
        hasher.update([0]);
        let src = from.join(rel);
        let dst = tmp.join(rel);
        if let Some(parent) = dst.parent() {
            fs::create_dir_all(parent).map_err(|err| AgentpackError::io(parent, err))?;
        }
        // Read once, hash and write
        let bytes = fs::read(&src).map_err(|err| AgentpackError::io(&src, err))?;
        hasher.update(&bytes);
        fs::write(&dst, &bytes).map_err(|err| AgentpackError::io(&dst, err))?;
    }

    let commit = truncate_str(&hex::encode(hasher.finalize()), 40);
    let identity = format!("{identity_prefix}\0{commit}");
    let cache_key = compute_cache_key(&identity);
    let out = cache_entry_dir(&cache_key)?;

    if out.exists() {
        // Content matches, discard temp copy
        fs::remove_dir_all(&tmp).ok();
    } else {
        fs::rename(&tmp, &out).map_err(|err| AgentpackError::io(&out, err))?;
        normalize_plugin_cache_layout(&out)?;
    }

    Ok((cache_key, commit, out))
}
```

This reads each byte exactly once (hash + write simultaneously) and skips the copy if the cache slot already exists.

### Fix 4: Parallel per-file hashing with rayon (diminishing returns at 2 MB)

For larger directories, hash files in parallel using `rayon`, then merge results:

```rust
use rayon::prelude::*;

relative_paths.sort();
let file_hashes: Vec<(PathBuf, [u8; 32])> = relative_paths
    .par_iter()
    .map(|rel| {
        let mut h = Sha256::new();
        let bytes = fs::read(root.join(rel))?;
        h.update(&bytes);
        Ok((rel.clone(), h.finalize().into()))
    })
    .collect::<Result<Vec<_>>>()?;

let mut final_hash = Sha256::new();
for (rel, file_hash) in &file_hashes {
    final_hash.update(rel.as_os_str().as_encoded_bytes());
    final_hash.update([0]);
    final_hash.update(file_hash);
}
```

This adds a `rayon` dependency. At 2 MB total the payoff is modest; it matters more if directory sizes grow. Note this changes the hash output (hashing per-file digests vs raw content), so it would require a cache migration or version bump.

## Summary

| Fix | Effort | Impact | Breaking? |
|-----|--------|--------|-----------|
| Skip copy when cache exists | Trivial | High (eliminates ~50% of hot path on repeat runs) | No |
| BufReader streaming | Small | Medium (eliminates 500 allocs, reduces peak mem) | No |
| Single-pass hash+copy | Medium | High (eliminates double walk entirely) | No |
| Parallel hashing (rayon) | Small | Low at 2 MB, high at larger scale | Yes (hash changes) |

The recommended approach is to apply fixes 1 and 2 immediately (low risk, no hash format change), and consider fix 3 as a follow-up refactor that unifies the two operations. Fix 4 is only worth the dependency if directory sizes are expected to grow significantly.
