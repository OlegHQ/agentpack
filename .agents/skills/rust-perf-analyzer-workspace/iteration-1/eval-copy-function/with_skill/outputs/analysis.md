## Performance Analysis: copy_package_dir_to_cache / hash_directory_contents / copy_tree_files

### Critical (fix before merge)

- [SYSCALL] `hash_directory_contents` + `copy_tree_files` -- **Double walk of the entire source tree.** `hash_directory_contents` calls `WalkDir::new(dir)` and reads every file to hash it, then `copy_tree_files` calls `WalkDir::new(from)` and reads every file again to copy it. For a directory with N files of total size S, this is 2N `open`+`read`+`close` sequences (6N syscalls for file I/O alone) plus 2 full directory traversals. A single-pass approach that hashes while copying (or hashes the destination after copy) would cut file-read syscalls in half.

- [SYSCALL] `copy_package_dir_to_cache`:52-54 -- **Unconditional `remove_dir_all` + full re-copy when cache entry exists.** If the cache entry already exists with identical content (same `cache_key`), the function deletes and re-copies everything. Since the `cache_key` is derived from the content hash, an existing entry with the same key is guaranteed to have the same content. The fix is trivial: if `out.exists()`, return early with the existing path instead of deleting and re-copying.

### Significant (worth addressing)

- [SYSCALL] `hash_directory_contents` -- **Reads entire file contents into memory per file with `fs::read(path)`.** Each call allocates a `Vec<u8>` the size of the file, hashes it, then drops the buffer. For large files this causes large transient allocations. Using `BufReader` with a fixed-size buffer (e.g., 64KB) and feeding chunks to the hasher would cap memory usage and reduce allocator pressure. For a tree with a single 100MB file, the current approach allocates 100MB transiently (twice, since the tree is walked twice -- see Critical above).

- [MEMORY] `hash_directory_contents` -- **`entries: Vec<PathBuf>` stores full absolute paths, then sorts.** Each `PathBuf` is a heap allocation. For N files this is N allocations just for sorting. Using relative paths from the start (strip prefix during collection, not after) would produce shorter strings. Alternatively, collecting `(OsString, PathBuf)` tuples with the relative path as sort key avoids the `strip_prefix` + `to_string_lossy` indirection in the hash loop.

- [SYSCALL] `copy_tree_files` -- **Per-file `fs::copy` uses userspace read+write.** On macOS, `clonefile(2)` (APFS copy-on-write) can copy a file or entire tree in a single syscall with zero data movement. On Linux, `copy_file_range(2)` keeps data in kernel space. The `reflink-copy` crate exposes these with a fallback to standard copy. For a 500-file tree this could reduce wall time significantly on APFS volumes.

- [SYSCALL] `copy_tree_files` -- **`create_dir_all` called for every directory entry.** `create_dir_all` does a stat + potentially multiple `mkdir` syscalls, including for directories that already exist (parent dirs). Since WalkDir yields entries in top-down order, a single `create_dir` (not `create_dir_all`) suffices for each directory entry, saving redundant stat calls on parent directories.

- [SYSCALL] `copy_package_dir_to_cache`:56-58 -- **Redundant `create_dir_all` calls.** `cache_entry_dir` returns a path under `cache_dir`. The code then calls `create_dir_all` on `cache_dir` and again on `out`. Since `create_dir_all(&out)` already creates all ancestors including `cache_dir`, the `create_dir_all(&cache_dir)` call is redundant. Additionally, `paths::ensure_user_agentpack_layout()` at line 46 likely already ensures the cache directory. This adds 1-2 unnecessary stat/mkdir syscalls per invocation.

### Minor (nice to have)

- [ITER] `hash_directory_contents` -- **`.filter_map(|e| e.ok())` silently ignores walk errors.** This is not a performance issue per se, but silently skipping unreadable entries means the hash may not reflect the true directory state, leading to false cache hits. Consider logging or propagating walk errors.

- [MEMORY] `hash_directory_contents` -- **`to_string_lossy()` for path hashing.** Using `as_os_str().as_encoded_bytes()` (available on Rust 1.74+) would avoid the lossy UTF-8 conversion and potential `Cow` allocation for non-UTF-8 paths. This also makes the hash stable across platforms where paths contain non-UTF-8 bytes.

- [ALLOC] `copy_package_dir_to_cache`:48 -- **`format!("{identity_prefix}\0{commit}")` allocates a temporary String.** Minor, since this runs once per call, but the hasher could be fed the prefix and commit bytes directly without the intermediate allocation: `hasher.update(identity_prefix); hasher.update(b"\0"); hasher.update(&commit);`.

### Estimated impact

- **Current**: O(N) with ~6N file I/O syscalls (2 full tree walks, each opening+reading+closing every file) plus per-entry stat/mkdir overhead. Unconditionally deletes and re-copies even when cache is already valid.
- **After critical fixes**: O(N) with ~3N file I/O syscalls (single walk), plus early-return when cache hit avoids all I/O entirely.
- **Expected speedup**: For repeat invocations with unchanged content (cache hit), improvement is effectively infinite (skip all work). For cold-cache runs, ~2x reduction in file I/O syscalls. Reflink/clonefile on supported filesystems could add another 2-5x on top for large trees.
