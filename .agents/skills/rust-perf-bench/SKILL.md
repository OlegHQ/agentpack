---
name: rust-perf-bench
description: |
  Designs benchmarks, profiles bottlenecks, and applies targeted optimizations to Rust code. Use this skill when the user wants to measure performance, write benchmarks, profile with flamegraphs, find bottlenecks, speed up slow operations, reduce memory usage, or set up CI performance regression testing. Triggers on: "benchmark", "profile", "flamegraph", "slow", "speed up", "how long does", "measure performance", "regression test", "criterion", "divan", "perf", "too slow", or any request to make Rust code faster with evidence. If someone says something takes too long and wants to fix it, this skill applies.
---

# Rust Performance Benchmarking & Optimization

You have a slow operation and you want to make it fast. Or you have a fast operation and you want to keep it that way. Either way, the process is the same: **measure, identify, fix, verify.** Never optimize without a benchmark proving it helped.

## Step 0: Quick Triage (do this first, takes 2 minutes)

Before writing any benchmarks, get a rough picture of where time goes:

```bash
# Wall-clock timing of the slow operation (simplest possible measurement)
time cargo run --release -- sync

# Syscall count (Linux) — high counts point to I/O-heavy hotspots
strace -c cargo run --release -- sync 2>&1 | tail -20

# Syscall count (macOS)
sudo dtruss -c cargo run --release -- sync 2>&1 | tail -20

# CPU flamegraph — install once: cargo install flamegraph
cargo flamegraph --bin myapp -- sync
# Open flamegraph.svg in browser. Wide boxes = where time goes.
```

If the operation involves file I/O, check the syscall profile first. A function that does 500 `open`+`read`+`close` sequences for 2MB total data is spending more time in kernel transitions than in actual computation.

## Step 1: Identify What to Benchmark

Not everything needs benchmarking. Focus on:

- **User-facing latency** — CLI commands, API response times, build steps
- **Hot paths** — Functions called thousands+ times per operation
- **I/O-heavy operations** — File system traversal, network calls, serialization
- **Suspected bottlenecks** — Code the user explicitly says is slow

Ask: "If this function took 0ms, would the user notice?" If no, don't benchmark it.

## Step 2: Write Benchmarks

### Choosing a Framework

**Use divan** for new projects — it's simpler, has better ergonomics, and produces clean output:

```rust
// benches/my_bench.rs
use divan::Bencher;

fn main() {
    divan::main();
}

#[divan::bench]
fn resolve_dependencies(bencher: Bencher) {
    // Setup OUTSIDE the benchmark loop
    let manifest = load_test_manifest();
    
    bencher.bench_local(|| {
        resolve_lock_from_manifest(&manifest)
    });
}

// Parameterized benchmarks for scaling analysis
#[divan::bench(args = [10, 100, 1000, 10000])]
fn process_n_items(bencher: Bencher, n: usize) {
    let items = generate_items(n);
    bencher.bench_local(|| {
        process_items(&items)
    });
}
```

**Use criterion** when you need HTML reports, statistical comparison, or CI integration:

```rust
// benches/my_bench.rs
use criterion::{criterion_group, criterion_main, Criterion, BenchmarkId};

fn bench_resolve(c: &mut Criterion) {
    let manifest = load_test_manifest();
    
    c.bench_function("resolve_lock", |b| {
        b.iter(|| resolve_lock_from_manifest(&manifest))
    });
}

// Scaling analysis
fn bench_scaling(c: &mut Criterion) {
    let mut group = c.benchmark_group("process_items");
    for n in [10, 100, 1000, 10000] {
        let items = generate_items(n);
        group.bench_with_input(
            BenchmarkId::from_parameter(n),
            &items,
            |b, items| b.iter(|| process_items(items)),
        );
    }
    group.finish();
}

criterion_group!(benches, bench_resolve, bench_scaling);
criterion_main!(benches);
```

**Cargo.toml setup:**

```toml
[dev-dependencies]
divan = "0.1"      # or criterion = "0.5"

[[bench]]
name = "my_bench"
harness = false
```

