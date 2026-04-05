# Performance Analysis: `resolve_all` Dependency Resolver

## Issues Found

### 1. O(n^2) duplicate checking via linear scan

```rust
if !resolved.iter().any(|r: &ResolvedPackage| r.name == dep.name)
```

Every deduplication check walks the entire `resolved` vector. With `n` total packages this is O(n) per check, and checks happen for every dependency encountered, yielding O(n^2) overall for deduplication alone.

**Fix:** Use a `HashSet<String>` (or `HashSet` keyed on the name) alongside the `Vec` to get O(1) membership tests.

```rust
let mut seen = HashSet::new();
// ...
if seen.insert(dep.name.clone()) {
    resolved.push(resolve_single(dep)?);
}
```

### 2. Redundant `fetch_dependencies` calls (no caching)

`fetch_dependencies` is called for every dependency encountered, even if the same package name appears as a transitive dependency of multiple parents. There is no memoization, so the same network or filesystem fetch can execute many times.

**Fix:** Cache results in a `HashMap<String, Vec<Package>>`:

```rust
let mut dep_cache: HashMap<String, Vec<Package>> = HashMap::new();
// ...
let deps = dep_cache
    .entry(pkg.name.clone())
    .or_insert_with(|| fetch_dependencies(&pkg.name).unwrap_or_default())
    .clone();
```

### 3. Only two levels of transitive resolution

The code resolves direct dependencies and their immediate sub-dependencies, but never recurses further. A dependency chain of depth 3 or more is silently truncated. This is a correctness bug that masquerades as a performance choice: fixing it (with recursion or a work queue) changes the performance profile and must be paired with the deduplication fix above to avoid exponential blowup.

**Fix:** Use an iterative work queue (BFS) or recursive DFS with the `seen` set:

```rust
let mut queue: VecDeque<Package> = packages.iter().cloned().collect();
let mut seen = HashSet::new();
let mut resolved = Vec::new();

while let Some(pkg) = queue.pop_front() {
    if !seen.insert(pkg.name.clone()) {
        continue;
    }
    let deps = fetch_dependencies(&pkg.name)?;
    for dep in deps {
        if !seen.contains(&dep.name) {
            queue.push_back(dep);
        }
    }
    resolved.push(resolve_single(&pkg)?);
}
```

### 4. Repeated filesystem I/O and JSON parsing in `resolve_single`

Every call to `resolve_single` reads and parses the manifest file from disk. If the same package is processed more than once (which the dedup check is meant to prevent but does not guarantee across different call sites), the file is read and parsed again.

Even with perfect deduplication, `resolve_single` re-reads the manifest that `fetch_dependencies` presumably already examined. Sharing parsed data between the two would eliminate redundant I/O.

### 5. `resolve_single` silently swallows missing fields

```rust
let name = parsed["name"].as_str().unwrap_or("").to_string();
```

This is not a performance issue per se, but an empty name means the `HashSet`/linear-scan dedup will treat all packages with missing names as identical, causing silent data loss. With the linear scan approach this can also cause the wrong package to "win" the dedup check, leading to downstream resolution errors and wasted re-resolution attempts.

### 6. String cloning on every comparison

The `any()` closure compares `r.name == dep.name` by value on `String`. While individual comparisons are fast, doing this n times per candidate compounds. The `HashSet` fix from item 1 eliminates this entirely.

### 7. No parallelism for independent fetches

All `fetch_dependencies` calls are sequential. When fetching from the network (GitHub API, etc.), independent packages could be resolved concurrently. With `tokio` or `rayon`:

```rust
let results: Vec<_> = deps
    .par_iter()
    .map(|dep| fetch_dependencies(&dep.name))
    .collect::<Result<Vec<_>>>()?;
```

### 8. Wrong resolution order (post-order violation)

Dependencies are pushed into `resolved` *before* the package that depends on them only by accident of loop structure, but the top-level `pkg` is always pushed last regardless of whether its dependencies were already present. This means if package A appears as both a direct package and a transitive dependency of an earlier package, it gets pushed twice (the dedup check at the outer level does not guard the final `resolved.push(resolve_single(pkg)?)`). This duplicates work and produces an incorrect result vector.

## Summary Table

| # | Issue | Severity | Category |
|---|-------|----------|----------|
| 1 | O(n^2) linear-scan dedup | High | Performance |
| 2 | No fetch caching | High | Performance / I/O |
| 3 | Only 2-level resolution | High | Correctness |
| 4 | Redundant manifest reads | Medium | I/O |
| 5 | Silent empty-name collisions | Medium | Correctness |
| 6 | String cloning in hot loop | Low | Performance |
| 7 | No parallelism | Medium | Performance |
| 8 | Missing top-level dedup guard | Medium | Correctness |

## Recommended Rewrite Sketch

```rust
use std::collections::{HashSet, VecDeque};

pub fn resolve_all(packages: &[Package]) -> Result<Vec<ResolvedPackage>> {
    let mut queue: VecDeque<Package> = packages.iter().cloned().collect();
    let mut seen = HashSet::new();
    let mut resolved = Vec::new();
    let mut dep_cache: HashMap<String, Vec<Package>> = HashMap::new();

    while let Some(pkg) = queue.pop_front() {
        if !seen.insert(pkg.name.clone()) {
            continue;
        }

        let deps = match dep_cache.entry(pkg.name.clone()) {
            Entry::Occupied(e) => e.get().clone(),
            Entry::Vacant(e) => {
                let fetched = fetch_dependencies(&pkg.name)?;
                e.insert(fetched.clone());
                fetched
            }
        };

        for dep in deps {
            if !seen.contains(&dep.name) {
                queue.push_back(dep);
            }
        }

        resolved.push(resolve_single(&pkg)?);
    }

    Ok(resolved)
}
```

This eliminates the quadratic dedup, adds fetch caching, handles arbitrary transitive depth, and prevents duplicate resolution of the top-level packages.
