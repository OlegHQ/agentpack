## Current reconciliation (Phase 0)

**Start commit:** `89f63b2801e3f907ee1b010a3a61407aab155075` (matches reference audit).

| Area | Status | Notes |
| - | - | - |
| Context roots (`DefinitionRoot` / `WorkspaceRoot`) | **completed** | `internal/paths/context.go`; legacy `--project-root` unchanged |
| Env store / bind / export / import / restore CLI | **completed** | `env use`/`bind`; `--env`; frozen restore publishes staging |
| Pure preflight | **partial** | Artifacts + winners + ambient; LookPath only; not full IR merge graph |
| Strict external overlays | **completed** | Cursor/Agy refused under `--strict-external` |
| Frozen restore | **completed** | Lock-byte + missing-input fail; staging rebuild via `Frozen` sync |
| Contracts + graded evidence | **partial** | schema/examples; observed predicates stay unknown without receipt |
| Native probe / receipts | **partial** | Claude plugin/skill presence; Codex version; receipts + freshness |
| Compare / support capsule | **completed** | `config compare` + redacted `support export` |
| Ambient/precedence faults | **completed** | CONFIG_SHADOWED + AMBIENT_ARTIFACT with artifact winners |
| Exit codes 0/2/3/4/5 | **completed** | Preflight/probe map overall_status → exit codes |
| R12 legacy workflows | **completed** | Existing launchers/sync preserved |

**Command mapping (reuse, do not rename):** `env use`→`env bind`; `--definition-root`/`--env`→selector; `env restore`→`restore --frozen`; `preflight` stays top-level.

**Ultra scope cut:** skip inventing native observations and advertising observed discovery for Cursor/Agy/Grok.

### R01–R12 progress (working tree)

| ID | Status | Evidence |
| - | - | - |
| R01 | **completed** | Integration + unit: init/bind/preflight leave checkout empty |
| R02 | **completed** | Lock-byte unchanged; missing locked input fails without lock rewrite |
| R03 | **completed** | Preflight mutates neither lock nor checkout; no child processes |
| R04 | **completed** | Artifact winners + planned writes; strict planned writes match sync refusal |
| R05 | **completed** | Declared/generated/observed/unknown; contracts do not forge digests |
| R06 | **completed** | Absent+observed without coverage → unknown |
| R07 | **completed** | Strict-external + invalid `--env` fail explicitly |
| R08 | **completed** | Shared Report; exit codes 0/2/3/4/5 |
| R09 | **completed** | Continuity reused; capsule/bundle exclude credentials; no new token copies |
| R10 | **completed** | Frozen staging publish; shared launch / exclusive rebuild flock; Codex generation leases retained |
| R11 | **completed** | Bundle + redacted support capsule; no secrets by default |
| R12 | **completed** | Legacy workflow preserved; suite green |

---

## Objective and implementation handoff

Implement a focused extension to agentpack that makes an externally locked CLI-agent environment inspectable and testable. A developer must be able to select a shared environment without adding configuration to the working repository, see how agentpack translates and composes it, and distinguish generated configuration from native CLI observations. CI must validate declared requirements without updating locks or executing agents.

This is an implementation specification, not authorization to modify code in this chat. The implementing agent should use it as the goal in its own authorized repository task.

### Handoff instruction for the implementing agent

First inspect the current checkout, repository instructions, working changes, and the external-lock/config/preflight implementation already underway. Preserve that work. Map these requirements to existing APIs and commands before adding abstractions. Do not reimplement a feature merely because this specification uses a different name. Record the actual starting commit and identify completed, partial, missing, and incompatible requirements. Then implement the smallest coherent workflow below, validate it, and return evidence and limitations.