### Benchmark Design Principles

- **Isolate the code under test** — Setup and teardown happen outside `bench_local`/`iter`. If your benchmark measures file I/O, create temp files in setup.
- **Use `black_box()`** — Prevents the compiler from optimizing away the result: `black_box(compute_thing())`.
- **Test at realistic scale** — Benchmark with the same data sizes users encounter. A benchmark with 5 items won't reveal an O(n^2) that kills you at 5000.
- **Parameterize for scaling** — Run at multiple N values. If time grows faster than linearly, you have a complexity problem.
- **Benchmark the whole operation** — Don't only micro-benchmark internal functions. Also benchmark the full user-facing operation end-to-end.

## Step 3: Profile to Find Bottlenecks

### Flamegraph (CPU time)

```bash
# Install once
cargo install flamegraph

# Profile a binary
cargo flamegraph --bin agentpack -- sync

# Profile a specific benchmark
cargo flamegraph --bench my_bench -- --bench "resolve_lock"

# On macOS (needs dtrace, may require SIP adjustment)
sudo cargo flamegraph --root --bin agentpack -- sync
```

**Cargo.toml for useful flamegraphs** — release builds strip debug info by default, which makes flamegraphs unreadable:

```toml
[profile.release]
debug = 1  # line tables only, minimal size impact

[profile.bench]
debug = 1
```

**Reading flamegraphs:**
- **Wide boxes** = functions consuming lots of CPU time. These are your targets.
- **Tall stacks** = deep call chains. Look for unnecessary abstraction layers.
- **Flat tops** = leaf functions doing actual work. If a leaf is wide, optimize its algorithm.
- **Look for surprises** — Is 40% of time in `serde_json::from_str`? Maybe you're parsing the same file repeatedly. Is `memcpy` dominating? You're cloning too much.

### Memory Profiling

**DHAT (heap allocation profiler):**

```rust
// Temporarily add to main.rs for profiling
#[global_allocator]
static ALLOC: dhat::Alloc = dhat::Alloc;

fn main() {
    let _profiler = dhat::Profiler::new_heap();
    // ... rest of main
}
```

```bash
cargo run --release --features dhat-heap -- sync
# Opens dhat-viewer with allocation sites, counts, and sizes
```

**What to look for in DHAT output:**
- **High allocation count** in a single call site = hot loop allocating
- **Large total bytes** from many small allocations = consider arena allocation or pre-allocation
- **Short-lived allocations** = allocate-use-drop pattern that could be stack-allocated or reused

**heaptrack** (Linux):

```bash
heaptrack cargo run --release -- sync
heaptrack_gui heaptrack.agentpack.*.gz
```

### Syscall Tracing

**Linux (strace):**
```bash
strace -c cargo run --release -- sync 2>&1 | tail -20
# Shows syscall counts and time spent in each

strace -e trace=open,openat,stat,read,write -c cargo run --release -- sync
# Filter to filesystem calls only
```

**macOS (dtrace/dtruss):**
```bash
sudo dtruss -c cargo run --release -- sync 2>&1 | tail -20
```

**What to look for:**
- **High `stat`/`lstat` counts** = checking file existence in loops
- **Many `open`+`close` pairs** = opening files one at a time instead of batching
- **`write` called thousands of times** = unbuffered output (need `BufWriter`)

## Step 4: Apply Targeted Optimizations

Once you know where the time goes, apply fixes from this playbook:

### I/O Batching

```rust
// BAD: N syscalls
for path in &paths {
    if path.exists() {  // stat syscall
        let data = fs::read(path)?;  // open + read + close
        process(data);
    }
}

// GOOD: N/2 syscalls, simpler
for path in &paths {
    match fs::read(path) {
        Ok(data) => process(data),
        Err(_) => continue,
    }
}

// BETTER: parallel with rayon
use rayon::prelude::*;
let results: Vec<_> = paths.par_iter()
    .filter_map(|path| fs::read(path).ok())
    .collect();
```

### Allocation Reduction

