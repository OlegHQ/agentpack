---
name: rust-perf-analyzer
description: |
  Analyzes Rust code for algorithmic complexity, memory inefficiency, and syscall overhead before writing or during refactoring. Use this skill whenever implementing new Rust features, reviewing existing Rust code for performance, refactoring hot paths, or when the user mentions "slow", "performance", "optimize", "allocation", "clone", "copy", "memory", "cache", "syscall", or "bottleneck". Triggers on ANY Rust implementation or review task where performance matters — which is most of them. If you're about to write a loop, allocate a collection, do file I/O, or make network calls in Rust, this skill applies.
---

# Rust Performance Analyzer

Performance problems compound silently. A single unnecessary `.clone()` in a hot loop, a quadratic scan nobody notices until the dataset grows, an `fs::read_dir` that could be a single `openat` — these are the costs that turn a 200ms operation into 37 seconds. This skill teaches you to see these costs before they ship.

The philosophy is simple: **every allocation is a choice, every syscall is a negotiation with the kernel, and every algorithm has a complexity class you should know before you write it.**

## When Implementing New Code

Before writing, ask three questions:

1. **What's the hot path?** — Which functions run per-item, per-request, or per-iteration? Those get scrutiny. Cold paths (startup, config loading) get a pass.
2. **What's the data volume?** — 10 items? Write whatever is readable. 10,000+? Think about complexity. 1M+? Think about memory layout and cache lines.
3. **How many kernel transitions?** — Each `fs::metadata`, `fs::read`, `Path::exists` check is a syscall. Batch them. Bun's package installer does ~165K syscalls where npm does ~997K for the same operation — that's the difference between 0.5s and 30s.

## Complexity Checklist

Run through this checklist for any function on a hot path:

### Algorithmic Complexity

- **Nested iteration over the same or related collections** — `for x in &items { for y in &items { ... } }` is O(n^2). If you're checking membership, use a `HashSet`. If you're matching pairs, sort first or use an index.
- **Repeated linear scans** — Calling `.find()`, `.contains()`, or `.position()` inside a loop makes it O(n*m). Build a lookup map once.
- **String concatenation in loops** — Each `format!()` or `+` allocates. Use `String::with_capacity()` and `push_str()`, or collect into a `Vec` and `join()`.
- **Growing collections without capacity hints** — `Vec::new()` followed by thousands of pushes reallocates ~11 times to reach 2048 elements. `Vec::with_capacity(n)` does it once.

### Memory & Allocation

- **Unnecessary `.clone()`** — Every clone is a heap allocation + memcpy. Ask: can this be a `&ref`? A `Cow<'_, str>`? An `Arc`? Cloning a `String` inside a loop that runs 50K times is 50K allocations you probably don't need.
- **`String` vs `&str`** — If a function only reads a string, take `&str` or `impl AsRef<str>`, not `String`. Accepting owned types forces callers to clone.
- **`Vec<String>` vs `Vec<&str>`** — Same principle. If you're building a temporary collection for lookup/iteration, borrow where possible.
- **`Box<dyn Trait>` in hot paths** — Dynamic dispatch + heap allocation. If the set of types is known and small, use an enum. Monomorphized generics are free at runtime.
- **Large enum variants** — `std::mem::size_of::<MyEnum>()` equals the size of the largest variant + discriminant. If one variant holds a `Vec<u8>` and another holds a `bool`, every instance pays the `Vec` cost. Box the large variant: `LargeVariant(Box<LargePayload>)`.
- **Struct field ordering** — Rust's default repr reorders fields for optimal alignment, but `#[repr(C)]` doesn't. If you're using repr(C), order fields from largest alignment to smallest to minimize padding. A struct `{a: u8, b: u64, c: u16}` is 24 bytes; `{b: u64, c: u16, a: u8}` is 16 bytes.

### I/O & Syscall Overhead

This is where the biggest wins often hide, especially in CLI tools and package managers:

- **`Path::exists()` before `fs::read()`** — That's two syscalls (stat + open+read). Just call `fs::read()` and handle the `Err`. One syscall instead of two.
- **Stat calls in loops** — `fs::metadata()` per file in a directory listing is O(n) syscalls. On Linux, `getdents64` already returns `d_type` — use it via the `walkdir` or `ignore` crate instead of stat-ing each entry.
- **Sequential file operations when parallelism is possible** — Reading 100 files sequentially is 100 `open` + 100 `read` + 100 `close` = 300 syscalls. If the files are independent, parallelize with `rayon::par_iter()`. For directory hashing/copying, `rayon` can overlap I/O across files while the OS serves from page cache. Even on a single-core machine, overlapping I/O reduces total wall time by hiding syscall latency.
- **Redundant network calls** — HTTP requests to the same host that could be batched or pipelined. Check if the API supports bulk endpoints.
- **Process spawning** — `Command::new("git").arg("...")` per file is catastrophic. Batch arguments: `git ls-files` once, not `git status <file>` N times.
- **Filesystem copy strategies** — On macOS, `clonefile()` (copy-on-write) copies a whole tree in one syscall. On Linux, `copy_file_range()` keeps data in kernel space. Both beat `read()`+`write()` loops. The `fs_extra` or `reflink` crates expose these. Always prefer these over manual `fs::read` + `fs::write` loops.
- **BufWriter / BufReader** — Unbuffered I/O means a syscall per `write!()` or `read()` call. Always wrap in `BufWriter::new()` / `BufReader::new()` for repeated I/O. `println!` inside a loop is a syscall per line — collect and print once, or use a `BufWriter` on stdout.
- **Double directory walks** — A common pattern: walk a directory to hash/checksum files, then walk it again to copy them. Combine into a single pass (hash while copying, or cache the walk results). Two walks = 2N `open`+`read`+`close` sequences. One walk = N.
- **Content-addressed cache skips** — If the cache key is derived from content hashing, an existing cache entry with the same key is guaranteed identical. Skip the copy entirely with an early return when the cache slot exists. This turns repeat operations from O(N) I/O to O(1).

