# Team Workflows

Patterns for sharing agent configuration across a team.

## Commit the manifest and lockfile

Commit both files together:

```sh
git add agentpack.toml pack.lock
git commit -m "chore: add agentpack dependencies"
```

That pair is what makes resolution identical for everyone. Staging trees are machine-local and live outside the repo by default, so there's nothing to ignore unless you've pointed `AGENTPACK_STAGING_ROOT` inside the project — in which case ignore that path.

## Onboarding

A new contributor clones the repo and runs one command:

```sh
agentpack sync     # fetch everything pinned in pack.lock and stage it
agentpack claude   # or any other launcher
```

No copying config files, no "which version of the rules are we on" — `pack.lock` answers that.

## Updating a dependency

Edit the constraint, re-resolve, commit:

```toml
[dependencies]
"github.com/acme/shared-rules" = "^0.4"   # was ^0.3
```

```sh
agentpack lock            # reconcile pack.lock with the changed manifest
git add agentpack.toml pack.lock
git commit -m "chore: bump shared-rules to ^0.4"
```

`lock` reconciles manifest edits. To advance a **floating** pin (a branch, or a constraint whose range hasn't changed) to its latest matching commit, use `agentpack lock --update`. Everyone else pulls the commit and runs `agentpack sync`.

## Shared team packages

Keep team-specific skills and rules in a GitHub repo, tag releases, and depend on it from each project:

```toml
[dependencies]
"github.com/acme/team-agent-config" = "^1.0"
```

For a **private** repo, every machine and CI runner needs `GITHUB_TOKEN` (or `GH_TOKEN`) with read access. Projects stay aligned with `agentpack lock && agentpack sync`.

## A committed project overlay (`./.agents/`)

To ship project-specific agent content that isn't a published package, drop it in `./.agents/` at the repo root and commit it. On `sync`, agentpack merges it into the harness trees that don't natively read it (the Claude bundle and Codex home), layered last so it wins on conflicts. Shape it like a harness layout: `rules/`, `skills/`, `agents/`, `commands/`, `hooks/`, `mcp.json`, top-level `AGENTS.md`/`CLAUDE.md`, and optional `claude/` and `codex/` subtrees.

Cursor, OpenCode, and Antigravity already discover workspace customization natively, so `./.agents/` is not merged into their staging. Set `AGENTPACK_DOT_AGENTS=0` to skip the overlay entirely.

## CI

Run `agentpack sync` before any step that needs staged artifacts, and persist the cache between runs. Key on `pack.lock` so the cache invalidates when dependencies change:

```yaml
# .github/workflows/ci.yml
- name: Cache agentpack
  uses: actions/cache@v4
  with:
    path: ~/.local/share/agentpack/cache   # or $AGENTPACK_HOME/cache
    key: agentpack-${{ hashFiles('pack.lock') }}

- name: Sync agentpack
  run: agentpack sync
```

## Merge conflicts in pack.lock

`pack.lock` is plain TOML and usually merges cleanly, but two branches that both change dependencies can collide. The reliable fix:

1. Resolve the conflict in `agentpack.toml` (the source of truth).
2. Run `agentpack lock` to regenerate a clean `pack.lock` from the merged manifest.
3. Commit the regenerated lockfile.