```rust
// BAD: allocates on every iteration
let mut results = Vec::new();
for item in &items {
    results.push(format!("{}: {}", item.name, item.value));
}

// GOOD: pre-allocate + avoid format where possible
let mut results = Vec::with_capacity(items.len());
for item in &items {
    let mut s = String::with_capacity(item.name.len() + 2 + 10);
    s.push_str(&item.name);
    s.push_str(": ");
    s.push_str(&item.value.to_string());
    results.push(s);
}

// BETTER: if you only need it for display, don't allocate strings at all
use std::io::{Write, BufWriter};
let mut out = BufWriter::new(std::io::stdout().lock());
for item in &items {
    writeln!(out, "{}: {}", item.name, item.value)?;
}
```

### Parallelism

```rust
// Sequential file processing
let results: Vec<_> = paths.iter()
    .map(|p| process_file(p))
    .collect::<Result<Vec<_>>>()?;

// Parallel with rayon (one-line change for embarrassingly parallel work)
use rayon::prelude::*;
let results: Vec<_> = paths.par_iter()
    .map(|p| process_file(p))
    .collect::<Result<Vec<_>>>()?;

// Parallel directory walking (ripgrep's approach)
use ignore::WalkBuilder;
WalkBuilder::new(root)
    .threads(num_cpus::get())
    .build_parallel()
    .run(|| Box::new(|entry| { /* process entry */ WalkState::Continue }));
```

### Caching & Memoization

```rust
// BAD: resolves the same ref multiple times
for dep in &deps {
    let commit = resolve_ref(&dep.git_ref)?;  // network call each time
    process(dep, &commit);
}

// GOOD: cache results
use std::collections::HashMap;
let mut ref_cache: HashMap<String, String> = HashMap::new();
for dep in &deps {
    let commit = ref_cache.entry(dep.git_ref.clone())
        .or_insert_with(|| resolve_ref(&dep.git_ref).unwrap());
    process(dep, commit);
}
```

### Lazy Evaluation

```rust
// BAD: computes expensive thing even if not needed
fn process(item: &Item) -> Result<Output> {
    let analysis = expensive_analysis(item);  // always runs
    if item.is_simple() {
        return Ok(simple_output(item));
    }
    Ok(complex_output(item, &analysis))
}

// GOOD: defer computation
fn process(item: &Item) -> Result<Output> {
    if item.is_simple() {
        return Ok(simple_output(item));
    }
    let analysis = expensive_analysis(item);  // only when needed
    Ok(complex_output(item, &analysis))
}
```

### Faster Hashing

```rust
// BAD: SHA-256 for non-security content fingerprinting (~250 MB/s)
use sha2::{Sha256, Digest};
let hash = Sha256::digest(&data);

// GOOD: blake3 for content addressing (~5 GB/s, parallelizable)
let hash = blake3::hash(&data);

// GOOD: xxhash for hash maps / dedup (~15 GB/s for small inputs)  
use xxhash_rust::xxh3::xxh3_64;
let hash = xxh3_64(&data);
```

If the hash is for cache keys, file deduplication, or content addressing — not cryptographic signatures — use `blake3` or `xxhash`. They're 5-20x faster than SHA-256 and just as collision-resistant for these use cases.

### Mtime-Based Cache Invalidation

```rust
// BAD: re-hashes all files on every invocation
fn content_hash(dir: &Path) -> String {
    // walks 500 files, reads 2MB, takes 60% of CPU time
    hash_all_files(dir)
}

// GOOD: check mtime first, skip hashing if nothing changed
fn content_hash_cached(dir: &Path, cache: &mut HashMap<PathBuf, (SystemTime, String)>) -> String {
    let mtime = fs::metadata(dir).ok().and_then(|m| m.modified().ok());
    if let Some((cached_mtime, cached_hash)) = cache.get(dir) {
        if mtime.as_ref() == Some(cached_mtime) {
            return cached_hash.clone();  // 1 stat instead of 500 reads
        }
    }
    let hash = hash_all_files(dir);
    if let Some(mtime) = mtime {
        cache.insert(dir.to_path_buf(), (mtime, hash.clone()));
    }
    hash
}
```

