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

## Receipt freshness

Current receipts require source and lock identities, mode, workspace input digest, generation digest, policy digest, creation time, adapter revision, executable byte identity, capability revision, termination, and status. Old incomplete receipts fail closed with a re-probe remedy. Failed, cancelled, nonzero, partial/error-status receipts are retained as diagnostics but cannot satisfy contracts.

Preflight compares current identities before attaching properties. Workspace fingerprints cover `.agents`, workspace `.claude` / `.cursor`, root `AGENTS.md` / `CLAUDE.md`, user skill roots, and known adapter configuration files; symlink contents are included and cycles fail. Generation identity additionally includes the staged manifest and relevant generated native configuration. A materialization input digest additionally detects an unchanged staged tree built from older inputs. Policy identity includes the selected policy, strict flag and contract bytes. The native executable is hashed without executing it. Upgrading adapter capabilities, changing configuration or instructions, rebuilding staging, or changing the policy invalidates evidence.

Every observed property needs readable retained diagnostic evidence with the recorded byte digest. Missing or modified evidence is unavailable; the digest is a local integrity check, not an attestation. Raw evidence and diagnostic paths are excluded from support exports.

`--receipt latest` selects the newest successful current-format receipt. It does not fall back silently when that receipt is stale. A receipt is local evidence, not a signed attestation. Fingerprints cannot establish unrecorded parent-directory instructions, arbitrary process environment, remote MCP state, model behavior, or external interpreters/dependencies behind an executable. Treat these as outside the closure.

Under CI policy, existing undeclared native configuration and skill roots cause violations. Descriptive capability/inheritance notes are informational; a clean controlled home is not rejected merely because the adapter can inherit configuration.


## Comparing receipts

Comparison separates observation categories and scopes, marks differing native versions or failed processes incompatible, and compares JSON values without coercing strings into booleans or numbers. Source, lock, mode, policy, workspace and generation identities appear as separate deltas. Missing properties and properties without observed evidence remain unknown.
