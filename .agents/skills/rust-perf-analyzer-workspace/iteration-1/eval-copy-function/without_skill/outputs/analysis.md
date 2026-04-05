# Performance Analysis: `copy_package_dir_to_cache`

**Files analyzed:**
- `src/cache/tree.rs` (actual implementation)
- `src/cache/layout.rs` (`hash_directory_contents`, `normalize_plugin_cache_layout`)

The code snippet provided in the prompt differs from the actual codebase. The analysis below covers both the snippet and the real implementation, noting where they diverge.

---

## Issue 1 (High): Double traversal of the source directory

**Location:** `copy_package_dir_to_cache` in `src/cache/tree.rs`, lines 47 and 59.

`hash_directory_contents(from)` walks the entire directory tree and reads every file to compute the SHA-256 digest. Then `copy_tree_files(from, &out)` walks the same tree again to copy each file. For large packages this means:

- **Two full directory walks** (syscalls for every `readdir`, `stat`/`lstat`).
- **Every file is read from disk twice** -- once for hashing, once for `fs::copy` (which internally reads + writes). The OS page cache will often serve the second read from memory, but on cold caches or memory-constrained systems this doubles I/O.

**Suggested fix:** Combine hashing and copying into a single pass. Read each file once, feed the bytes to the hasher, then write them to the destination. This halves both syscall count and read I/O. The cache_key depends on the hash, so the destination path is not known until after hashing completes -- but the content hash (`commit`) is the only thing computed from file contents. One approach:

1. Walk once, collecting relative paths (sorted) and reading file bytes.
2. Hash during the walk.
3. Compute cache_key from the finished hash.
4. Write the already-in-memory file contents to the destination.

Trade-off: this holds all file contents in memory simultaneously. For packages that are typically small (markdown, JSON, config), this is fine. For very large trees, a two-phase approach that stores to a temp dir and renames would avoid the memory spike while still avoiding double reads.

---

## Issue 2 (Medium): Unconditional `remove_dir_all` when cache entry exists

**Location:** `src/cache/tree.rs`, lines 52-54.

```rust
if out.exists() {
    fs::remove_dir_all(&out).map_err(|err| AgentpackError::io(&out, err))?;
}
```

The cache is content-addressed: same identity prefix + same directory content hash = same cache_key. If `out` already exists, the contents are by definition identical (barring hash collisions or external corruption). Deleting and re-copying is pure waste in the common "already cached" case.

**Suggested fix:** Return early when the cache directory already exists:

```rust
if out.exists() {
    return Ok((cache_key, commit, out));
}
```

This turns repeat `add` / `lock` / `sync` of unchanged path dependencies into a no-op after the hash check, which is the fast path that matters most for developer iteration.

If corruption recovery is desired, a `--force` flag or integrity check would be more targeted than always deleting.

---

## Issue 3 (Medium): `hash_directory_contents` reads entire files into memory

**Location:** `src/cache/layout.rs`, lines 101-102.

```rust
let bytes = fs::read(root.join(&rel))
    .map_err(|err| AgentpackError::io(root.join(rel), err))?;
```

`fs::read` loads the entire file into a `Vec<u8>`. For hashing, streaming via `BufReader` with a fixed buffer (e.g., 64 KiB) would cap peak memory regardless of file size:

```rust
let mut reader = BufReader::with_capacity(64 * 1024, File::open(path)?);
loop {
    let buf = reader.fill_buf()?;
    if buf.is_empty() { break; }
    hash.update(buf);
    let n = buf.len();
    reader.consume(n);
}
```

For typical skill/plugin packages (small files), this is minor. It becomes relevant if a package contains large binary assets.

---

## Issue 4 (Low): Redundant `create_dir_all` calls

**Location:** `src/cache/tree.rs`, lines 56-58.

```rust
let cache_dir = paths::cache_dir()?;
fs::create_dir_all(&cache_dir).map_err(|err| AgentpackError::io(&cache_dir, err))?;
fs::create_dir_all(&out).map_err(|err| AgentpackError::io(&out, err))?;
```

`create_dir_all(&out)` already creates all ancestors including `cache_dir`. The separate `create_dir_all(&cache_dir)` is redundant. Additionally, `ensure_user_agentpack_layout()` on line 46 likely already creates the cache directory. Minor syscall savings but worth cleaning up for clarity.

---

## Issue 5 (Low): `root.join(&rel)` computed twice in `hash_directory_contents`

**Location:** `src/cache/layout.rs`, lines 101-102.

The path `root.join(&rel)` is constructed twice in the error-handling arm (once for `fs::read`, once for the `.map_err` closure). This allocates a `PathBuf` twice. Binding it to a local variable eliminates the duplicate allocation:

```rust
let full_path = root.join(&rel);
let bytes = fs::read(&full_path).map_err(|err| AgentpackError::io(full_path, err))?;
```

---

## Issue 6 (Low, snippet only): `WalkDir` without `follow_links(false)` in the provided snippet

The snippet's `hash_directory_contents` uses `WalkDir::new(dir)` without `follow_links(false)`. The actual codebase correctly sets `follow_links(false)`. Following symlinks risks infinite loops on circular links and hashing content outside the package directory. The codebase is correct; the snippet is not.

---

## Issue 7 (Info): Snippet vs. actual `copy_tree_files` divergence

The snippet uses `WalkDir`-based flat iteration. The actual implementation in `src/cache/tree.rs` uses recursive `fs::read_dir` with `resolve_tree_copy_source` (symlink-aware). The recursive approach is fine for typical package depths, but very deep trees could stack-overflow. The `WalkDir` approach in the snippet avoids that. Neither is a practical concern for skill/plugin packages.

---

## Summary

| # | Issue | Impact | Effort |
|---|-------|--------|--------|
| 1 | Double directory traversal + double file read | High | Medium |
| 2 | Unconditional delete-and-recopy of content-addressed cache | Medium | Low |
| 3 | Full file slurp for hashing instead of streaming | Medium | Low |
| 4 | Redundant `create_dir_all` | Low | Trivial |
| 5 | Duplicate `root.join(&rel)` allocation | Low | Trivial |
| 6 | Missing `follow_links(false)` in snippet | Low (snippet only) | Trivial |

**Highest-value fix:** Issue 2 (early return on cache hit) -- eliminates all copy work for unchanged packages with a one-line change. Issue 1 (single-pass hash+copy) provides the biggest improvement for cold-cache or changed-package scenarios.