The reference audit used OlegHQ/agentpack revision 89f63b2801e3f907ee1b010a3a61407aab155075. New source takes precedence over the module observations in this specification. See the [reference revision](https://github.com/OlegHQ/agentpack/commit/89f63b2801e3f907ee1b010a3a61407aab155075).

### Product goal

A developer can answer four questions:

1. Which environment and exact package inputs did I select?

2. What configuration will agentpack generate, inherit, replace, or omit for this CLI?

3. What has the native CLI actually confirmed, and what remains unknown?

4. How do I fix a mismatch without editing the project or losing native login/history?

Initial users are teams sharing private packs, developers switching client or task profiles, evaluation maintainers, and pack authors checking compatibility.

### Scope and release boundary

The initial release covers external definitions and locks, explicit environment selection, effective configuration planning, pure preflight, native observation receipts, report comparison, and actionable recovery. Implement useful native observation for two validated CLI adapters. Claude Code and Codex are candidates, not guaranteed winners; choose after a short capability spike.

Retain all existing project-based workflows and six adapters. Only advertise the new strict external workflow for tested combinations of adapter, native CLI version, artifact kind, and policy. Other adapters can support planning without claiming observed native discovery.

Do not add a new agent framework, registry, GUI, LLM replay system, sandbox, automatic skill router, or model-quality optimizer. Do not rename agentpack.

### Core acceptance gate

The release must demonstrate the same portable locked environment in two relocated checkouts; two native adapters with useful supported observations; two ambient-input or precedence faults missed by package-only checks; one actionable recovery; clean controls; and no agentpack configuration writes to the checkout in strict external mode.

If native inspection is insufficient, deliver static planning with explicitly unknown native properties and narrow the release claim. Do not invent observations to meet the gate.

## Requirements and boundaries

Use MUST for release requirements, SHOULD for preferred behavior, and MAY for later conveniences. Keep optional functionality out of the critical path.

### Required invariants

| ID | Requirement | Acceptance evidence |
| - | - | - |
| R01 | External definition and lock are separate from WorkspaceRoot | Creating, selecting, restoring, and checking an external environment does not create manifest/lock/config files in the checkout |
| R02 | Frozen restore never changes resolved pins or lock bytes | Cold and warm restores preserve lock hash; unavailable locked input fails |
| R03 | Static preflight is nonmutating | No cache/index/staging/lock/binding/config updates, network, child processes, native version queries, hooks, or MCP connections |
| R04 | Effective plan drives materialization and reporting | Reported outputs/winners/omissions match the generated target configuration |
| R05 | Evidence is graded by property | Declared and generated artifacts never become native observations by implication |
| R06 | Negative observations require complete relevant inspection | Missing catalog entry is not confirmed absence when inspection is partial |
| R07 | Unsupported strict requirements fail explicitly | No silent fallback that writes the repository or discards requested artifacts |
| R08 | Human and JSON output agree | Shared findings, stable codes, consistent policy outcomes and exit codes |
| R09 | Authentication and history continuity remain explicit | Supported native handling; no blind refresh-token copies or credential export |
| R10 | Published generations are immutable and concurrency-safe | Failed restore leaves prior generation usable; active launch not deleted/rebuilt |
| R11 | Portable export excludes machine-private state | No credentials, history, local bindings, absolute private paths, or raw native logs by default |
| R12 | Existing workflows remain compatible | Existing command contracts and lock readers continue to work; intentional changes documented |

### Configuration writes versus agent task edits

The strict guarantee covers agentpack writing, replacing, deleting, chmod-ing, or symlinking configuration inside the workspace or real native global configuration roots. It includes ignored and untracked paths. A clean Git diff is insufficient.

Normal coding-agent task edits are outside this guarantee. Native runtime writes such as authentication, history, logs, and session databases must have a documented destination and ownership policy. Probe writes occur only in declared managed diagnostic storage, except explicitly documented unavoidable native effects; such effects cannot be described as zero-global-write behavior.

Preflight may read project/native configuration as declared inputs but must not update it. Reading files may update filesystem access times; mutation tests should compare content, permissions, links, and owned state rather than ordinary read access times.

### Meaning of reproducibility

A source lock fixes the declared package inputs covered by the existing integrity model. A target configuration identity includes rendering and declared ambient inputs. A receipt records an observation at a particular time and native CLI version.

These do not establish deterministic model answers, arbitrary natural-language equivalence, successful task behavior, absence of prompt injection, full runtime hermeticity, or complete remote-MCP dependency closure. A file read is not proof that the model received the text; a discovered skill is not proof it was used.

Remote endpoints, unpinned launchers, dynamic discovery, managed settings, secret-dependent behavior, and unobservable native sources must remain visible as coverage limits.

### Required phase separation

| Phase | Allowed effects | Forbidden implicit behavior |
| - | - | - |
| Resolve or update | Explicit network acquisition and new lock creation in external storage | Change an environment used by another active session |
| Restore | Fetch exact locked inputs, verify, render, publish a managed generation | Resolve new versions or amend the supplied lock |
| Plan or preflight | Read existing files and metadata; produce report on stdout | Download, repair, create layout, write reports/state, spawn any CLI |
| Probe | Explicit native diagnostic execution with declared effects | Hidden execution inside preflight; arbitrary production hook/server execution |
| Launch | Start native CLI using selected generation and declared continuity policy | Silent unsupported-target fallback or auto-update |
| Export | Explicit creation of a portable bundle or sanitized report | Automatic upload or inclusion of secrets |
| Cleanup | Remove only identified inactive agentpack-owned storage | Follow links into workspace/native homes or delete active generations |

Strict configuration policy is not a sandbox. It cannot prevent a launched CLI from dynamically reading unrelated files. State separately which boundaries are configured, observed, or unsupported.

### Compatibility and inheritance policy

Provide an understandable default inheritance policy for interactive use and an explicit controlled policy for CI. Preserve existing behavior by default in the legacy project workflow.

For external mode, users must see whether native global/project/managed configuration may be inherited. Unknown inheritance is not permission to silently disable project guidance. A strict contract can reject unknown required properties; a permissive contract can proceed while displaying them.

The initial controlled mode must declare allowed artifact sources and merges. Use narrowly scoped allowances with finding code, target, artifact/property identity, and rationale. Avoid blanket ignore-all switches. The same exception must apply to terminal and CI reports. Invalid policy is a usage/configuration error, not a clean report.

An empty project, detached worktree, nested cwd, and checkout containing pre-existing native configuration must each have defined behavior.

## Architecture and data contracts

### Separate context roots

Introduce or reuse a context with WorkspaceRoot, DefinitionRoot, external environment selector, selected mode, inheritance policy, and execution phase.

WorkspaceRoot is the native agent cwd and base for explicitly workspace-relative inputs. DefinitionRoot contains the external manifest and lock and is the base for definition-relative package resources. A local binding maps a canonical checkout identity to an environment alias or source ID outside the checkout.

Never emulate external definitions by changing today's project root to the definition directory: that confuses cwd, local dependencies, project guidance, and overlay destinations. Specify relative-path semantics explicitly. Reject portable export of undeclared host-local inputs, or materialize them into the bundle with explicit inclusion.

Selector precedence MUST be: explicit invocation selector; explicit local binding; existing project manifest discovery when no external selector/binding applies. An invalid explicit selector must fail rather than fall through to another environment. Binding an existing project must state which environment wins. Binding does not modify project files.

### Managed storage layout

Adapt to current path conventions. The following logical objects are required, not prescribed directory names:

- Portable definitions and their immutable locked revisions.

- Machine-local bindings outside the checkout.

- Verified package cache.

- Immutable per-target configuration generations.

- Mutable auth/history/session state separated from generation content.

- Native probe receipts and bounded raw evidence in private managed storage.

- Explicit exports.

Use an environment source ID independent of host paths. Generations include source ID, target identity, mode, policy, renderer/adapter revision, and declared ambient-input identity. A local alias is mutable; an immutable source ID is not. Launch must report the resolved source ID, not just the alias.

Acquire per-environment/generation locks and launch leases using existing primitives where possible. Render into a private temporary generation, verify it, then atomically publish. A generation must never be reset while another session uses it. Garbage collection must honor ownership markers and active leases and must not traverse symlinks into user storage.

### Reuse map from the audited revision

| Existing area | Reuse | Extension |
| - | - | - |
| internal/paths and internal/cli | Managed locations, discovery, current command conventions | Root separation, external selector/binding, clear output |
| Lockfile, resolver, cache, internal/sync | Exact resolution, hashes, repair and modes | Frozen restore and pure validation paths |
| internal/artifacts | Parsed IR and target rendering | Source-to-output derivation and property support records |
| internal/hooks | Origin and native/emulated/degraded/unsupported status | Preserve diagnostics through staging and CLI |
| internal/staging | Composition, collisions, manifest and output layout | Pure effective plan, planned writes, inherited sources |
| internal/harness | Native launch flags and lifecycle contracts | Versioned capabilities and observation interface |
| Codex generations | Transactional generations and leases | Reuse semantics for external generations |
| Existing diagnostics | Event/log facilities | Private bounded probe evidence and redacted export |

Do not require a new package named scope. Suggested cohesive areas are environment, configuration planning, and contracts, but follow repository architecture and avoid duplicated resolution or render pipelines.

### Portable identity

Use the existing digest algorithm where applicable. Document a canonical versioned identity envelope containing normalized locked inputs, definition semantic digest, selected portable contract/policy, and included local-resource digests. Include schema/digest algorithm identifiers.

Exclude timestamps, local aliases, absolute workspace paths, credentials, receipt IDs, and raw environment values from the portable source identity. Canonicalization MUST preserve semantically significant sequence order; sorting map keys does not authorize reordering instruction arrays.

Do not silently conflate raw file integrity with semantic identity. Keep the lock byte hash as well as a versioned source identity when both are needed. Changes to normalization/schema require a new identity version.

A target identity adds adapter/render revision, target executable identity, verified native version, selected mode, declared inheritance policy, and relevant input digests. A version string is useful metadata but alone is not a binary identity. For script launchers, record the launcher and explicitly note underlying runtime gaps.

Receipt freshness is invalidated by any observed-input change, contract/policy/mode change, executable/adapter change, or generation change. Mutable ambient configuration must be fingerprinted at check/launch time where readable. Revalidation reduces drift risk; it does not eliminate a time-of-check/time-of-use race. A copied pinned input belongs to the generation; a mutable native-discovered source remains a limitation.

### Effective plan

Build a pure planner returning requested artifacts, inherited sources, transformations, winners, omissions, unresolved properties, and all intended writes. It may read supplied inputs and existing cache; it must not initialize cache or staging.

Each artifact record requires a stable identity scoped by package source, source path, artifact kind, and logical name. Duplicate names from distinct sources remain distinct records. Record provenance, target output path/digest, selected mode, support status, and omission/winner reason.

Property support is one of native, translated, prose_only, omitted, unsupported, or unknown. A rule scope rendered as instruction prose must be prose_only, never native enforcement. A property can be represented natively and still have unknown runtime discovery; support status and evidence level are separate dimensions.

Represent precedence as a directed ordered merge/winner explanation, including list merging where applicable. Never guess native precedence for unvalidated versions. Distinguish package artifacts, workspace-native inputs, user-global inputs, managed policy, environment/CLI overrides, and unclassified inputs.

The materializer consumes the accepted plan. If an input changes before publication, abort/replan rather than publish files inconsistent with the report.

### Configuration contract

Add a versioned contract attached to the external definition or an explicitly selected policy file. It expresses required presence, forbidden named ambient artifacts, required support properties, allowed sources/inheritance, and minimum evidence.

For each requirement specify stable ID, target/artifact/property selector, expected value or predicate, minimum evidence, severity, and optional scoped allowance rationale. Evaluate predicates independently of renderer success.

Supported predicates in v1: artifact present; named artifact absent; expected source/digest; field value equal; artifact source allowed; property natively represented; no planned workspace/global-config write; native discovery coverage complete for a specified category. Do not implement natural-language prompt equivalence or behavioral predicates.

Every result is satisfied, violated, unknown, or not_applicable. Unknown and not_applicable are distinct. Not_applicable applies only when the contract itself scopes the requirement out, not when observation fails. Strict required unknown results fail policy.

### Findings and JSON report

All commands produce the same versioned report model, with additive fields allowed within a schema version. Reject unknown future major versions on import. Provide a JSON schema and validated examples.

Required report fields: schema_version, phase, source_id, lock_digest, generation_id when available, target identity/capability revision, mode, policy digest, input coverage, artifact/property records, findings, contract results, observed evidence references, and overall status.

Each finding includes code, severity, target, artifact/property identity if applicable, source, message, consequence, remediation, evidence level, and related records. Remediation is structured: explanation plus command tokens when an actual implemented remedy exists. Do not build executable shell strings from source-controlled paths.

Recommended stable codes include ENV_NOT_FOUND, LOCK_MISMATCH, LOCKED_INPUT_UNAVAILABLE, CONTENT_DRIFT, UNSUPPORTED_TARGET, WORKSPACE_WRITE_REQUIRED, CONFIG_SHADOWED, RULE_SCOPE_DEGRADED, HOOK_UNSUPPORTED, AMBIENT_ARTIFACT, NATIVE_VERSION_UNVALIDATED, OBSERVATION_UNAVAILABLE, OBSERVATION_PARTIAL, RECEIPT_STALE, AUTH_REQUIRED, SECRET_REQUIRED, and PROBE_FAILED.

Overall statuses are ready, ready_with_warnings, violated, unknown, or error. Derive them from a single policy evaluator. Never print a security/safety guarantee.

### Native observation receipt

A receipt includes ID, creation time, contract/source/generation/input identities, native executable/version, probe strategy/version, actual launch environment summary with values redacted, observed properties, completeness per category, evidence references, findings, termination status, and declared effects.

Evidence levels are declared, generated, observed, and unknown. Observed records require the method and bounded evidence. A source file parser proves source content; a filesystem read trace proves accessed bytes; a native catalog API proves reported discovery; none automatically proves model context or use.

Negative presence claims require authoritative complete inspection for the named category and target version. Otherwise return unknown. An adapter-wide observed flag is forbidden; confidence and completeness are per property/category.

The receipt is local evidence, not a signed publisher attestation. If signing is added later, explain whose identity signed which claim.

### Required v1 contract and receipt shapes

The following JSON is a normative shape proposal. Adapt field names to existing shipped schemas only if the implementing agent documents an equivalent mapping and tests it. IDs and digests below are illustrative.

```json
{
  "schema_version": 1,
  "requirements": [
    {
      "id": "review-skill-source",
      "target": "claude",
      "selector": {
        "category": "skill",
        "name": "review",
        "property": "source_digest"
      },
      "predicate": "equals",
      "expected": "sha256:example",
      "minimum_evidence": "observed",
      "severity": "error"
    },
    {
      "id": "no-ambient-test-helper",
      "target": "claude",
      "selector": {
        "category": "skill",
        "name": "ambient-test-helper",
        "property": "presence"
      },
      "predicate": "absent",
      "minimum_evidence": "observed",
      "severity": "error"
    },
    {
      "id": "no-checkout-config-writes",
      "target": "*",
      "selector": {
        "category": "planned_write",
        "property": "workspace_configuration"
      },
      "predicate": "absent",
      "minimum_evidence": "generated",
      "severity": "error"
    }
  ],
  "allowances": [],
  "unknown_required": "fail"
}
```

The first requirement is unknown if the native interface reports a name but cannot attribute its bytes/source. Do not substitute the package digest for a native observed digest. The third concerns the plan; filesystem write tests separately establish implementation conformance.

Define selectors with an explicit category-specific property registry. Unknown property/predicate, duplicate requirement ID, unsupported minimum evidence, invalid wildcard, or contradictory unscoped requirements are validation errors. Empty requirements are allowed for inspection but must not produce an implied isolation guarantee. Never evaluate arbitrary expressions supplied by a pack.

```json
{
  "schema_version": 1,
  "phase": "probe",
  "receipt_id": "example-receipt",
  "source_id": "sha256:source",
  "lock_digest": "sha256:lock",
  "generation_id": "sha256:generation",
  "mode": "review",
  "policy_digest": "sha256:policy",
  "target": {
    "adapter": "claude",
    "adapter_revision": "example",
    "executable_identity": "sha256:binary",
    "native_version": "tested-version",
    "capability_revision": "example"
  },
  "input_fingerprint": "sha256:inputs",
  "created_at": "2026-10-10T20:00:00Z",
  "coverage": [
    {
      "category": "skill",
      "scope": "native_catalog",
      "completeness": "partial",
      "reason": "The interface does not expose source bytes."
    }
  ],
  "properties": [
    {
      "artifact_id": "package/path/review",
      "property": "presence",
      "value": true,
      "evidence": "observed",
      "method": "native_catalog",
      "evidence_ref": "local-evidence-1"
    },
    {
      "artifact_id": "package/path/review",
      "property": "source_digest",
      "value": null,
      "evidence": "unknown",
      "reason": "Native interface does not attribute source bytes."
    }
  ],
  "contract_results": [
    {
      "requirement_id": "review-skill-source",
      "result": "unknown",
      "finding_code": "OBSERVATION_UNAVAILABLE"
    }
  ],
  "findings": [
    {
      "code": "OBSERVATION_UNAVAILABLE",
      "severity": "error",
      "target": "claude",
      "artifact_id": "package/path/review",
      "property": "source_digest",
      "source": "native_catalog",
      "message": "Skill presence was observed but its source bytes were not.",
      "consequence": "The required source-digest check cannot be satisfied.",
      "remediation": {
        "message": "Use a supported observation strategy or explicitly narrow the contract.",
        "argv": null
      },
      "evidence": "unknown",
      "related_records": ["local-evidence-1"]
    }
  ],
  "effects": {
    "model_calls": false,
    "network": "none_observed",
    "write_scope": "managed_diagnostic_storage"
  },
  "termination": {
    "status": "completed",
    "native_exit_code": 0
  },
  "overall_status": "unknown"
}
```

A successful native process exit is not a satisfied contract. Operational termination and policy status are independent. The partial coverage example does not prove absence of ambient-test-helper. Effects labels distinguish planned, configured, and measured effects; none_observed must refer to measured evidence, not a no-network assumption.

Use UTC timestamps in receipts and local formatting in terminal output. Timestamps do not enter portable IDs. Store raw evidence privately with bounded retention and ownership, retaining references across comparison; if deleted, report evidence unavailable rather than reconstructing it.

Deliver schema files, valid examples, invalid examples, and compatibility tests. Production code should construct reports from typed records, not concatenate JSON strings.

### Support exports

Portable bundle export contains the manifest, unchanged lock, versioned contract, and explicitly included resources. It excludes bindings, receipts, secrets, auth/history, and unmanaged absolute paths. Import validates path traversal, symlinks, entry sizes, digests, and schema before publishing; partial import must not replace a working environment.

Support capsule export is separate and opt-in. By default pseudonymize private names/paths, exclude raw logs and environment values, and include only useful diagnostic records and public binary versions. Stable hashes of private predictable names are not sufficient anonymization. Display a preview/manifest and sharing scope. Never upload automatically.

## CLI and developer experience

### Command integration

The syntax below is proposed. If the other agent already implemented equivalents, reuse them and publish the mapping in documentation. Avoid redundant command families. The required behaviors are independent of exact spelling.

```text
agentpack env import ./team-environment.bundle
agentpack env bind team-env --project /path/to/checkout
agentpack env status --project /path/to/checkout

agentpack restore --env team-env --project /path/to/checkout --frozen
agentpack preflight --env team-env --project /path/to/checkout --agent claude
agentpack probe --env team-env --project /path/to/checkout --agent claude
agentpack preflight --env team-env --project /path/to/checkout --agent claude --receipt RECEIPT_ID --json

agentpack claude --env team-env
agentpack config compare RECEIPT_A RECEIPT_B
agentpack support export RECEIPT_ID --output ./support-capsule.json
```

Use existing launcher names and argument forwarding conventions. Separate agentpack arguments from native arguments with a documented boundary; reject ambiguous selector flags rather than forward accidentally. No command shown as a remedy may be unimplemented.

### First run

Import or select an external definition without running init in the working repository. Print resolved source ID, supported targets, storage location, and next action. Binding is optional when an explicit selector is used. Warn before replacing an existing binding; provide a noninteractive explicit replacement flag.

Restore explains acquisition before starting, verifies existing locked inputs, publishes configuration, and reports the generation. A private-source auth error is not a generic missing package error. Do not save successful state on failed acquisition.

Preflight shows one useful summary followed by actionable findings. Do not require the developer to understand IR, graph nodes, or evidence terminology to fix a problem.

### Example human output

```text
Environment: team-review @ sha256:...
Target: Claude Code [validated version]
Mode: review
Configuration: external; no workspace configuration writes planned
Checks: 18 passed, 1 warning, 1 unknown
Native observations: no current receipt

Warning CONFIG_SHADOWED
The user skill named test-helper overrides the packaged skill.
Source: user configuration
Effect: this launch will not use the packaged version.
Next: choose the documented inheritance policy or explicitly accept this source.

Unknown OBSERVATION_UNAVAILABLE
The native CLI has not confirmed skill discovery.
Next: run the explicit native probe, or use a policy limited to generated configuration.
```

Do not display “this launch will not use” if only generation precedence is known and native precedence is not validated; use “agentpack omitted the packaged artifact” in that case. Messages must match evidence strength.

### Output and exit codes

JSON mode emits one JSON document to stdout, with no banners, progress, ANSI escapes, or prompts. Human progress and errors go to stderr. Respect quiet/noninteractive/no-color behavior and existing completion/help conventions. Explicit output-file creation belongs to export or shell redirection, not pure preflight.

Use existing compatible exit conventions if present. If defining new commands, use: 0 policy satisfied, including permitted warnings; 2 invalid invocation/definition/policy; 3 policy violated; 4 required observation unknown or stale; 5 acquisition/native probe operational failure; 1 unexpected internal failure. Cancellation uses the platform convention, commonly 130. Document deterministic precedence when multiple findings coexist: internal/operational failure, usage error, violation, unknown, success. Legacy commands retain their prior codes.

### Recovery journeys

| Situation | Required explanation and recovery |
| - | - |
| Missing cache offline | Name exact missing locked input; recommend explicit restore, not update |
| Tampered input | Name covered digest mismatch; suggest existing explicit repair when supported; retain lock |
| Private source access missing | Identify credential mechanism without revealing tokens; avoid empty successful hashes |
| Artifact shadowed | Name winning source and whether agentpack or native CLI chose it; offer scoped policy |
| Native CLI outside validated versions | Continue only at supported evidence level; strict observed contract fails unknown |
| Authentication required | Explain native supported login scope; no silent token copying |
| External mode requires project writes | Reject before materialization; offer explicitly named legacy mode |
| Probe failed or incomplete | Preserve failure evidence; no successful receipt; list incomplete categories |
| Concurrent restore or cleanup | Wait/fail clearly under bounded locking; retain active generation |
| Update requested | Show source/effective configuration delta before new alias publication |

Update preview is desirable once the core workflow works. It should show added/removed artifacts, changed source/digests, support losses, newly inherited/overridden inputs, and observation invalidation. It must not promise behavioral impact.

### Comparison

Compare only compatible receipt categories. Show source lock, target version, mode, policy, inheritance, generated outputs, and observed category deltas separately. Mark unobserved fields unknown rather than equal. Different version/capability revisions may invalidate direct comparison; report that boundary. Do not compare secret values or infer causality from a configuration delta.

### CI workflow

A CI job first imports/selects an immutable environment ID, explicitly restores frozen inputs, then runs offline static preflight. Static jobs requiring generated configuration do not need a model, API key, or native CLI execution.

If CI requires native observation, run a separate probe job with controlled diagnostic capabilities, then evaluate a fresh matching receipt. A stored local receipt does not satisfy another machine's observed contract unless all required identities and observation scope match; even matching identities do not prove unrecorded ambient state.

Publish configuration reports as CI artifacts only with the chosen redaction scope. Preserve the exact lock used. Never repair or update during preflight. Failed checks must lead to an identifiable requirement and concrete recovery.

### UX acceptance

A new user can select an environment, understand inheritance, restore, identify one intentional mismatch, and recover using only command help and findings. Record where the user gets stuck before redesigning output. Defaults favor predictable existing behavior with explicit stricter policies, rather than breaking authentication/history for an apparently stronger isolation story.

## Native adapters and probes

### Capability model

Each adapter must declare capabilities for artifact kinds, native external paths, inherited configuration sources, startup inspection categories, strict source exclusion, write destinations, auth/history continuity, and validated native versions.

A capability entry contains adapter revision, native version range or exact tested versions, platform, artifact category/property, support level, observation strategy, completeness, and fixture references. Runtime probes on unvalidated versions may produce raw evidence but cannot silently claim validated semantics.

Capabilities are finer than a boolean supported flag. The absence of repository writes can depend on requested artifact kinds. Static planning across six renderers is not native conformance across six clients.

### Adapter selection spike

Try supported native inspection for Claude Code and Codex first. If one cannot provide useful non-model discovery observations, evaluate OpenCode. Choose based on demonstrated coverage, not brand count.

Deliver a small native capability record per candidate: exact binary/version, command/API used, outputs parsed, what is complete, what is unknown, auth effects, network behavior, config reads/writes, and passing/negative fixture cases. Do not scrape an interactive screen unless there is a stable tested method and a clear unsupported fallback.

### Audited adapter boundaries

- Claude already uses external plugin/settings paths. Verify user/project/managed merges and external MCP controls for the actual version. A status list of source files does not prove effective per-key values.

- Codex already uses CODEX_HOME and transactional generations. Verify native skill/instruction discovery beyond that home, authentication scope, and history continuity. Redirecting CODEX_HOME alone does not establish closure.

- OpenCode uses OPENCODE_CONFIG_DIR; check native merge rules and inherited configuration.

- Grok uses a redirected home; verify mode state separation and native discovery.

- Cursor workspace agent overlays and Antigravity bundle overlays existed in the audited source. Strict external mode must reject combinations still requiring those writes. Do not change ordinary support merely to claim strict support.

Do not insert undocumented bypass flags or patch installed native clients. Prefer official native interfaces; a wrapper that only parses generated files remains generated evidence.

### Observation interface

Keep planning independent from execution. A minimal Go design may expose adapter capability metadata, a pure static observation extractor, a probe planner, and an explicit probe executor. Exact method names are implementation choices.

ProbePlan must expose executable, fixed argv, cwd, environment-name mapping, readable/write roots, network requirement, timeout, process cleanup, supported observation categories, and potential hook/MCP execution before running.

The executor must use structured argv, never shell interpolation; honor cancellation and bounded stdout/stderr; use a disposable owned diagnostic directory; preserve failure output privately; and kill owned process groups on timeout. Do not kill unrelated native sessions.

Default probes must avoid model inference, arbitrary downloaded hook execution, and production MCP server startup. Use documented metadata/catalog inspection where available. If such execution is unavoidable, classify that probe separately with explicit opt-in effects and exclude it from the deterministic default conformance suite. A blocked/avoided path is unknown, not verified absent.

### Observation parsing

Version parsers and retain fixture outputs from native interfaces. Unexpected formats return OBSERVATION_PARTIAL or PROBE_FAILED; they must not become empty successful inventories. Bound payload sizes and tolerate irrelevant fields. Record native output errors distinctly from absent artifacts.

Include both positive and negative controlled sentinels. If a probe cannot discover a known-positive fixture, its negative claims are invalid. A parser unit test is necessary but does not replace native discovery validation.

Where native settings APIs expose effective values, report them with origins only when the origin is actually provided or independently validated. For array merges, identify constituents rather than inventing one winner. Never use an LLM to decide configuration closure.

### Freshness and launch validation

Immediately before strict launch, revalidate generation/source/policy and readable ambient fingerprints. Require a compatible receipt for properties requiring observed evidence, or refuse with an explicit probe remedy. Do not run the probe implicitly in a pure-check operation.

Receipt timestamps alone do not establish freshness. If fingerprints cannot cover a required source, report unknown. Active-session state and dynamic native discovery can change after validation; document this limit.

Interactive permissive launch may proceed without a receipt while reporting generated versus unknown categories. Strict CI requires the declared evidence level. Keep these policies consistent and avoid global “all checks passed” output hiding unknown required surfaces.

### Authentication and private inputs

Use supported native login/session mechanisms and existing agentpack continuity where validated. Never export credential files or solve refresh races by copying tokens into every environment. Separate immutable configuration generations from mutable auth stores.

Native settings can embed secrets. Parse only needed fields, retain secret presence/names without values, and ensure error/debug output does not leak them. A declared secret name does not prove a usable remote service. Endpoint identity and credential-dependent behavior remain bounded coverage.

Concurrency tests must cover two projects, two modes, restore during launch, and a failed probe, particularly any shared generated settings. Treat interference risks from the prior audit as tests to reproduce, not asserted bugs in the new code.

## Validation and delivery

### Implementation phases

| Phase | Tasks | Completion gate |
| - | - | - |
| 0 Current source reconciliation | Read new source, instructions, external work; map commands/modules and update capability gaps | Written completed/partial/missing matrix; no duplicated feature |
| 1 Pure planning and external context | Separate roots, stable IDs, frozen restore, effective plan, surface existing diagnostics | Relocated restore; no checkout configuration writes; static planner no mutation |
| 2 Contracts and reports | Versioned schema, policy evaluator, findings, unknowns, human/JSON consistency | Golden output, invalid policies, strict/permissive outcomes agree |
| 3 Native capability spike | Validate useful interfaces for two targets; define effects and supported versions | Positive/negative native fixtures and explicit coverage |
| 4 Probe receipts | Bounded executor/parser, freshness, explicit native probe, generation linkage | Native observations cannot be forged by file rendering; stale/partial fail correctly |
| 5 Recovery and comparison | Actionable remedies, report comparison, sanitized export, help/completion | Unaided newcomer task and recovery; no secret/default upload |
| 6 Evidence and release | Held-out fault fixtures, baseline workflows, native tests, public samples | Reproducible evidence bundle and complete handoff |

Do not wait for all ancillary improvements before testing native observability. If Phase 3 fails, keep useful static work and reduce the claimed contribution. The implementation is not complete merely because it compiles or mocked tests pass.

### Required automated tests

| Test | Expected result |
| - | - |
| External definition on clean checkout | No manifest, lock, .gitignore, native overlay, or binding in checkout |
| Checkout moved to another path | Same portable source identity; new external local association; correct cwd |
| Nested cwd and explicit project | Root semantics deterministic; no accidental overlay in cwd |
| Frozen restore with missing version | Fails without new resolution or lock changes |
| Static preflight missing state layout | No directories, indexes, logs, or temporary generations created |
| Static preflight tampered cache | Reports drift without repair; lock/cache unchanged |
| Rule scope fallback and unsupported hook | Correct support status and visible finding; no enforced-scope claim |
| Duplicate and inherited artifact | Exact source/winner or explicit unknown native precedence |
| Receipt for different policy/mode/generation | Stale/mismatched; cannot satisfy required observation |
| Native output changed/malformed | Partial/error, never empty successful inventory |
| Incomplete inventory missing named skill | Absence unknown, not satisfied |
| Strict required unknown versus allowed warning | Unknown fails; scoped allowed warning succeeds |
| Failed restore/interrupted publication | Prior generation remains usable |
| Two concurrent environments | No generated-settings overwrite or history/auth corruption |
| Cleanup with active lease/symlink | Retains active generation; never traverses into user files |
| Export/import traversal or unsupported schema | Rejects before publication; existing environment preserved |
| Private source/auth error | Actionable failure; no tokens or empty successful integrity fields |
| JSON mode and noninteractive invocation | Valid sole JSON stdout; no prompt; deterministic exit status |
| Existing project workflow | Relevant existing behavior and lock compatibility preserved |

Choose meaningful tests around real behavior and boundaries, not trivial mirrors of individual functions. Run required existing repository checks and targeted suites. Do not rewrite unrelated tests or launch production agents on uncontrolled user projects.

### Native conformance fixtures

Create a version-pinned fixture corpus with expected properties written independently of agentpack output. Use synthetic home/config roots and benign named artifacts.

Required categories: ambient user skill; project guidance; conflicting names; field override and array merge; scoped rule translation; unsupported hook; mode selection; relative resource path; changed cwd/worktree; stale generated bytes; native version change; missing native observation; and clean controls.

Only categories observable through validated native interfaces count as observed conformance. Keep inaccessible properties as unknown. The corpus can contain planned native cases without falsely labeling them passed.

Hold out mutation instances during development. Separate development fixtures from evaluation fixtures, while keeping the parser and format tests reproducible. Publish expected results and how ground truth was established.

### Configuration write and contamination measurement

Snapshot checkout content, ignored/untracked paths, symlink targets, file modes, and relevant real native config roots before/after preparation/check/probe. Separate expected external diagnostic/runtime writes and authorized agent task edits. Exclude volatile access times from the no-write oracle.

Prove pure preflight without relying only on an unchanged lock. Instrument network/process invocation boundaries and capture writes in isolated test directories. Scope measurements to tested platforms; do not imply untested Windows behavior from macOS/Linux tests.

### Minimum public artifact

Ship a usable binary or prebuilt package, a documented supported-version matrix, two small external environment examples, a static offline demo, native probe instructions and effects, a fixture runner, JSON schemas/examples, expected outcomes, and known limits.

A model/API key should not be required for static correctness checks. If a native probe requires credentials/network, state that requirement and keep an offline receipt-inspection example clearly labeled recorded evidence.

A 3–5 minute demo should show the same locked pack, an unexpected inherited artifact, the observation level, an actionable remedy or honest limitation, an unchanged checkout, and CI contract failure/pass. Every command must exist and work in the released artifact.

### Definition of done

- External and legacy workflows coexist without duplicate configuration pipelines.

- Frozen inputs survive relocated restoration with stable source identity.

- Preflight is genuinely nonmutating and never secretly launches probes.

- Existing diagnostics reach users in terminal and JSON.

- Two real native adapters have versioned, bounded observation evidence.

- Unknown and partial observation cannot become satisfied required absence.

- Reported effective composition matches materialization.

- Authentication/history handling is documented and tested within declared limits.

- Generation publication and cleanup respect active sessions.

- Export/import is portable, validated, and redacted by default.

- Targeted and required existing checks pass; native evidence and failures are recorded.

- Commands/help/examples and the support matrix reflect actual implementation.

- Public claims distinguish package integrity, generated configuration, native discovery, and task behavior.

### Completion report from the implementing agent

Return actual start/end commits; changed modules; mapping of every R01–R12 requirement to implementation/tests; exact implemented command syntax; schemas and sample reports; validated native versions and coverage; full test commands/results including skipped/failed cases; observed configuration writes/effects; backward-compatibility changes; authentication/history limitations; remaining gaps; and how to reproduce the demo.

Include one clean report, one violation, one unknown/partial observation, one stale receipt, and one sanitized support export. Identify the strongest real fault caught beyond the package lock. Do not report the specification itself as implemented functionality.

#### Working completion notes (uncommitted; start `89f63b2`)

**Narrowed release claim:** Strict external (no checkout config writes) advertised for Claude + OpenCode. Native *observed* discovery: Claude plugin/skill **presence** via `plugin list --json` + `plugin details` (2.1.295). Codex: **version only** (0.159.3); skill catalog unknown. Source digests and ambient `~/.claude/skills` remain unknown unless covered by a fresh receipt’s completeness.

**Commands:** `--env` / `--definition-root` / `--workspace` / `--strict-external`; `env init|use|bind|unuse|list|status|export|import|restore`; `preflight [--json|--agent|--policy|--contract|--receipt|--strict-external]`; `probe --agent claude|codex`; `config compare A B`; `support export ID --output PATH`.

**Modules:** `internal/environment`, `internal/paths/context.go`, `internal/sync/{preflight,service}`, `internal/cli/{env,preflight,probe,compare,support,parse,run,command}`, `internal/harness/{probe.go,claude/probe.go,codex/probe.go}`, `internal/staging/collision.go`, docs/schemas.

**Tests:** `go test ./...` green. Key: `TestCompiledCLIExternalEnvDoesNotWriteCheckout`, `TestRestoreFrozenFailsMissingInputWithoutLockChange`, `TestRelocatedDefinitionsShareSourceIdentity`, `TestPreflightFlagsAmbient*`, `TestPreflightReceiptStale*`, `TestPreflightPlannedWritesMatchStrictSyncRefusal`, capsule redact, contract eval.

**Gate demo (manual):** two relocated workspaces same `source_identity`; empty checkouts; Claude+Codex probes; support capsule; strict cursor → `WORKSPACE_WRITE_REQUIRED` exit 3.

**Strongest fault beyond package lock:** user/project skill name collision (`CONFIG_SHADOWED` / `AMBIENT_ARTIFACT`) that package-only lock checks miss.

**Still open (non-blocking under narrowed claim):** R09 dedicated auth-continuity fixtures beyond existing harness bridging; full settings-array merge IR.

**R10 note:** Launch holds shared `staging/.rebuild.lock`; sync/restore exclusive rebuild fails while a launch is active. Codex generation homes still rotate under their own leases.

### Research and naming boundaries

Credit existing external locks, temporary skills, mixed configuration compilation, native flags, and environment managers. Relevant comparisons include [Vercel skills](https://github.com/vercel-labs/skills), [Ruler](https://github.com/intellectronica/ruler), [Skilldex](https://arxiv.org/abs/2604.16911), [Rel AI Build control-plane preprint](https://arxiv.org/abs/2606.26924), and [Nix](https://nix.dev/manual/nix/2.28/command-ref/new-cli/nix3-flake.html).

Motivating reports include [Codex concurrent profiles](https://github.com/openai/codex/issues/30967), [skill restoration](https://github.com/vercel-labs/skills/issues/283), [installed-content drift](https://github.com/vercel-labs/skills/issues/1812), and [configuration composition](https://github.com/intellectronica/ruler/issues/319). Treat them as reported motivations, not current reproduced defects. Native interfaces must be checked against the actual version and official documentation.

