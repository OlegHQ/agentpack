# Performance Analysis: `extract_tarball`

## Function reviewed

The provided `extract_tarball` function and the production equivalent `extract_tarball_with_prefix` in `src/github/download.rs`.

## Issues found

### 1. Unnecessary intermediate buffer for file contents (Medium impact)

```rust
let mut contents = Vec::new();
entry.read_to_end(&mut contents)?;
fs::write(&target, &contents)?;
```

This allocates a `Vec<u8>`, reads the entire file into memory, then writes it to disk. For large files this doubles peak memory usage unnecessarily. The production code (`extract_tarball_with_prefix`) already fixes this correctly by using `std::io::copy(&mut entry, &mut f)`, which streams data through a fixed-size internal buffer without allocating proportional to file size.

**Verdict:** The production code does not have this bug. The snippet under review does.

### 2. Redundant `create_dir_all` calls (Low impact)

Every file entry calls `create_dir_all` on its parent directory, even when many files share the same parent. `create_dir_all` issues a `stat` syscall chain for each path component on every call. For archives with hundreds of files in the same directory, this is redundant work.

**Mitigation options:**
- Cache a `HashSet<PathBuf>` of already-created directories and skip the syscall when the parent is already known to exist. This trades a small heap allocation for potentially many saved syscalls.
- In practice, the OS kernel caches directory metadata aggressively, so the real cost is the userspace-to-kernel round-trip per call, not disk I/O. For typical GitHub tarballs (tens to low hundreds of files), this is negligible.

**Verdict:** Low priority. Not worth adding complexity for typical agentpack workloads.

### 3. Path re-parsing per entry (Negligible impact)

```rust
let path = entry.path()?.to_path_buf();
let components: Vec<_> = path.components().collect();
```

Collecting all components into a `Vec` allocates on every entry. The production code avoids `Path::components()` entirely and uses string splitting (`path.split('/')`) which is cheaper but still allocates a `Vec<&str>`. Both could use an iterator with `.skip(n)` to avoid the intermediate collection, but the savings are trivial for typical archive sizes.

**Verdict:** Negligible. Not actionable.

### 4. Decompression is single-pass and streaming (Good)

`GzDecoder` wrapping the byte slice and feeding `Archive` is already the right approach. The gzip stream is decompressed once in a streaming fashion. No issue here.

### 5. No parallelism for file writes (Not actionable)

Tar entries must be read sequentially (the format is a linear stream). Parallelizing writes would require buffering entries and spawning tasks, adding significant complexity for marginal gain on the small archives agentpack handles.

**Verdict:** Not worth pursuing.

## Production code comparison

The actual production function `extract_tarball_with_prefix` in `src/github/download.rs` (lines 113-186) is already well-optimized relative to the snippet:

- Uses `std::io::copy` for streaming writes (no intermediate buffer).
- Uses `Cursor::new(buf)` to wrap the byte slice for `GzDecoder` (required since `buf` is `&[u8]` and `GzDecoder` needs `Read`).
- Uses string-based path manipulation (`split('/')`, `strip_prefix`) which avoids the heavier `Path::components()` machinery.
- Creates the output file with `fs::File::create` and streams directly, which is the idiomatic zero-copy-ish approach.

## Summary

| Issue | Severity | Present in production? |
|---|---|---|
| `read_to_end` + `fs::write` instead of `io::copy` | Medium | No -- production uses `io::copy` |
| Redundant `create_dir_all` per file | Low | Yes, but negligible for typical archive sizes |
| `Vec` allocation for path components | Negligible | Yes (as `Vec<&str>` from `split`) |

The only meaningful performance issue in the snippet -- buffering entire file contents in memory -- is already resolved in the production code. The remaining micro-optimizations are not worth pursuing given the typical workload (GitHub tarballs with tens to hundreds of small files).
