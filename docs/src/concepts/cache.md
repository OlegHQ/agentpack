# Package Cache

agentpack fetches each package once and stores it under a key derived from its repository, path and commit, so the same package at the same commit is never downloaded twice — and is shared across every project on the machine. The key names the slot; the `content_hash` in `pack.lock` is what proves the files in it.

## Where it lives

Everything sits under the user-wide agentpack home, never inside your repo:

```text
$AGENTPACK_HOME/
  cache/
    <cache_key>/          # one extracted package tree per repository, path and commit
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

Each `cache/<cache_key>/` directory is an extracted tree, named by the `cache_key` recorded in [`pack.lock`](./lockfile.md). The key is the SHA-256 of the package identity (`github:<owner>/<repo>`, in-repo path, commit). It is not a hash of the files and does not change when they do. The same key is reached whenever owner, repo, in-repo path, and commit match, so two projects pinning the same commit share one copy.

agentpack treats a slot as read-only once it is filled. It replaces one only when you ask (`sync --repair`), or when `lock` records a first content hash and finds the cached copy differs from a fresh download; both are reported.

## How sync uses it

`agentpack sync` walks `pack.lock` and, for each package:

1. If `cache/<cache_key>/` exists, recomputes its tree digest and compares it with the lock's `content_hash`. A mismatch stops the sync; nothing is staged.
2. If it is missing, downloads the tree from GitHub at the pinned commit (or copies it, for filesystem and `local/` mirror specs) into a temporary directory, verifies it the same way, and only then moves it into the slot.
3. Materializes it into the per-harness [staging directories](./staging.md), converting artifacts to each harness's native format.

See [Integrity and Verification](./integrity.md) for the digest, the error, and the repair path.

Path dependencies are the exception: they are re-copied from source on every `lock`/`sync`, so their slot, commit and content hash follow your directory.

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

The cache is never invalidated automatically: a key names one commit, so a new upstream commit is simply a different key, stored alongside the old one. To replace entries that no longer match the lock, use:

```sh
agentpack sync --repair
```

To force a clean re-fetch of everything, delete the directory and sync again. Every download is verified against `pack.lock` before it is used:

```sh
rm -rf "$AGENTPACK_HOME/cache"
agentpack sync
```
