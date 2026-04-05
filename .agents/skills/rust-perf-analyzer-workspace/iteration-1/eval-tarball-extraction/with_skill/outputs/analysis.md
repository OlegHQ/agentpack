## Performance Analysis: extract_tarball

The function under review extracts a gzip-compressed tarball to a filesystem destination, stripping leading path components. This is used for unpacking GitHub `codeload` tarballs into the agentpack cache. Typical data volumes: tens to hundreds of files per package, tarballs ranging from a few KB to low single-digit MB.

### Critical (fix before merge)

- [ALLOC] line 7 (`entry.path()?.to_path_buf()`) -- Allocates a `PathBuf` for every archive entry. The path is only used to split into components and reassemble. This forces a heap allocation + copy per entry that could be avoided by working with the borrowed `Cow<Path>` returned by `entry.path()` directly, or by using `to_string_lossy()` and splitting the string (as the real codebase implementation does).

- [ALLOC] line 8 (`let components: Vec<_> = path.components().collect()`) -- Collects all path components into a `Vec` on every iteration. This is an unnecessary intermediate allocation. Components can be skipped with `.skip(strip_components)` on the iterator, then collected directly into a `PathBuf` without the intermediate `Vec`.

- [ALLOC] line 17 (`let mut contents = Vec::new(); entry.read_to_end(&mut contents)?; fs::write(&target, &contents)?;`) -- **Double-buffering**: reads the entire file into a heap-allocated `Vec<u8>`, then writes that buffer to disk. This means every file pays for a full heap allocation of its content size. The correct approach is `std::io::copy(&mut entry, &mut file)`, which streams through a small stack buffer (8 KB by default) and never allocates the full file contents on the heap. For a 5 MB file in the archive, this is the difference between a 5 MB allocation vs. an 8 KB stack buffer. The real codebase implementation already uses `std::io::copy` correctly.

### Significant (worth addressing)

- [SYSCALL] line 12 (`fs::create_dir_all(parent)?` on every file entry) -- `create_dir_all` issues multiple `stat` + `mkdir` syscalls, and this is called for **every file entry** even when many files share the same parent directory. For a package with 50 files across 5 directories, this produces ~50 `create_dir_all` calls when ~5 would suffice. Fix: track already-created directories in a `HashSet<PathBuf>` and skip the syscall when the parent was already created. Cost: one `HashSet` lookup (O(1)) vs. multiple kernel transitions per file.

- [ALLOC] line 10 (`let rel: PathBuf = components[strip_components..].iter().collect()`) -- Allocates a new `PathBuf` per entry by collecting component slices. Combined with the `Vec` allocation on line 8, each entry now requires two allocations just for path manipulation. Using `iter().skip(n).collect::<PathBuf>()` directly from the components iterator avoids the intermediate `Vec`, but ideally the path stripping should be done via string slicing (find the Nth `/` and take the substring after it) for zero intermediate allocation.

- [MEMORY] line 17 (`Vec::new()`) -- `Vec::new()` starts with zero capacity. When `read_to_end` fills it, it grows through several reallocations (0 -> 8 -> 16 -> 32 -> ... bytes). If you must buffer (rather than using `io::copy`), pre-allocate with `Vec::with_capacity(entry.size() as usize)` since tar headers contain the entry size. This eliminates all intermediate reallocations.

### Minor (nice to have)

- [ITER] line 14 (`entry.header().entry_type().is_dir()`) -- The directory branch calls `fs::create_dir_all` and increments `count`, but directories are implicitly created by the file branch's `create_dir_all(parent)`. Processing directory entries explicitly is only useful if empty directories must be preserved. If empty dirs are not needed, directory entries can be skipped entirely, reducing syscall count.

- [ALLOC] line 10 -- The `dest.join(&rel)` call allocates a new `PathBuf` on each iteration. This is unavoidable for the join itself, but if `dest` were converted to a `String` once before the loop and path assembly done via string operations, the per-iteration cost would be a single `String` allocation instead of `PathBuf` construction with OS string conversion.

### Estimated impact

- **Current**: O(n) algorithmic complexity (acceptable), but with ~3 heap allocations per entry (path `to_path_buf`, components `Vec`, full file content `Vec`) plus redundant `create_dir_all` syscalls. For an archive with 200 files: ~600 unnecessary heap allocations and ~195 redundant `create_dir_all` syscall chains.
- **After fixes**: Same O(n) complexity, but ~1 allocation per entry (the unavoidable target path) and ~5 `create_dir_all` calls for a typical directory structure. File content streams through a fixed 8 KB stack buffer via `io::copy`.
- **Expected improvement**: ~2-3x reduction in allocator pressure; measurable wall-clock improvement for large archives (1000+ files). The `io::copy` fix is the most impactful single change -- it eliminates peak memory usage proportional to the largest file in the archive and removes per-file allocation overhead entirely.

### Comparison with real codebase

The actual implementation in `src/github/download.rs` (`extract_tarball_with_prefix`) already addresses the most critical finding: it uses `std::io::copy(&mut entry, &mut f)` instead of buffering file contents into a `Vec`. It also avoids `to_path_buf()` by using `to_string_lossy().to_string()` and string splitting rather than `Path::components()`, which is slightly better (one string allocation vs. `PathBuf` + `Vec<Component>`). The redundant `create_dir_all` per-file issue remains in both versions but is acceptable given typical package sizes (tens of files, not thousands).
