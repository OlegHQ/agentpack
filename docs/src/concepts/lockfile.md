# Lockfile (pack.lock)

`pack.lock` records the exact resolved state of your dependency graph. agentpack generates it; you commit it and never edit it by hand. It uses **lockfile version 2** — older `[[skills]]` / `[[plugins]]` layouts are rejected.

## Why it exists

- **Reproducibility** — everyone resolves to the same commit, regardless of when they sync or which tags have since been published.
- **Completeness** — the lock lists every package, both the direct dependencies from your `agentpack.toml` *and* the transitive ones pulled from nested `agentpack.toml` files inside fetched packages.
- **Integrity** — each package carries a `cache_key` (a content hash) that identifies its cache slot and lets agentpack detect a corrupted or wrong tree before staging.
- **Auditability** — it's plain TOML, so `git diff pack.lock` shows exactly what moved.

## Structure

A v2 lockfile has a version marker, a `[meta]` block, an optional `[config]` block, and a flat list of `[[packages]]`:

```toml
lockfile_version = 2

[meta]
name = "my-project"
version = "0.0.1"

[[packages]]
module    = "github.com/anthropics/skills/skills/canvas-design"
direct    = true
kind      = "skill"
url       = "https://github.com/anthropics/skills/tree/<40-hex>/skills/canvas-design"
owner     = "anthropics"
repo      = "skills"
path      = "skills/canvas-design"
commit    = "<40-hex commit SHA>"
cache_key = "<64-hex content hash>"
name      = ""
```

### Fields per package

| Field | Description |
|---|---|
| `module` | Module ID, matching a key in `[dependencies]` (or a transitive dependency) |
| `direct` | `true` for a direct dependency, `false` for a transitive one |
| `kind` | `"skill"` or `"plugin"` |
| `url` | GitHub tree URL pinned at the resolved commit |
| `owner`, `repo`, `path` | GitHub coordinates and in-repo path |
| `commit` | Full 40-hex commit SHA that was resolved |
| `cache_key` | 64-hex content hash; names the cache slot under `$AGENTPACK_HOME/cache/` |
| `name` | Plugin name (empty for skills) |

The optional `[config]` table holds bookkeeping such as `disabled_plugins`.

## Refreshing the lock

```sh
agentpack lock            # resolve agentpack.toml → pack.lock, keeping commits already pinned
agentpack lock --update   # re-resolve floating pins (branches, floating semver) from GitHub
```

`lock` resolves the full graph and rewrites the file. It does not download package content — that is `sync`'s job. Note that **launching a harness does not advance floating pins**: launchers run a fast pre-sync that skips re-resolution when inputs are unchanged. Run `lock --update` (or `sync --update-lock`) when you actually want a branch or floating constraint to move.

## When to commit it

Always — for application projects and shared agent configurations alike. It is what makes "works on my machine" hold across the team and CI. Reusable packages should commit it too, though their downstream consumers resolve independently.

## Drift

If `pack.lock` falls out of sync with `agentpack.toml` (typically after a hand edit), reconcile with `agentpack lock`. With an **empty** `[dependencies]` table, `sync` treats the existing lock as authoritative and leaves it alone — useful for hand-authored or test lockfiles.
