# Integrity and Verification

`pack.lock` pins two things for every package: the **commit** it came from and a **content hash** of the files agentpack cached for it. The commit says which revision you asked for. The content hash lets agentpack prove, every time it reads the cache, that the files are still the ones that were locked.

## The content hash

`content_hash` looks like `sha256-tree-v1:<64 hex>`. The prefix names the algorithm, so another tool can recompute the value and a future algorithm can sit next to this one.

"No content hash" means exactly one thing: the `content_hash` key is absent from the entry. A key that is present must be the prefix followed by 64 lowercase hex digits. Anything else (an unknown or missing prefix, a short or uppercase digest, an empty string) makes every command refuse the lock before it fetches or stages anything, and no command rewrites it, `lock` included:

```text
invalid content_hash in pack.lock: github.com/acme/skills/demo
  found     "sha256-0000000000000000000000000000000000000000000000000000000000000000"
  expected  sha256-tree-v1:<64 lowercase hex digits>
  lockfile  /work/project/pack.lock
  fix       restore pack.lock from version control, or upgrade agentpack if a newer version wrote it
```

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

### An entry must agree with itself

agentpack writes `commit`, `cache_key` and `content_hash` together: the key is derived from the repository, path and commit, and the hash describes that commit's files. For every GitHub entry that has a content hash, each command recomputes the key and refuses the lock if it does not belong to the entry's commit. That is what a hand-edited `commit` looks like, and following it would stage a commit the hash was never taken from:

```text
inconsistent pack.lock entry: github.com/acme/skills/demo
  commit     8fc7add0e2b00906426adfb1ab9bf0f0bb40de2d
  cache_key  712cf95c…
  expected   89acc09c…   (the cache_key of that commit)
  fix        restore pack.lock from version control; move a pin with `agentpack update` or `agentpack lock --update`
```

If the `cache_key` is edited to match as well, the entry is consistent but its content hash still describes the old commit, so the fetched or cached files fail verification as described below. Nothing is fetched, staged or rewritten in either case.

### What `sync` may write to pack.lock

`sync` and the launchers verify the lock; they do not re-resolve it. For an entry that is already locked they never change `commit`, `cache_key`, `content_hash` or `url`. If nothing the lock records has changed, the file is not rewritten at all.

`sync` still follows `agentpack.toml`, and says so on stderr, one line per change:

| Cause | What `sync` writes | Line printed |
|---|---|---|
| A dependency was added to the manifest | a new entry | `pack.lock: added <module> at commit <sha>` |
| A dependency was removed from the manifest | the entry is dropped | `pack.lock: removed <module>` |
| An exact commit pin was changed in the manifest, or a path dependency's files changed | the entry's pin | `pack.lock: <module> moved from commit <a> to <b>` |
| A package without a content hash had to be downloaded | its first `content_hash` | `recorded content hashes for N package(s) …` |
| An MCP server was removed or redefined | its record is dropped | `pack.lock: dropped the record of MCP server <name> …` |

`lock`, `add`, `remove` and `update` are the commands that re-resolve on purpose (`sync --update-lock` counts as one). They print the same `moved from commit` line for every pin they change. Only `agentpack lock` replaces a lock it cannot parse, with a warning that it did; a lock with an invalid `content_hash` or an inconsistent entry is not replaced by any command.

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

Nothing is changed: the cache entry is left as found so you can inspect it, staging keeps what the last good sync built, and the lock is not touched. agentpack never repairs silently. The same applies when the lock's digest is well formed but wrong: with a warm cache the entry does not match it, and with an empty cache the download does not match it.

A freshly **downloaded** tree is checked the same way before it is allowed into the cache. If GitHub (or anything in between) serves different files for the commit the lock pins, or the lock was edited, the download is discarded and the error says the fetched content does not match. `--repair` cannot fix this; restore `pack.lock` from version control.

### Repair

```sh
agentpack sync --repair
```

For each cache entry that fails verification, `--repair` downloads the pinned commit again into a temporary directory, verifies it against the lock, and only then replaces the entry. It reports each replacement on stderr with the old and new digests. If the download does not match the lock either, the entry is left alone and the command fails.

### Staged files

Each sync records what it staged for every harness: the `skills/`, `commands/`, `agents/`, `rules/` and `hooks/` trees and the MCP config file of each staged root, with a SHA-256 per file. The record lives under `$AGENTPACK_HOME/projects/<hash>/`, outside the staging tree.

`agentpack sync --verify-only` hashes the staged files and compares them with that record. A file that was modified, added or removed fails the command and is named:

```text
staged tree does not match the last sync
  modified  /tmp/agentpack-29d1…/modes/default/cursor/agentpack-bundle/skills/demo/SKILL.md
  added     /tmp/agentpack-29d1…/modes/default/cursor/agentpack-bundle/skills/demo/scripts/extra.sh
  fix       agentpack sync   (rebuilds staging from the verified cache and reports what it replaced)
```

A plain `sync` always rebuilds staging from the verified cache. When it replaces files that changed since the last sync, it says which:

```text
warning: 2 staged file(s) changed since the last sync and were replaced from the verified cache: modified /tmp/…/SKILL.md; added /tmp/…/extra.sh
```

Limits of this check:

- The rest of a staged root (credentials, history, harness settings such as `config.toml`) is written by the harness while it runs and is not recorded.
- Files staged as hard links to your project's `./.agents/` are yours; editing them in place is not reported.
- The `sync` report compares file type, size and modification time, because it is about to rebuild anyway. An edit that preserves both is replaced without being named. `--verify-only` hashes content and does see it.
- `--verify-only` fails if no sync by this version has recorded the staging yet; run `sync` once.

### Launches that skip the sync

When the manifest, the lock, `./.agents/`, the mode and the target are unchanged since the last launch, a launcher skips the sync and stages nothing: the harness starts with the tree the last sync built from a verified cache. On that path agentpack does not hash file contents. It compares a fingerprint of the cache entries' metadata (each file's path, type, permission bits, size and modification time) with the one taken after the last full verification. Any visible change ends the fast path and sends the launch through a full sync, which hashes everything and stops on a mismatch.

An edit that keeps a file's size and restores its modification time is invisible to the fingerprint. It still cannot reach a harness unnoticed, because reaching a harness means re-staging and re-staging always hashes. Set `AGENTPACK_FULL_VERIFY=1` to hash on every launch anyway.

A launch that skips the sync does not check the staged files against the record; use `sync --verify-only` for that.

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
- A hand-edited lock entry: a malformed or wrong `content_hash`, or a `commit` that no longer matches its `cache_key`.
- A staged file that was edited, added or removed: `sync --verify-only` fails and names it, and the next sync rebuilds staging from the verified cache and reports the replacement.

It does not:

- **Vet content.** A hash proves the files are the ones that were locked, not that they are safe. Review a skill, plugin or MCP definition before you lock it, and review `git diff pack.lock` when a pin moves.
- **Authenticate the publisher.** There are no signatures. The first lock trusts what GitHub serves for the commit over TLS; afterwards the hash holds everyone to that.
- **Protect the lock.** Whoever can rewrite an entry completely (commit, cache key and a matching content hash, or the entry with its `content_hash` key removed) has written a different valid lock. Keep it in version control and review its diffs.
- **Stop someone with your user account.** They can also edit the staged tree, which is not hashed on a launch that skips the sync, or replace the agentpack binary. There is also a short window between verifying a cache entry and copying it into staging.
- **Cover path dependencies against their source.** They are re-copied from your directory on every `lock` and `sync`, and the hash follows the directory.
- **Pin everything an MCP server runs.** See [MCP Servers](./mcp.md#what-packlock-records).
