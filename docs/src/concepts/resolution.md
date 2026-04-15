# Dependency Resolution

When you run `agentpack lock`, agentpack walks your dependency graph and resolves every package to a single concrete commit and version. This page explains how that process works.

## Steps

### 1. Load the manifest

agentpack reads `agentpack.toml` and collects all entries in `[dependencies]`.

### 2. Fetch available versions

For each remote dependency, agentpack queries the GitHub API to list available tags. Tags are expected to be SemVer strings (`v1.2.3` or `1.2.3`).

### 3. Select the best version

For each dependency, agentpack selects the highest version that satisfies the declared constraint:

```toml
"github.com/acme/rules" = "^1.2"
# Selects the latest tag >= 1.2.0 and < 2.0.0
```

### 4. Resolve transitive dependencies

If any resolved package itself contains an `agentpack.toml`, agentpack reads it and adds those transitive dependencies to the graph. Resolution recurses until the graph is fully expanded.

### 5. Conflict resolution

When two packages require different version ranges of the same dependency, agentpack attempts to find a version that satisfies both constraints. If no such version exists, the lock command fails with a conflict error describing which packages are in conflict.

### 6. Write pack.lock

Once the full graph is resolved, the commit SHA for each selected tag is recorded and the lockfile is written.

## Version selection examples

| Constraint | Available tags | Selected |
|---|---|---|
| `"^1.0"` | 1.0.0, 1.2.3, 2.0.0 | 1.2.3 |
| `"~1.2"` | 1.2.0, 1.2.9, 1.3.0 | 1.2.9 |
| `"=1.0.0"` | 1.0.0, 1.1.0 | 1.0.0 |
| `">=1.0, <1.5"` | 1.0.0, 1.4.9, 1.5.0 | 1.4.9 |

## Branch pinning

If you declare `branch = "main"` instead of a version constraint, agentpack resolves to the HEAD commit of that branch at lock time. This is non-reproducible across time and is not recommended for stable configurations:

```toml
"github.com/acme/wip" = { branch = "main" }
```

## Local path dependencies

Local dependencies are not resolved via the version selection algorithm. agentpack reads the local directory at the declared path. The content hash is computed from the files present at lock time.

## Offline mode

If the cache is warm and you need to lock without network access, set `AGENTPACK_LAUNCH_FULL_SYNC=0` and re-use the existing lockfile. This prevents sync but does not regenerate the lock from scratch.
