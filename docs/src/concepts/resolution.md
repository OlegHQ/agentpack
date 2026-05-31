# Dependency Resolution

`agentpack lock` turns your manifest into a fully pinned graph: every package, direct and transitive, resolved to one concrete commit. This page covers how that happens.

## The steps

1. **Load the manifest.** agentpack reads `agentpack.toml` and collects `[dependencies]`.
2. **List available versions.** For each remote dependency it needs to resolve a ref, it queries GitHub for the repo's tags. Tags may be `v1.2.3` or `1.2.3`.
3. **Select a version.** For a SemVer constraint, agentpack picks the highest tag that satisfies it. A `tag`/`commit` pin is taken as-is; a `branch` resolves to that branch's current HEAD.
4. **Expand transitively.** If a resolved package contains its own `agentpack.toml`, its `[dependencies]` are added to the graph and resolved too. This recurses until the graph is complete.
5. **Resolve conflicts.** When two packages constrain the same dependency differently, agentpack looks for a version satisfying both. If none exists, `lock` fails and names the conflicting packages.
6. **Write `pack.lock`.** Each selected ref's commit SHA is recorded along with its `cache_key`.

`lock` resolves; it does not download content. Fetching happens in [`sync`](./cache.md).

## Version selection

agentpack picks the highest tag satisfying the constraint:

| Constraint | Available tags | Selected |
|---|---|---|
| `"^1.0"` | 1.0.0, 1.2.3, 2.0.0 | 1.2.3 |
| `"~1.2"` | 1.2.0, 1.2.9, 1.3.0 | 1.2.9 |
| `"1.0.0"` | 1.0.0, 1.1.0 | 1.0.0 |
| `">=1.0, <1.5"` | 1.0.0, 1.4.9, 1.5.0 | 1.4.9 |

## Branch pins drift

A `branch` pin resolves to the branch HEAD *at lock time*. Two people who lock on different days get different commits, so it is not reproducible over time — prefer a version constraint or tag for anything stable:

```toml
"github.com/acme/wip" = { branch = "main" }
```

Because the lockfile stores the resolved commit, a branch pin does not move on its own. It only advances when you explicitly run `agentpack lock --update` (or `sync --update-lock`).

## Local path dependencies

A `path` dependency skips version selection entirely. agentpack reads the directory on disk and computes its content hash from the files present at lock time, so local edits are reflected on the next `lock`/`sync`.

## Metadata caching and the offline fallback

GitHub ref→commit and tag-list lookups are cached in `$AGENTPACK_HOME/cache/db.reddb` and reused across `add`, `lock`, and `sync`, which keeps repeat resolutions off the network. When the GitHub REST API fails or is throttled, agentpack falls back to the Git protocol — an embedded `gix` `ls-refs` against `https://github.com/<owner>/<repo>.git` — before resorting to stale cached metadata. There is no hard dependency on the REST API for ref and tag resolution.

To work entirely from a warm cache without touching the network, keep `[dependencies]` and `pack.lock` unchanged and run `agentpack sync`: with an unchanged lock, sync stages from the cache without re-resolving.
