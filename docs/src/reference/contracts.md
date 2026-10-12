# Environment contracts and evidence

A `contract.json` beside an external definition expresses checks over its effective plan or an explicitly supplied native probe receipt. Preflight never executes the native CLI. Contract validation rejects unknown categories, properties, targets, selector keys, evidence levels, duplicate IDs, and allowances without a known requirement and rationale.

## Property registry

| Category | Properties |
| --- | --- |
| `skill`, `command`, `agent`, `rule` | `presence`, `source_digest`, `rendered_digest`, `source`, `module`, `winner`, `output_path`, `scope_preserved`, `source_kind`, `dropped_fields` |
| `plugin` | `presence`, `source_digest`, `source`, `module`, `winner` |
| `runtime` | `native_version` |
| `planned_write` | `workspace_configuration` |

Names are exact storage names or full artifact IDs; globs are unsupported. Omitted artifacts do not count as present. Package Markdown is parsed and rendered using the same parser, renderer and mode selectors as staging. `source_digest` hashes source bytes; `rendered_digest` hashes generated native Markdown bytes. Neither proves that a native CLI discovered those bytes. Logical plugin records describe locked packages, so select `minimum_evidence: "declared"` for locked plugin identity. Agentpack merges those packages into one native bundle.

Workspace `.agents` content follows its existing adapter routes. Claude raw overlay bytes and Codex skill/command projections are modeled. Other adapter-specific `.agents` seeding, and Codex rule guidance aggregation, remain declared rather than reporting an invented generated digest. A generated requirement for such a property stays unknown.

## Predicates

- `present` / `absent`: check a named artifact's presence, or planned workspace configuration writes. A positive observed check needs a matching observed property. Native absence needs a complete catalog for the requested category and scope, backed by an observed `catalog_complete` property; a coverage label alone is insufficient.
- `equals`: compare the selected property to `expected` using JSON value equality.
- `source_allowed`: compare `source`, `module`, or `winner` against an expected string or list of strings. It is an identity allow-list, not proof of publisher trust.
- `native_property`: equality that always requires observed evidence.
- `coverage_complete`: require complete native coverage for the category and scope; incomplete or unobserved coverage stays unknown.
- `no_workspace_write`: shorthand for absent `planned_write.workspace_configuration`. It checks Agentpack's plan, not every possible write by a launched program.

`minimum_evidence` defaults to `generated`. Observed evidence is never substituted from the package lock or rendered files. `severity: "warning"` and `"info"` keep a mismatch nonblocking; `"error"`, `"violation"`, and an omitted severity block. `unknown_required` defaults to `fail`; `warn` and `allow` relax unknown results explicitly. An allowance applies only to its named violated requirement, records `ALLOWED`, and cannot manufacture a missing observation.

## Native scope

Observation scope defaults to `native_catalog`. Current safe probes run in a controlled diagnostic projection without the user's ambient configuration. Their properties carry `scope: "controlled_projection"`. They cannot establish actual-launch ambient exclusion, instruction precedence, source attribution, or sandbox isolation. To check a positive managed skill under that bounded scope, declare it explicitly:

```json
{
  "schema_version": 1,
  "unknown_required": "fail",
  "requirements": [{
    "id": "review-loads-in-diagnostic-projection",
    "target": "codex",
    "selector": {
      "category": "skill",
      "name": "review",
      "property": "presence",
      "scope": "controlled_projection"
    },
    "predicate": "present",
    "minimum_evidence": "observed",
    "severity": "error"
  }],
  "allowances": []
}
```

A controlled positive observation does not require complete ambient catalog coverage. An unscoped observed requirement stays unknown when only controlled evidence is available.

### Session scope

`scope: "workspace_session"` describes a session started in the checkout with the staged configuration and an empty user home. The probe reads the agent's own catalog and sends no model request: the print-mode init event for Claude Code 2.1.296, `debug prompt-input` for Codex 0.159.2 and 0.159.3. Unlike the controlled projection it includes skills the checkout itself provides. It still excludes user-level configuration, so absence stays unknown.

For each packaged skill the receipt carries an observed `winner`:

| Value | Meaning |
| --- | --- |
| `package` | the staged copy is the only catalog entry with that name |
| `ambient` | Claude Code lists another skill under the bare name; the packaged copy is reachable only as `agentpack-bundle:<name>` or was dropped from the bundle |
| `ambiguous` | Codex lists several entries with that name |
| `unstable` | OpenCode 1.17.11 listed the staged file in some of 60 sampled runs and another file in others |

```json
{
  "id": "review-holds-its-name",
  "selector": {"category": "skill", "name": "review", "property": "winner", "scope": "workspace_session"},
  "predicate": "source_allowed",
  "expected": "package",
  "minimum_evidence": "observed"
}
```

The same requirement without a scope and with `minimum_evidence: "generated"` checks the planned winner from the workspace scan instead. The two grades disagree in useful ways. The scan flags `.claude/skills/<name>` for every target although Codex does not read that directory, and the observation shows Codex unaffected. A same-named `.agents/skills/<name>` replaces the packaged bytes inside the Claude bundle, which the Claude catalog cannot show because it lists names, not bytes; the scan reports it.

## Receipt freshness

Current receipts require source and lock identities, mode, workspace input digest, generation digest, policy digest, creation time, adapter revision, executable byte identity, capability revision, termination, and status. Old incomplete receipts fail closed with a re-probe remedy. Failed, cancelled, nonzero, partial/error-status receipts are retained as diagnostics but cannot satisfy contracts.

Preflight compares current identities before attaching properties. Workspace fingerprints cover `.agents`, workspace `.claude` / `.cursor`, `.codex/skills` and `.opencode/skills`, root `AGENTS.md` / `CLAUDE.md`, user skill roots, and known adapter configuration files; symlink contents are included and cycles fail. Generation identity additionally includes the staged manifest and relevant generated native configuration. A materialization input digest additionally detects an unchanged staged tree built from older inputs. Policy identity includes the selected policy, strict flag and contract bytes. The native executable is hashed without executing it. Upgrading adapter capabilities, changing configuration or instructions, rebuilding staging, or changing the policy invalidates evidence.

Every observed property needs readable retained diagnostic evidence with the recorded byte digest. Missing or modified evidence is unavailable; the digest is a local integrity check, not an attestation. Raw evidence and diagnostic paths are excluded from support exports.

`--receipt latest` selects the newest successful current-format receipt. It does not fall back silently when that receipt is stale. Where no receipt exists, preflight reports `RECEIPT_MISSING` and required observations stay unknown. A receipt is local evidence, not a signed attestation. Fingerprints cannot establish unrecorded parent-directory instructions, arbitrary process environment, remote MCP state, model behavior, or external interpreters/dependencies behind an executable. Treat these as outside the closure.

Under CI policy, existing undeclared native configuration and skill roots cause violations. Descriptive capability/inheritance notes are informational; a clean controlled home is not rejected merely because the adapter can inherit configuration.


## Comparing receipts

Comparison separates observation categories and scopes, marks differing native versions or failed processes incompatible, and compares JSON values without coercing strings into booleans or numbers. Source, lock, mode, policy, workspace and generation identities appear as separate deltas. Missing properties and properties without observed evidence remain unknown.
