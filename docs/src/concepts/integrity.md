# Integrity and Verification

`pack.lock` pins two things for every package: the **commit** it came from and a **content hash** of the files agentpack cached for it. The commit says which revision you asked for. The content hash lets agentpack prove, every time it reads the cache, that the files are still the ones that were locked.

## The content hash

`content_hash` looks like `sha256-tree-v1:<64 hex>`. The prefix names the algorithm, so another tool can recompute the value and a future algorithm can sit next to this one. A lock with a prefix this build does not know is rejected, never ignored.

`sha256-tree-v1` covers the package directory as it sits in `$AGENTPACK_HOME/cache/<cache_key>/`:

1. Collect every **regular file** and **symbolic link** below the directory. Directories are not records, so empty directories do not count. Any other file type (device, socket, pipe) is an error.
2. Name each record by its path relative to the directory, with `/` separators.
3. Sort the records by that path, comparing bytes.
4. Feed one SHA-256 stream with, for each record in order:

   ```text
   kind 0x00 path 0x00 length 0x00 payload
   ```

   | Part | Value |
   |---|---|
   | `kind` | `f` regular file, `x` regular file with any execute permission bit, `l` symbolic link |
   | `path` | the relative path, as bytes |
   | `length` | the payload length in bytes, as ASCII decimal |
   | `payload` | the file bytes, or for a link its target text (the link is never followed) |

5. The digest is the prefix plus the lowercase hex SHA-256.

Other permission bits, ownership, and timestamps are not part of the hash. Moving the directory, copying it to another machine, or restoring it from a CI cache does not change it. Editing a byte, adding, deleting or renaming a file, making a file executable, or swapping a file for a link does.

The same computation in Python:

```python
import hashlib, os, stat

def sha256_tree_v1(root):
    records = []
    for directory, _, names in os.walk(root):
        for name in names:
            path = os.path.join(directory, name)
            records.append((os.path.relpath(path, root).replace(os.sep, "/").encode(), path))
    digest = hashlib.sha256()
    for relative, path in sorted(records):
        mode = os.lstat(path).st_mode
        if stat.S_ISLNK(mode):
            kind, payload = b"l", os.readlink(path).encode()
        elif stat.S_ISREG(mode):
            kind = b"x" if mode & 0o111 else b"f"
            payload = open(path, "rb").read()
        else:
            raise ValueError(f"unsupported file type: {path}")
        digest.update(kind + b"\0" + relative + b"\0" + str(len(payload)).encode() + b"\0" + payload)
    return "sha256-tree-v1:" + digest.hexdigest()
```

(`os.walk` lists a link to a directory under directory names; a complete implementation records those links too.)

### What exactly is hashed

The hash is over the tree agentpack **materializes**, which is the upstream tree at the pinned commit and path after two fixed steps:

- **Extraction** writes every archive entry as a regular file with mode `0644`. Execute bits are dropped, and a link in the archive becomes an empty regular file. A cache entry that agentpack wrote therefore contains only `f` records; an `x` or `l` record means something changed it afterwards.
- **Layout normalization** adds files agentpack derives from the package: plugin manifest stubs for the harnesses the package did not ship one for (`.claude-plugin/`, `.cursor-plugin/`, `.codex-plugin/`), `mcp.json` copied from a legacy `.mcp.json`, and for a single-plugin marketplace repository the merged plugin directory.

To recompute a hash from a Git checkout you have to apply the same two steps. A change to either step changes what is hashed, so it must come with a new algorithm prefix; a test pins the digest of a normalized fixture to enforce that.

For a **path dependency** the hashed tree is the copy agentpack makes of your directory (regular files only, Git-ignored files left out).

## When the hash is recorded

A content hash is only ever taken from bytes fetched for that purpose in the same command. agentpack never reads a hash off a cache entry that was already sitting on disk, because that would bless whatever is there.

| Command | What it does about hashes |
|---|---|
| `agentpack lock`, `add`, `remove`, `update` | Record a hash for every package. A package that is already cached but has no hash in the lock is downloaded again at its pinned commit; if the cached copy differs from the download, the cache entry is replaced and a warning says so. |
| `agentpack sync` and the launchers | Verify. They record a hash only for a package they have to download because it is not cached. |

