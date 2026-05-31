# Content-Addressed Cache

agentpack fetches each package once and stores it under a content hash, so the same package at the same commit is never downloaded twice — and is shared across every project on the machine.

## Where it lives

Everything sits under the user-wide agentpack home, never inside your repo:

```text
$AGENTPACK_HOME/
  cache/
    <cache_key>/          # one extracted package tree per content hash
    db.reddb              # metadata index + alias map + cached GitHub ref/tag lookups
  local/
    <owner>/<repo>/...     # optional offline mirror (same layout as owner/repo specs)
  projects/
    <project-hash>/        # per-project bookkeeping (overlay manifests, launch-sync state)
```

`AGENTPACK_HOME` defaults to `$XDG_DATA_HOME/agentpack` (or `$HOME/.local/share/agentpack`) on Unix and `%LOCALAPPDATA%\agentpack` on Windows. Override it:

```sh
export AGENTPACK_HOME=/data/agentpack
```

Each `cache/<cache_key>/` directory is an immutable extracted tree, keyed by the `cache_key` recorded in [`pack.lock`](./lockfile.md). agentpack never overwrites a slot: if the key is already present, the fetch is skipped. The same key is reached whenever owner, repo, in-repo path, and commit match — so duplicate content deduplicates automatically.

## How sync uses it

`agentpack sync` walks `pack.lock` and, for each package:

1. Checks whether `cache/<cache_key>/` already exists.
2. If missing, downloads the tree from GitHub at the pinned commit (or copies it, for filesystem and `local/` mirror specs).
3. Stores it under the cache key.
4. Materializes it into the per-harness [staging directories](./staging.md), converting artifacts to each harness's native format.

Path dependencies are the exception to immutability: they are re-copied from source on every `lock`/`sync`, and their content hash detects changes.

## Sharing across projects and CI

Because the cache lives under `AGENTPACK_HOME` rather than in the project, every project on the machine shares it. Two projects depending on the same package at the same commit store it once.

In CI, persist the cache between runs to skip redundant downloads. Key on `pack.lock` so the cache invalidates when dependencies change:

```yaml
# GitHub Actions
- uses: actions/cache@v4
  with:
    path: ~/.local/share/agentpack/cache   # or $AGENTPACK_HOME/cache
    key: agentpack-${{ hashFiles('pack.lock') }}
```

## Invalidation

The cache is never invalidated automatically: a key identifies its content, so changed upstream content is simply a different key, stored alongside the old one. To force a clean re-fetch, delete the directory and sync again:

```sh
rm -rf "$AGENTPACK_HOME/cache"
agentpack sync
```
