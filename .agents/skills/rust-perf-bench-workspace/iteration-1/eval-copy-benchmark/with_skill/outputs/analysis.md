## Benchmark Results: copy_tree_files scaling analysis

### Setup

- **Function under test:** `copy_tree_files(from: &Path, to: &Path) -> Result<()>` — recursive directory copy with symlink resolution, used to populate the content-addressed cache.
- **Framework:** divan 0.1 (parameterized benchmark with `args = [10, 100, 1000]`).
- **File layout:** Files distributed across 5 subdirectories (`commands/`, `skills/analysis/`, `hooks/`, `agents/`, `rules/project/`) to simulate realistic plugin/skill package structure. Each file is ~300 bytes of markdown content.
- **Benchmark file:** `benches/copy_tree.rs`
- **The copy function is replicated in the benchmark** because the original is `pub(super)` in `src/cache/tree.rs`. The logic is identical: `symlink_metadata` check, `canonicalize` for symlinks, `create_dir_all` + `fs::copy` for files, recursive `read_dir` for directories.

### Results

| File count | Fastest    | Slowest    | Median     | Mean       | Samples |
|------------|------------|------------|------------|------------|---------|
| 10         | 2.237 ms   | 14.79 ms   | 2.497 ms   | 2.647 ms   | 100     |
| 100        | 15.96 ms   | 19.79 ms   | 17.97 ms   | 18.01 ms   | 100     |
| 1000       | 167.9 ms   | 206.9 ms   | 182.5 ms   | 183.7 ms   | 100     |

### Scaling Analysis

| Transition     | File ratio | Time ratio (median) | Scaling exponent |
|----------------|------------|---------------------|------------------|
| 10 -> 100      | 10x        | 7.2x                | 0.86             |
| 100 -> 1000    | 10x        | 10.2x               | 1.01             |
| 10 -> 1000     | 100x       | 73.1x               | 0.93             |

**Overall scaling exponent: ~0.93 (sub-linear to linear).**

The function scales linearly with file count. The 10-file case shows slightly higher per-file cost due to fixed overhead (creating the 5 subdirectories, initial `create_dir_all` calls). At 100+ files the cost settles to ~180 us/file.

### Per-file cost breakdown

| File count | Median per file |
|------------|-----------------|
| 10         | 250 us          |
| 100        | 180 us          |
| 1000       | 183 us          |

### Observations

1. **Linear scaling confirmed** -- no O(n^2) pathology. The recursive `read_dir` + `fs::copy` approach scales as expected.
2. **Syscall-heavy** -- each file requires at minimum: `symlink_metadata` (1), `create_dir_all` (1+), `fs::copy` which internally does `open` + `read` + `write` + `close` (4). For 1000 files that's roughly 6000+ syscalls.
3. **Variance is moderate** -- the 10-file case has a 6.6x fastest-to-slowest spread (likely OS cache effects on the first iteration), but 100 and 1000 file cases are stable (1.2x spread).
4. **~183 ms for 1000 files** is acceptable for a cache population operation that runs infrequently, but would be worth optimizing if called on every `sync`.

### Potential optimizations (if needed)

- **Parallel copy with rayon:** `read_dir` entries processed via `par_iter` could cut wall time ~2-4x on multi-core for the 1000-file case.
- **Reduce `create_dir_all` calls:** Pre-collect the set of unique parent directories and create them in one pass before copying files, avoiding redundant `mkdir` syscalls.
- **macOS `clonefile()`:** For same-filesystem copies, `clonefile()` is a single syscall that creates a copy-on-write clone instantly. Would eliminate nearly all I/O for the common case.
- **Buffered metadata:** Cache `symlink_metadata` results during directory walk instead of calling it per-entry after `read_dir` already provides `DirEntry::file_type()`.

### How to run

```bash
cargo bench --bench copy_tree
```
