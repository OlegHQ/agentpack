## Performance Analysis: resolve_all / resolve_single

### Critical (fix before merge)

- [COMPLEXITY] resolve_all:4-6 -- `resolved.iter().any(|r| r.name == dep.name)` inside a loop over `deps` (which is itself inside a loop over `packages`) is O(n*m) per outer iteration, O(n*m^2) overall where n = number of packages and m = average dependency count. As `resolved` grows, every membership check scans the entire vector. **Replace `resolved` with a `HashSet<String>` for O(1) name lookups** (keep the `Vec` for ordered output, use the set only for the membership test).

- [COMPLEXITY] resolve_all:7-11 -- The same linear scan pattern repeats for sub-dependencies: `resolved.iter().any(|r| r.name == sub.name)` inside a nested loop. With three levels of nesting (packages -> deps -> sub_deps), worst-case complexity is O(n * m * k * |resolved|) where k = sub-dependency count. This is effectively **O(n^3)** when dependency graphs are dense. A single `HashSet<String>` for "already resolved" names eliminates all three linear scans.

- [SYSCALL] resolve_single:1-2 -- `fs::read_to_string(&pkg.manifest_path)` is called **every time** a package is resolved, including duplicates that pass the name check but whose transitive dependencies trigger re-resolution. The same manifest file may be read multiple times across different call sites. **Cache parsed manifests** in a `HashMap<PathBuf, (String, String)>` to avoid redundant filesystem reads and JSON parsing.

- [SYSCALL] resolve_all:4,8 -- `fetch_dependencies(&pkg.name)` and `fetch_dependencies(&dep.name)` are called unconditionally, even when the package is already in `resolved`. The fetch (likely a network or filesystem call) should be **skipped entirely** when the name is already in the resolved set. Move the membership check **before** the fetch call:
  ```rust
  if seen.contains(&pkg.name) { continue; }
  let deps = fetch_dependencies(&pkg.name)?;
  ```

### Significant (worth addressing)

- [ALLOC] resolve_single:3-4 -- `parsed["name"].as_str().unwrap_or("").to_string()` and the same for `version` each allocate a new `String`. When called thousands of times in the inner loops, these allocations add up. Consider using `serde::Deserialize` with a `#[derive(Deserialize)]` struct to avoid the intermediate `serde_json::Value` (which itself heap-allocates every JSON value) and parse directly into owned fields.

- [ALLOC] resolve_single:2 -- `serde_json::from_str(&metadata)` parses into a `serde_json::Value`, which allocates a `Map`, `Vec`, and `String` for every node in the JSON tree. If the manifest only needs `name` and `version`, this is vastly over-parsing. Define a minimal struct:
  ```rust
  #[derive(Deserialize)]
  struct ManifestSlim { name: String, version: String }
  ```
  and use `serde_json::from_str::<ManifestSlim>(&metadata)` to skip allocating unused fields.

- [ALLOC] resolve_all:12,14 -- `resolve_single(dep)` and `resolve_single(pkg)` each call `pkg.path.clone()`. If `path` is a `PathBuf` or `String`, each clone is a heap allocation. In hot loops with many packages, this adds up. Consider using `Arc<Path>` or restructuring so the resolved package borrows the path.

- [MEMORY] resolve_all:1 -- `Vec::new()` for `resolved` starts at capacity 0 and will reallocate as it grows. If the approximate number of total packages (direct + transitive) is known or estimable, use `Vec::with_capacity(packages.len() * estimated_avg_deps)` to avoid ~11 reallocations to reach 2048 elements.

### Minor (nice to have)

- [ITER] resolve_all -- The three-level nesting (packages -> deps -> sub_deps) only resolves two levels of transitive dependencies. If the dependency graph is deeper than two levels, packages at depth 3+ are silently dropped. This is a correctness issue more than a performance one, but fixing it with a **worklist/BFS pattern** would also simplify the code and make the deduplication logic cleaner:
  ```rust
  let mut queue = VecDeque::from(packages.to_vec());
  let mut seen = HashSet::new();
  let mut resolved = Vec::with_capacity(packages.len() * 4);
  while let Some(pkg) = queue.pop_front() {
      if !seen.insert(pkg.name.clone()) { continue; }
      let deps = fetch_dependencies(&pkg.name)?;
      queue.extend(deps);
      resolved.push(resolve_single(&pkg)?);
  }
  ```

- [ALLOC] resolve_all -- The `name` field comparisons (`r.name == dep.name`) create no allocations themselves, but if `name` is a `String`, hashing it for a `HashSet` is cheaper than comparing it against every element. Already covered by the critical HashSet recommendation above.

### Estimated impact

- **Current**: O(n * m * k) complexity with linear membership checks making effective complexity O(n * m * k * r) where r = size of resolved set. Each resolve_single call does 1 filesystem read + 1 JSON parse. fetch_dependencies called even for already-resolved packages (wasted network/fs calls).
- **After fixes**: O(n_total) with HashSet lookups, where n_total = total unique packages. Manifest reads cached, fetch calls skipped for known packages. JSON parsing reduced to only needed fields.
- **Expected speedup**: ~10-50x for dependency graphs with 100+ packages, dominated by eliminating redundant fetch_dependencies calls and replacing O(n) scans with O(1) HashSet lookups. For small graphs (<20 packages), ~2-3x from avoiding redundant I/O alone.