A hash that is already in the lock is never recomputed or replaced for the same commit. It changes only when the pin itself changes (`lock --update`, `update`, or a manifest edit).

## Verification

Every command that reads the cache to resolve or stage compares each cache entry with its `content_hash` first: `lock`, `add`, `remove`, `update`, `sync`, `sync --verify-only`, and any launch that has to sync.

On a mismatch the command stops with a non-zero exit before anything is staged, and prints:

```text
content hash mismatch: github.com/acme/skills/demo
  expected  sha256-tree-v1:9041ba90…
  actual    sha256-tree-v1:e2917cc0…
  cache     /home/you/.local/share/agentpack/cache/e024077a…
  commit    1111111111111111111111111111111111111111
  repair    agentpack sync --repair   (re-fetches the commit and verifies it again)
```

Nothing is changed: the cache entry is left as found so you can inspect it, staging keeps what the last good sync built, and the lock is not touched. agentpack never repairs silently.

A freshly **downloaded** tree is checked the same way before it is allowed into the cache. If GitHub (or anything in between) serves different files for the commit the lock pins, the download is discarded and the error says the fetched content does not match. This is the one case `--repair` cannot fix; accept new content only by changing the pin on purpose.

### Repair

```sh
agentpack sync --repair
```

For each cache entry that fails verification, `--repair` downloads the pinned commit again into a temporary directory, verifies it against the lock, and only then replaces the entry. It reports each replacement on stderr with the old and new digests. If the download does not match the lock either, the entry is left alone and the command fails.

### Launches that skip the sync

When the manifest, the lock, `./.agents/`, the mode and the target are unchanged since the last launch, a launcher skips the sync and stages nothing: the harness starts with the tree the last sync built from a verified cache. On that path agentpack does not hash file contents. It compares a fingerprint of the cache entries' metadata (each file's path, type, permission bits, size and modification time) with the one taken after the last full verification. Any visible change ends the fast path and sends the launch through a full sync, which hashes everything and stops on a mismatch.

An edit that keeps a file's size and restores its modification time is invisible to the fingerprint. It still cannot reach a harness unnoticed, because reaching a harness means re-staging and re-staging always hashes. Set `AGENTPACK_FULL_VERIFY=1` to hash on every launch anyway.

## Locks without content hashes

A lock written before content hashes existed (`lockfile_version = 2`) still loads and still stages. Its packages are **not verified**, and every `sync` and launch says so in one line:

```text
warning: 3 package(s) in pack.lock have no content hash and are not verified; run `agentpack lock` to record them
```

`sync` does not rewrite such a lock unless it has to download a package. Run `agentpack lock` once to record hashes for everything and commit the result; the file becomes `lockfile_version = 3`, which older agentpack binaries refuse to load (they report `content_hash` as an unsupported field).

Set `AGENTPACK_REQUIRE_VERIFIED=1` (in CI, for example) to make `sync` and the launchers refuse a lock that still has unverified packages.

## What this protects against, and what it does not

It detects:

- A cache entry that was edited, truncated, or partly deleted after it was fetched: by a script, another tool, an agent "fixing" a skill in place, a bad disk, or a poisoned CI cache restore.
- A fetch that returns different files for the pinned commit than the ones that were locked.
- A staged file that was edited: the next sync rebuilds staging from the verified cache.

It does not:

- **Vet content.** A hash proves the files are the ones that were locked, not that they are safe. Review a skill, plugin or MCP definition before you lock it, and review `git diff pack.lock` when a pin moves.
- **Authenticate the publisher.** There are no signatures. The first lock trusts what GitHub serves for the commit over TLS; afterwards the hash holds everyone to that.
- **Protect the lock.** Whoever can change `pack.lock` can change the hash. Keep it in version control and review its diffs.
- **Stop someone with your user account.** They can also edit the staged tree, which is not hashed on a launch that skips the sync, or replace the agentpack binary. There is also a short window between verifying a cache entry and copying it into staging.
- **Cover path dependencies against their source.** They are re-copied from your directory on every `lock` and `sync`, and the hash follows the directory.
- **Pin everything an MCP server runs.** See [MCP Servers](./mcp.md#what-packlock-records).
