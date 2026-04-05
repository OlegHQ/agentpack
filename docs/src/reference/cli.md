# CLI Commands

## Global flags

| Flag | Description |
|---|---|
| `--help`, `-h` | Print help for the command |
| `--version`, `-V` | Print the agentpack version |

---

## `agentpack init`

Initialize a new manifest in the current directory.

```sh
agentpack init
```

Creates `agentpack.toml` with a `[package]` section derived from the directory name. Does nothing if a manifest already exists.

---

## `agentpack add <module-id>`

Add a dependency to the manifest.

```sh
agentpack add github.com/OlegHQ/paperclip-skills
agentpack add github.com/acme/monorepo/packages/rules@^1.2
```

| Argument | Description |
|---|---|
| `<module-id>` | Module ID, optionally followed by `@<constraint>` |

Writes the dependency to `[dependencies]` and runs `agentpack lock` automatically.

---

## `agentpack remove <module-id>`

Remove a dependency from the manifest and regenerate the lockfile.

```sh
agentpack remove github.com/OlegHQ/paperclip-skills
```

---

## `agentpack lock`

Resolve the dependency graph and write (or update) `pack.lock`.

```sh
agentpack lock
```

Makes network calls to fetch available versions from GitHub. Does not download package content.

---

## `agentpack sync`

Download all packages listed in `pack.lock` into the cache and materialize staging directories.

```sh
agentpack sync
```

Skips packages whose content hash is already in the cache. Always re-stages all harnesses.

---

## `agentpack claude`

Stage dependencies for Claude Code and launch `claude`.

```sh
agentpack claude [-- <claude-args>...]
```

Sets `--plugin-dir` pointing at the Claude staging directory, then execs `claude`.

---

## `agentpack agent`

Stage dependencies for Cursor and launch `cursor`.

```sh
agentpack agent [-- <cursor-args>...]
```

Overrides `HOME` with a synthetic directory containing packaged agent content, then execs `cursor`.

---

## `agentpack opencode`

Stage dependencies for OpenCode and launch `opencode`.

```sh
agentpack opencode [-- <opencode-args>...]
```

Sets `OPENCODE_CONFIG_DIR` to the OpenCode staging directory, then execs `opencode`.

---

## `agentpack codex`

Stage dependencies for Codex and launch `codex`.

```sh
agentpack codex [-- <codex-args>...]
```

Sets `CODEX_HOME` to the Codex staging directory, then execs `codex`.

---

## Exit codes

| Code | Meaning |
|---|---|
| `0` | Success |
| `1` | General error (check stderr for details) |
| `2` | Manifest or lockfile parse error |
| `3` | Dependency resolution conflict |
| `4` | Network error during lock or sync |
