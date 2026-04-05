# copy_tree Benchmark Analysis

## Task

Write a benchmark testing directory-tree copy performance at 10, 100, and 1000 files, matching the `copy_tree_files(from: &Path, to: &Path) -> Result<()>` signature from `src/cache/tree.rs`.

## What was done

Created `/Users/snowbear/WORK/GIT/agentpack/benches/copy_tree.rs` -- a benchmark binary using `divan` that:

1. Re-implements the same recursive copy algorithm used by `copy_tree_files` (symlink resolution, recursive dir walk, `fs::copy` for files) without internal crate dependencies.
2. Builds synthetic source trees with files distributed across realistic subdirectories (`commands/`, `skills/analysis/`, `hooks/`, `agents/`, `rules/project/`) using ~300-byte markdown files with frontmatter.
3. Uses `divan::bench(args = [10, 100, 1000])` to parameterize file counts, with source tree built once outside the timed region.
4. Each iteration creates a fresh destination `TempDir`, copies the tree, and returns it so cleanup is outside the measured scope.

Added `divan = "0.1"` to `[dev-dependencies]` and a `[[bench]]` section to `Cargo.toml` with `harness = false`.

## Results

```
Timer precision: 41 ns
copy_tree             fastest       | slowest       | median        | mean          | samples | iters
|- copy_tree_n_files                |               |               |               |         |
   |- 10              2.234 ms      | 14.69 ms      | 2.51 ms       | 2.648 ms      | 100     | 100
   |- 100             15.9 ms       | 19.75 ms      | 18.06 ms      | 18.03 ms      | 100     | 100
   +- 1000            168.3 ms      | 205.3 ms      | 182.4 ms      | 183.9 ms      | 100     | 100
```

## Key findings

- **Linear scaling confirmed.** 10x more files produces roughly 10x more time at each step:
  - 10 -> 100 files: median 2.51 ms -> 18.06 ms (7.2x for 10x files)
  - 100 -> 1000 files: median 18.06 ms -> 182.4 ms (10.1x for 10x files)
  - Overall 10 -> 1000: median 2.51 ms -> 182.4 ms (72.7x for 100x files, exponent ~1.0)
- **Per-file cost is ~180 us** (median 182.4 ms / 1000 files), dominated by filesystem syscalls (`create_dir_all`, `fs::copy`, `read_dir`).
- **Variance is moderate** -- the 10-file case shows occasional outliers (14.69 ms max vs 2.51 ms median, likely OS filesystem cache effects). The 100 and 1000 file cases are more stable (max within ~15% of median).
- **No algorithmic bottleneck.** The recursive approach with per-entry `fs::copy` is I/O-bound. Potential optimizations if needed:
  - Parallel file copies (rayon/tokio) could help for large trees.
  - Batching `create_dir_all` calls by pre-scanning the directory structure.
  - Using platform-specific bulk copy APIs (e.g., `clonefile` on macOS APFS).

## Files

- Benchmark: `/Users/snowbear/WORK/GIT/agentpack/benches/copy_tree.rs`
- Cargo.toml: added `divan = "0.1"` dev-dependency and `[[bench]] name = "copy_tree"` with `harness = false`
