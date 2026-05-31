# Staging and Bundles

Staging materializes cached packages into the directory layout each harness expects. Every harness has its own conventions, so `sync` builds a separate staging tree per harness and per [mode](./modes.md).

## Staging stays outside your repo

Staged trees are deliberately kept out of your project's Git repository. They are machine-specific, regenerated on demand from the cache, and can be large. agentpack also does **not** symlink pack content into your real `~/.claude` or `~/.cursor` — that would leak project-specific pins into your global agent config.

Only `agentpack.toml` and `pack.lock` belong in version control. (Two opt-in exceptions exist: the `agentpack agent` and `agentpack agy` launchers create a single workspace symlink — see their harness guides.)

## Staging root

The default staging root is a per-project directory in the system temp location:

```text
<temp_dir>/agentpack-<project-hash>/
```

Set `AGENTPACK_STAGING_ROOT` to pin a stable location — useful when your OS rotates temp directories, or in CI for a fast local disk:

```sh
export AGENTPACK_STAGING_ROOT=/tmp/agentpack-staging
```

Each mode gets its own subtree under `modes/<mode>/`, and each harness gets a directory there:

```text
<staging-root>/
  modes/
    default/
      plugins/agentpack-bundle/   # Claude --plugin-dir bundle
      opencode/                   # OpenCode config root (OPENCODE_CONFIG_DIR)
      codex-home/                 # Codex home (CODEX_HOME)
      cursor/  cursor-home/       # Cursor plugin tree + fake HOME
      grok/    grok-home/         # Grok bundle + home (GROK_HOME)
      agy/                        # Antigravity workspace plugin
```

A launcher points its harness at the right subtree by setting an environment variable or CLI flag before exec-ing the binary — `claude --plugin-dir …`, `OPENCODE_CONFIG_DIR`, `CODEX_HOME`, `GROK_HOME`, a fake `HOME` for Cursor, a workspace `--add-dir` for Antigravity. The [harness guides](../harnesses/claude.md) cover each one.

## What a bundle contains

A **bundle** is what one package contributes to a staging tree. Packages carry some mix of:

- **commands** — slash-command definitions
- **agents** — sub-agent or mode definitions
- **skills** — reusable instruction sets
- **rules** — persistent context applied across a session
- **MCP servers** — merged into each harness's native MCP config

[Cross-harness conversion](../harnesses/conversion.md) happens here: a skill authored in one format is re-rendered into every target harness's native format rather than copied verbatim. Layering order within a tree is user config first, then plugins (by `cache_key`), then bare skills, then the project's `./.agents/` overlay — later layers win on the same relative path.

## Re-staging

`sync` rebuilds every harness's staging from the current cache state:

```sh
agentpack sync
```

Launchers run a **fast pre-sync** when `agentpack.toml`, `pack.lock`, and `./.agents/` are unchanged since the last successful launch: they verify cache and staging integrity and skip the full lock-resolve, re-download, and rebuild. Because of that, floating pins do not advance on launch alone — run `agentpack sync` or `agentpack lock --update` when you need the lock refreshed.
