# Staging and Bundles

Staging is the process of materializing cached package content into a directory layout that a specific AI harness expects. Each harness has its own layout conventions, so agentpack maintains one staging directory per harness.

## Why staging exists outside the repo

Staged artifacts are intentionally placed **outside your project's Git repository**. They are:

- Machine-specific (different harnesses may be installed in different locations)
- Regenerated on demand from the cache (no need to commit them)
- Potentially large (docs, examples, binaries)

This keeps your repository clean. Only `agentpack.toml` and `pack.lock` belong in version control.

## Staging root

By default the staging root is:

```
$AGENTPACK_STAGING_ROOT/
```

If `AGENTPACK_STAGING_ROOT` is not set, agentpack derives a location under `$AGENTPACK_HOME`:

```
$AGENTPACK_HOME/staging/<project-hash>/
```

Override the root:

```sh
export AGENTPACK_STAGING_ROOT=/tmp/agentpack-staging
```

## Per-harness layout

Inside the staging root, each harness gets its own subdirectory:

```
staging/
  claude/
    .claude/
      commands/
      agents/
  cursor/
    .cursor/
      rules/
  opencode/
    ...
  codex/
    ...
```

Each launcher (`agentpack claude`, `agentpack agent`, etc.) points the harness at its own subdirectory by setting environment variables or CLI flags before exec-ing the binary.

## Bundle contents

A **bundle** is the collection of artifacts materialized from one package into a staging directory. Each package may contribute one or more artifact types:

- **commands** — harness-specific slash-command definitions
- **agents** — sub-agent or mode definitions
- **skills** — reusable instruction sets (called differently per harness)
- **rules** — persistent context injected into every session

Cross-harness [artifact conversion](../harnesses/conversion.md) happens at bundle time: a skill defined in one format is automatically converted to every harness's native format.

## Forcing a re-stage

To wipe and re-materialize staging from the cache without re-downloading:

```sh
agentpack sync
```

`sync` always re-stages all harnesses from the current cache state. To run a full sync (including network) before launching, set:

```sh
export AGENTPACK_LAUNCH_FULL_SYNC=1
agentpack claude
```
