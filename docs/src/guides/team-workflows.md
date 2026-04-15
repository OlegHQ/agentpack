# Team Workflows

This guide covers common patterns for using agentpack in a team environment.

## Committing the manifest and lockfile

Always commit both `agentpack.toml` and `pack.lock` to your repository:

```sh
git add agentpack.toml pack.lock
git commit -m "chore: add agentpack dependencies"
```

This ensures every team member and every CI run resolves to the same package versions. Do not add the staging directories to version control — they are machine-local.

Add this to `.gitignore` if you want to be explicit (though staging directories are outside the repo by default):

```gitignore
# agentpack staging (if AGENTPACK_STAGING_ROOT is inside the repo)
.agentpack-staging/
```

## Onboarding new developers

New developers clone the repository and run:

```sh
agentpack sync
```

This downloads all packages locked in `pack.lock` into the local cache and stages them. After that, they can launch any supported harness:

```sh
agentpack claude
```

No manual copying of config files, no "which version of the rules are we on?" conversations.

## Updating a dependency

One developer updates the version constraint in `agentpack.toml`:

```toml
[dependencies]
"github.com/OlegHQ/paperclip-skills" = "^0.4"   # was ^0.3
```

Then regenerates the lockfile and commits:

```sh
agentpack lock
git add agentpack.toml pack.lock
git commit -m "chore: upgrade paperclip-skills to ^0.4"
```

Other developers pull the commit and run `agentpack sync` to get the updated content.

## CI integration

In CI, run `agentpack sync` before any step that needs staged artifacts. Cache `$AGENTPACK_HOME/cache/` to avoid redundant downloads:

```yaml
# .github/workflows/ci.yml
- name: Cache agentpack
  uses: actions/cache@v4
  with:
    path: ~/.agentpack/cache
    key: agentpack-${{ hashFiles('pack.lock') }}

- name: Sync agentpack dependencies
  run: agentpack sync
```

## Shared team packages

Create a private GitHub repository for team-specific agent skills and rules. Publish tagged releases and add it as a dependency in each project:

```toml
[dependencies]
"github.com/acme/team-agent-config" = "^1.0"
```

All projects stay in sync simply by running `agentpack lock && agentpack sync`.

## Per-developer overlays

For personal customizations that should not be committed, set `AGENTPACK_DOT_AGENTS` in your shell profile:

```sh
export AGENTPACK_DOT_AGENTS="$HOME/.my-personal-agents"
```

Files in this directory are staged into every harness on your machine only. Other team members are unaffected.

## Handling merge conflicts in pack.lock

`pack.lock` is TOML and is relatively merge-friendly, but conflicts can occur when two branches both update dependencies. The safest resolution is:

1. Accept either version of the conflicting section.
2. Run `agentpack lock` to recompute a clean lockfile from the merged `agentpack.toml`.
3. Commit the regenerated `pack.lock`.
