# Lockfile (pack.lock)

`pack.lock` records the exact resolved state of your dependency graph. agentpack generates it; you commit it and never edit it by hand. The current format is **lockfile version 3**; version 2 locks still load. Older `[[skills]]` / `[[plugins]]` layouts are rejected.

## Why it exists

- **Reproducibility** — everyone resolves to the same commit, regardless of when they sync or which tags have since been published.
- **Completeness** — the lock lists every package, both the direct dependencies from your `agentpack.toml` *and* the transitive ones pulled from nested `agentpack.toml` files inside fetched packages.
- **Integrity** — each package carries a `content_hash` of its file tree. agentpack compares the cache with it before staging and stops on a mismatch. See [Integrity and Verification](./integrity.md).
- **MCP servers** — the lock records what each staged MCP server runs and, where agentpack can pin it, the exact version. See [MCP Servers](./mcp.md#what-packlock-records).
- **Auditability** — it's plain TOML, so `git diff pack.lock` shows exactly what moved.

## Structure

A lockfile has a version marker, a `[meta]` block, an optional `[config]` block, a flat list of `[[packages]]`, and a list of `[[mcp_servers]]`:

```toml
lockfile_version = 3

[meta]
name = "my-project"
version = "0.0.1"

[[packages]]
module       = "github.com/anthropics/skills/skills/canvas-design"
direct       = true
kind         = "skill"
url          = "https://github.com/anthropics/skills/tree/<40-hex>/skills/canvas-design"
owner        = "anthropics"
repo         = "skills"
path         = "skills/canvas-design"
commit       = "<40-hex commit SHA>"
cache_key    = "<64-hex cache slot name>"
content_hash = "sha256-tree-v1:<64-hex digest of the file tree>"

[[mcp_servers]]
name       = "playwright"
source     = "plugin"
launcher   = "npm"
status     = "pinned"
definition = "sha256:<64-hex digest of the command and arguments>"
requested  = "@playwright/mcp@latest"
package    = "@playwright/mcp"
version    = "0.0.83"
integrity  = "sha512-…"
registry   = "https://registry.npmjs.org"
```

### Fields per package

| Field | Description |
|---|---|
| `module` | Module ID, matching a key in `[dependencies]` (or a transitive dependency) |
| `direct` | `true` for a direct dependency; omitted for a transitive one |
| `kind` | `"skill"` or `"plugin"` |
| `url` | GitHub tree URL of the package |
| `owner`, `repo`, `path` | GitHub coordinates and in-repo path |
| `commit` | Full 40-hex commit SHA that was resolved |
| `cache_key` | Name of the cache slot under `$AGENTPACK_HOME/cache/`. It is the SHA-256 of the package **identity** (`github:<owner>/<repo>`, path, commit), not of the files, and says nothing about their content |
| `content_hash` | Digest of the package's file tree, with its algorithm prefix. Absent in a lock written before version 3; such a package is not verified |
| `name` | Plugin name; omitted for skills |

### Fields per MCP server

| Field | Description |
|---|---|
| `name` | Server name after the [merge](./mcp.md#the-merge-pipeline) |
| `source` | Where the winning definition came from: `plugin`, `manifest`, or `.agents` |
| `launcher` | `npm`, `pypi`, `docker`, `remote`, or `command` |
| `status` | `pinned`, `unpinnable`, or `unpinned` |
| `definition` | Digest of the server's type, command, arguments and URL. A record applies only while it matches the current definition. Environment values are never hashed or stored |
| `requested` | The package or image spec as written, such as `@playwright/mcp@latest` |
| `package`, `version`, `integrity`, `registry` | What the spec resolved to, when agentpack resolved it |
| `host` | Host of a remote server |
| `command` | Executable of a local command |
| `allow_unpinned` | `true` when the server was recorded unpinned on purpose with `--allow-unpinned-mcp` |
| `reason` | Why a server is `unpinned` or `unpinnable` |

The optional `[config]` table holds bookkeeping such as `disabled_plugins`.

### Versions

| `lockfile_version` | Contents | Read by |
|---|---|---|
| `2` | `[[packages]]` without `content_hash`, no `[[mcp_servers]]` | every build |
| `3` | version 2 plus `content_hash` and `[[mcp_servers]]` | builds that know content hashes |

agentpack writes version 3 as soon as a lock holds a content hash or an MCP record, and version 2 otherwise, so a lock that was never re-locked stays readable by older binaries.

## Refreshing the lock

```sh
agentpack lock            # resolve agentpack.toml → pack.lock, keeping commits already pinned
agentpack lock --update   # re-resolve floating pins (branches, floating semver, npm dist-tags)
```

`lock` resolves the full graph and rewrites the file. It fetches every package it has no content hash for, records the hash, and records the project's MCP servers. An MCP server it cannot pin because the registry is unreachable fails the command; `--allow-unpinned-mcp` records it as `unpinned` instead. Note that **launching a harness does not advance floating pins**: launchers run a fast pre-sync that skips re-resolution when inputs are unchanged. Run `lock --update` (or `sync --update-lock`) when you actually want a branch or floating constraint to move.

## When to commit it

Always — for application projects and shared agent configurations alike. It is what makes "works on my machine" hold across the team and CI. Reusable packages should commit it too, though their downstream consumers resolve independently.

## Drift

If `pack.lock` falls out of sync with `agentpack.toml` (typically after a hand edit), reconcile with `agentpack lock`. With an **empty** `[dependencies]` table, `sync` treats the existing lock as authoritative and leaves it alone — useful for hand-authored or test lockfiles.
