## Performance Analysis: copy_package_dir_to_cache / hash_directory_contents / copy_tree_files

### Critical (fix before merge)

- [SYSCALL] `copy_package_dir_to_cache` — **Double directory walk**: `hash_directory_contents` walks the entire source tree (opening and reading every file), then `copy_tree_files` walks it again (opening and reading every file to copy). This means every file is opened and read twice: 2N `open` + 2N `read` + 2N `close` = 6N syscalls instead of 3N. Combine hashing and copying into a single pass — read each file once, feed bytes into the hasher, and write them to the destination simultaneously. This cuts file I/O syscalls in half.

- [SYSCALL] `copy_package_dir_to_cache` — **Unconditional cache destruction when content is unchanged**: The function computes a content-addressed `cache_key` from the directory hash, then unconditionally deletes and re-copies the cache entry even when the `cache_key` matches an existing entry. Since the cache key is derived from content hashing, an existing cache entry with the same key is guaranteed to be identical. Add an early return when `out.exists()` instead of `remove_dir_all` + re-copy. This turns repeat operations on unchanged content from O(N) I/O (delete all + copy all) to O(1). For a directory with 500 files, this saves ~1500 syscalls on every re-invocation with unchanged content.

### Significant (worth addressing)

- [SYSCALL] `hash_directory_contents` — **Full file reads into memory via `fs::read()`**: Each file is loaded entirely into a `Vec<u8>` before feeding to the hasher. For large files this causes unnecessary memory spikes. Use a `BufReader` feeding fixed-size chunks (e.g., 64KB) into the hasher to cap memory at the buffer size regardless of file size. This also reduces memory allocator pressure from repeated large-vec allocations.

- [SYSCALL] `copy_tree_files` — **`fs::copy` instead of platform-optimized copy**: `std::fs::copy` uses a `read`+`write` loop in userspace. On macOS, `clonefile()` (copy-on-write via APFS) can copy a file in a single syscall with zero data movement. On Linux, `copy_file_range()` keeps data in kernel space. Consider using the `reflink` crate with fallback to `fs::copy`. For a 500-file tree on macOS/APFS, this can reduce copy time from seconds to near-instant.

- [HASH] `hash_directory_contents` — **SHA-256 for non-security content fingerprinting**: The cache key is used for content addressing, not cryptographic verification. SHA-256 is ~3-5x slower than alternatives. `blake3` is parallel-capable and ~5x faster than SHA-256 for this use case. For a directory with many files, the hashing time itself becomes measurable.

- [SYSCALL] `copy_package_dir_to_cache` — **`remove_dir_all` before copy is a full recursive delete**: Even if the early-return optimization above is not applied, deleting the entire tree and re-creating it is expensive. If the cache entry must be refreshed, consider diffing or simply overwriting in place (files that exist get overwritten, new files get created, stale files get removed). Though the simplest fix is the early-return above.

### Minor (nice to have)

- [MEMORY] `hash_directory_contents` — **`entries` Vec could use `with_capacity`**: `WalkDir` does not expose a size hint, but if the typical directory size is known (e.g., 50-200 files for most packages), a `Vec::with_capacity(128)` avoids ~7 reallocations during the walk. Marginal benefit.

- [ALLOC] `hash_directory_contents` — **`to_string_lossy()` allocates a `Cow` per path**: The `as_bytes()` call after `to_string_lossy()` is fine when the path is valid UTF-8 (returns borrowed), but on non-UTF-8 paths it allocates a `String`. On Unix, consider using `std::os::unix::ffi::OsStrExt::as_bytes()` directly on the `OsStr` to avoid the lossy conversion entirely.

- [SYSCALL] `copy_package_dir_to_cache` — **`fs::create_dir_all(&cache_dir)` is redundant**: `cache_entry_dir(&cache_key)` presumably returns a subdirectory of `cache_dir`. The subsequent `fs::create_dir_all(&out)` would create all parent directories including `cache_dir`. The explicit `create_dir_all(&cache_dir)` call is an extra syscall.

### Estimated impact

- **Current**: ~6N file I/O syscalls per invocation (2 full walks: hash + copy), plus unconditional delete + re-copy even when content is unchanged. SHA-256 hashing overhead on all files.
- **After critical fixes**: O(1) for unchanged content (early return on existing cache key). ~3N file I/O syscalls when content is new (single combined hash+copy pass).
- **After all fixes**: O(1) unchanged case; ~3N with platform-optimized copy and streaming hash for new content; ~3-5x faster hashing with blake3.
- **Expected speedup**: For repeat invocations with unchanged content (common case for `sync`/`lock`): effectively infinite speedup (skip all I/O). For new content with 500 files: ~2x from eliminating the double walk, additional gains from platform copy and faster hashing. Overall for typical CLI usage: 5-50x improvement depending on whether content has changed.
