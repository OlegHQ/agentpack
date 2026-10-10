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

# Share validated manifest, unchanged lock, and optional contract
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
agentpack probe --agent codex           # validated positive static-projection discovery
agentpack --strict-external probe --agent claude --policy ci
agentpack --strict-external preflight --agent claude --policy ci --receipt latest
```

Probes are explicit (never inside pure preflight). Receipts land under `$AGENTPACK_HOME/projects/<hash>/probe-receipts/`. Claude plugin/skill presence and Codex 0.159.2 positive skill discovery can be observed in isolated `controlled_projection` scope. Require that scope explicitly in a contract. Full interactive configuration, source digests, model use and complete ambient absence remain unknown; Codex 0.159.3 has version-only coverage. `--policy` and `--strict-external` on a probe bind receipt freshness to the later preflight policy, and `--receipt latest` selects the latest successful current-format record.

```sh
agentpack support export RECEIPT_ID --output ./support-capsule.json
```

Support capsules retain diagnostic codes, evidence grades, coverage, status, and public binary versions. They omit free-form native messages, remedies, arbitrary property values, evidence paths, and private artifact names because these can contain credentials. Review before sharing; nothing uploads automatically.

Supported **strict external** launch targets (no checkout configuration writes): **Claude** (`--plugin-dir` / `--settings`) and **OpenCode** (`OPENCODE_CONFIG_DIR`). Cursor and Antigravity still require workspace overlays in compatibility mode; `--strict-external` refuses them before writing.

## Preflight vs `sync --verify-only`

`sync --verify-only` can still resolve dependencies, restore missing cache entries, upsert index rows, drop colliding staged skills, and rewrite the staged manifest. **`preflight` does none of that.** Missing cache in offline preflight is a finding with a restore remedy, not permission to fetch.

## Inheritance policy

- **`local` (default)** — report inherited user/global configuration as info/warnings.
- **`ci`** — same findings, with MCP pin gaps and (under `--strict-external`) undeclared inheritance treated as violations.

A green report means the enumerated configuration checks passed for the selected target and policy. It does not claim the agent is secure, or that LLM answers are reproducible.

## Safe sharing

Exports contain `agentpack.toml`, the unchanged `pack.lock`, optional `contract.json`, and a versioned per-file SHA-256 manifest. Bindings, caches, receipts, raw logs, auth, and history are excluded. Export refuses symlinked definition files, inline MCP environment values, recognizable credential arguments or URL credentials, and host-private absolute paths; supply secrets through the launching environment. This conservative check cannot infer every possible secret embedded in an arbitrary string. Review the definition before sharing.

Host-local path dependencies are refused, including relative paths: the current bundle does not include their file trees. Publish these resources as pinned GitHub packages before exporting. A bundle is a portable definition, not an offline cache archive; restore still needs the locked remote inputs or a verified cache.

Import accepts only the documented regular files, rejects duplicates, traversal, symlinks, files over 4 MiB, unsupported schemas, invalid definitions/contracts/locks, and digest mismatches. It validates in a private temporary directory and publishes only when complete. The destination must not already exist, and export also refuses an existing output file. Older bundles without `bundle.json` must be re-exported. SHA-256 protects against corruption, not an untrusted publisher: inspect a received definition before launching it.

## Rebuild recovery and active launches

Service rebuilds hold an exclusive staging lock through verification. An active launch holds a shared lease, so rebuild returns a clear “in use” error rather than modifying that session's files. The staging-root override is partitioned by definition identity; Grok homes are partitioned by mode. The changed layout requires a fresh sync after upgrading. Existing native credential/history sources continue to be used.

Codex uses published immutable home generations. Other adapters retain stable staging paths: rebuild saves prior trees and the staged-content manifest, restores them on returned errors, and removes backups after successful verification. A durable journal is saved before roots are renamed. If the process is terminated during rebuilding, the next restore/rebuild recovers the prior trees before native state preparation; launch fast paths refuse pending recovery journals. Completed publication is marked before backups are removed, so cleanup interruptions retain the new trees. Recovery accepts only the exact managed roots for that project/mode and refuses symlinked backup directories. This is recoverable publication, not a single crash-atomic pointer switch across every adapter or a guarantee against storage hardware failure. Auth/history symlinks are moved as links and are never followed during rollback. Compatibility-mode workspace overlays remain adapter-managed; strict external mode refuses targets requiring them.

Claude's allowlist and attribution settings are stored per project/mode and passed as inline `--settings` JSON. They cannot overwrite another environment's allowlist, and `CLAUDE_CONFIG_DIR` stays unchanged so the native credential namespace remains available.

`env init --from` validates a complete copy before publication and includes an optional contract. Relative local dependencies are rebased to their original absolute source paths so relocation cannot silently select a different local package. These copies remain machine-local and cannot be exported until path dependencies are replaced by pinned remote packages. New destinations publish atomically; existing empty user directories retain their identity, with no-overwrite file publication and rollback on returned failures. Initialization refuses nonempty destinations.
