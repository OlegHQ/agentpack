# Content-Addressed Cache

agentpack stores all downloaded package content in a local cache. The cache is **content-addressed**: each entry is keyed by the SHA-256 hash of its content, not by its name or version. This means a given file is downloaded exactly once and shared across all projects on the machine.

## Cache location

The default cache root is:

```
$AGENTPACK_HOME/cache/
```

If `AGENTPACK_HOME` is not set, it defaults to `~/.agentpack`. Override it:

```sh
export AGENTPACK_HOME=/data/agentpack
```

## Cache layout

```
$AGENTPACK_HOME/
  cache/
    sha256/
      e3b0c44298fc1c149af...   # raw archive or file content
      abcdef1234567890abc...
  registry/
    github.com/
      OlegHQ/
        paperclip-skills/
          versions.json        # cached tag list
```

Entries under `sha256/` are immutable blobs named by their hash. agentpack never overwrites an existing entry — if the hash is already present, the download is skipped.

## How sync works

`agentpack sync` compares the hashes listed in `pack.lock` against the local cache:

1. For each locked package, check if `cache/sha256/<hash>` exists.
2. If missing, download the archive from GitHub at the pinned commit.
3. Verify the downloaded content matches the hash in `pack.lock`.
4. Store the verified content in the cache.
5. Materialize the content into the [staging directory](./staging.md) for each harness.

## Cache sharing across projects

Because the cache is stored under `AGENTPACK_HOME` (not inside the project), all projects on the same machine share the same cache. If two projects depend on the same package at the same version, the content is stored once.

In CI environments you can preserve `$AGENTPACK_HOME/cache/` between runs to avoid redundant downloads:

```yaml
# GitHub Actions example
- uses: actions/cache@v4
  with:
    path: ~/.agentpack/cache
    key: agentpack-${{ hashFiles('pack.lock') }}
```

## Cache invalidation

The cache is never invalidated automatically. A given hash entry is valid forever — if the content changed upstream the hash would be different, which would be a different entry. To force a re-download of everything, delete the cache directory:

```sh
rm -rf "$AGENTPACK_HOME/cache"
agentpack sync
```