### Zero-Copy Patterns

```rust
// BAD: copies the string
fn find_name(input: &str) -> String {
    input.split('/').last().unwrap_or("").to_string()
}

// GOOD: borrows from input
fn find_name(input: &str) -> &str {
    input.split('/').last().unwrap_or("")
}

// When ownership is sometimes needed: Cow
use std::borrow::Cow;
fn normalize_path(input: &str) -> Cow<'_, str> {
    if input.contains('\\') {
        Cow::Owned(input.replace('\\', "/"))
    } else {
        Cow::Borrowed(input)  // no allocation in the common case
    }
}
```

## Step 5: Verify the Fix

After applying optimizations:

```bash
# Run benchmarks and compare to baseline
cargo bench  # criterion saves baselines automatically

# For divan, compare output manually or use:
cargo bench -- --filter "resolve_lock"

# Check that tests still pass
cargo test

# Profile again to confirm the bottleneck moved
cargo flamegraph --bin agentpack -- sync
```

**What to report:**

```
## Benchmark Results: <optimization description>

### Before
- resolve_lock: 847ms (±12ms)
- cache_copy: 234ms (±8ms)  
- Total sync: 37.2s

### After  
- resolve_lock: 123ms (±4ms)  [-85%]
- cache_copy: 45ms (±2ms)   [-81%]
- Total sync: 4.1s           [-89%]

### What changed
- Replaced sequential fs::metadata per file with parallel walkdir (SYSCALL reduction)
- Pre-allocated Vec with capacity hint (ALLOC reduction)  
- Cached resolved git refs across dependencies (NETWORK reduction)

### Regression risk
- None: all existing tests pass, behavior unchanged
```

## CI Regression Testing

### With criterion + GitHub Actions

```yaml
# .github/workflows/bench.yml
name: Benchmark
on: [pull_request]
jobs:
  bench:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions-rs/toolchain@v1
      - name: Run benchmarks
        run: cargo bench -- --output-format bencher | tee bench_output.txt
      - name: Compare with baseline
        uses: benchmark-action/github-action-benchmark@v1
        with:
          tool: cargo
          output-file-path: bench_output.txt
          alert-threshold: '120%'  # fail if >20% regression
          comment-on-alert: true
```

### Quick Regression Check (no CI)

```bash
# Save a baseline
cargo bench -- --save-baseline main

# After changes
cargo bench -- --baseline main
# Criterion shows % change with statistical significance
```

## Reference: Profiling Tool Selection

| Goal | Tool | Platform | Command |
|------|------|----------|---------|
| CPU hotspots | flamegraph | All | `cargo flamegraph --bin X -- args` |
| CPU hotspots (sampling) | samply | macOS/Linux | `samply record cargo run --release -- args` |
| Heap allocations | DHAT | All | Add `dhat` crate, run normally |
| Heap timeline | heaptrack | Linux | `heaptrack cargo run --release -- args` |
| Cache misses | cachegrind | Linux | `valgrind --tool=cachegrind cargo run --release -- args` |
| Syscall counts | strace | Linux | `strace -c cargo run --release -- args` |
| Syscall counts | dtruss | macOS | `sudo dtruss -c cargo run --release -- args` |
| Lock contention | Instruments | macOS | Time Profiler instrument |
| Async task timing | tokio-console | All | Add `console-subscriber` crate |

## Case Studies to Reference

- **ripgrep**: Lock-free parallel directory walker via `ignore` crate. Per-thread buffers merged at end. 5-12x faster than grep because parallelism + gitignore filtering + skipping binary files.
- **Bun install**: 165K syscalls vs npm's 997K. Uses `clonefile()` on macOS (CoW, one syscall per tree), hardlinks on Linux, lock-free work-stealing thread pool, per-thread memory pools.
- **Turbopack**: Incremental memoization engine. Caches function results at cell level, only recomputes dirty subgraphs. 1000 modules rebuild in ~50ms because most computation is skipped.
- **TiKV**: Custom memory allocator tuning (jemalloc with tcache), batch I/O coalescing, careful enum sizing to fit cache lines.