### Hashing & Checksumming

- **SHA-256 for non-security content fingerprinting** — SHA-256 is cryptographically secure but ~3-5x slower than non-crypto alternatives. If you're hashing for cache keys, deduplication, or content addressing (not signatures/verification), consider `blake3` (parallel, ~5x faster than SHA-256), `xxhash` via the `xxhash-rust` crate (~10x faster for small inputs), or `highway` hash.
- **Mtime-based cache invalidation** — Instead of hashing every file on every invocation, check the directory's `mtime` (modification time). If no files have been modified since the last hash, reuse the cached hash. Store `(mtime, hash)` pairs. This turns re-hashing an unchanged 500-file directory from 500 reads to 1 stat call.
- **Streaming hash vs full-file reads** — `fs::read(path)` loads the entire file into a `Vec<u8>` for hashing. Use `BufReader` feeding chunks into the hasher to cap memory at the buffer size (e.g., 64KB) regardless of file size.

### Iterator & Collection Patterns

- **`.collect::<Vec<_>>()` followed by `.iter()`** — You just allocated a vec to iterate it once. Chain the iterators instead.
- **Multiple passes over the same data** — `.iter().filter().count()` then `.iter().filter().collect()` traverses twice. Do it in one pass.
- **`HashMap` for small N** — Below ~20 entries, a sorted `Vec` with binary search is faster due to cache locality. `HashMap` has per-lookup overhead from hashing + bucket resolution.
- **`to_string()` / `to_owned()` in iterator chains** — `.map(|s| s.to_string())` inside a chain that only needs `&str` is allocating for no reason.
- **Sorting when you need top-K** — `vec.sort()` is O(n log n). If you need the top 5 elements, use a `BinaryHeap` or `select_nth_unstable` for O(n).

### Concurrency & Parallelism

- **Lock contention** — `Mutex<Vec<_>>` shared across threads where each thread pushes results is a serialization point. Use per-thread `Vec` and merge at the end, or use a lock-free structure like `crossbeam::queue`.
- **False sharing** — Two `AtomicUsize` in adjacent struct fields share a cache line. When different threads write to each, every write invalidates the other thread's cache. Use `#[repr(align(64))]` padding between them.
- **Thread pool overhead for small batches** — Spawning rayon tasks for 5 items is slower than sequential. Only parallelize when N > ~100 and per-item work is non-trivial.
- **Async where blocking I/O suffices** — `tokio::spawn` + `.await` for CPU-bound work adds scheduling overhead. For CLI tools doing sequential I/O, blocking with `rayon` for parallelism is simpler and often faster.

## When Reviewing / Refactoring

Focus review on:

1. **Hot loops** — Any loop body that runs >1000 times. Every allocation, clone, or syscall inside is amplified.
2. **Collection building** — How are `Vec`, `HashMap`, `String` built? Is capacity pre-allocated? Are there unnecessary intermediate collections?
3. **Error paths** — Formatting error messages with `format!()` allocates. On hot paths, consider static error strings or lazy formatting.
4. **Serialization/deserialization** — `serde_json::from_str` allocates. For repeated parsing of the same schema, consider `serde_json::from_reader` with a `BufReader`, or zero-copy deserialization with `serde_json::from_slice` + `#[serde(borrow)]`.

## Output Format

When analyzing code, produce a **findings report** structured as:

```
## Performance Analysis: <module/function>

### Critical (fix before merge)
- [ALLOC] <file>:<line> — `.clone()` inside loop over N items = N heap allocations. Use `&ref` or `Cow`.
- [COMPLEXITY] <file>:<line> — Nested loop is O(n^2). Build a HashSet for O(n) lookup.

### Significant (worth addressing)
- [SYSCALL] <file>:<line> — `Path::exists()` + `fs::read()` = 2 syscalls per file. Just `fs::read()` and handle Err.
- [MEMORY] <file>:<line> — `Vec<String>` could be `Vec<&str>` since strings outlive the collection.

### Minor (nice to have)
- [ITER] <file>:<line> — `.collect::<Vec<_>>()` immediately iterated. Chain iterators instead.

### Estimated impact
- Current: ~O(n^2) with ~3n syscalls per invocation
- After fixes: ~O(n log n) with ~n syscalls
- Expected speedup: ~5-10x for n > 1000
```

Severity classification:
- **Critical**: O(n^2)+ complexity, allocations in hot loops, unbounded growth
- **Significant**: Unnecessary syscalls, redundant allocations, suboptimal data structures
- **Minor**: Style improvements with marginal perf benefit
