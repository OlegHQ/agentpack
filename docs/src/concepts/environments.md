# Portable Environments

Agentpack can keep `agentpack.toml` and `pack.lock` **outside** a git checkout so a teammate or CI job can restore the same locked inputs without committing harness-specific setup into every repository.

## Three roots

| Object | Role |
|---|---|
| **WorkspaceRoot** | Agent cwd / checkout. Source of `.agents/` inputs and any compatibility-mode overlays. |
| **DefinitionRoot** | Directory holding `agentpack.toml` and `pack.lock` (and definition-relative path deps). |
| **Binding** | Machine-local map from a workspace path to a definition root, stored under `$AGENTPACK_HOME/projects/<hash>/environment.toml`. |

Legacy `--project-root` alone keeps today's single-root workflow (definition = workspace). Prefer `--definition-root` and `--workspace` (or `agentpack env use`) when they differ.

## Commands

```sh
# Create an external definition (optionally copied from an existing project)
agentpack env init team --from /path/to/project
agentpack env use team --project /path/to/checkout   # alias: env bind

# Explicit selector (fails if missing; never falls through to another env)
agentpack --env team --workspace /path/to/checkout preflight --agent claude

# Share a locked bundle (manifest + pack.lock only; no secrets)
agentpack env export team.bundle --from team
agentpack env import team.bundle --name team

# Fetch locked cache trees and rebuild staging without amending pack.lock
agentpack env restore

# Pure offline check (no lock/cache/staging/repo writes)
agentpack preflight --agent claude --json --policy local
agentpack --strict-external preflight --agent opencode --policy ci --contract ./contract.json
```

Exit codes for `preflight` / `probe`: `0` ready (including warnings), `2` usage/policy error, `3` violated, `4` required observation unknown, `5` operational failure, `1` unexpected.

## Native probe

```sh
agentpack sync
agentpack probe --agent claude          # plugin list + skill inventory via --plugin-dir
agentpack probe --agent codex           # version only; skill catalog stays unknown
```

Probes are explicit (never inside pure preflight). Receipts land under `$AGENTPACK_HOME/projects/<hash>/probe-receipts/`. Claude skill *presence* can be observed; source digests and ambient `~/.claude/skills` remain unknown. Codex has no validated non-model skill catalog in 0.159.3.

```sh
agentpack support export RECEIPT_ID --output ./support-capsule.json
```

Support capsules are redacted (no credentials, no full digests, paths pseudonymized). Review before sharing; nothing uploads automatically.

Supported **strict external** launch targets (no checkout configuration writes): **Claude** (`--plugin-dir` / `--settings`) and **OpenCode** (`OPENCODE_CONFIG_DIR`). Cursor and Antigravity still require workspace overlays in compatibility mode; `--strict-external` refuses them before writing.

## Preflight vs `sync --verify-only`

`sync --verify-only` can still resolve dependencies, restore missing cache entries, upsert index rows, drop colliding staged skills, and rewrite the staged manifest. **`preflight` does none of that.** Missing cache in offline preflight is a finding with a restore remedy, not permission to fetch.

## Inheritance policy

- **`local` (default)** — report inherited user/global configuration as info/warnings.
- **`ci`** — same findings, with MCP pin gaps and (under `--strict-external`) undeclared inheritance treated as violations.

A green report means the enumerated configuration checks passed for the selected target and policy. It does not claim the agent is secure, or that LLM answers are reproducible.
