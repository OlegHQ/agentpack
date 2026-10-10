# ICSE evaluation and demonstration

Agentpack's candidate contribution is an external locked environment with an explainable effective configuration and evidence-graded CI contracts. Locking, external configuration flags, skill installation and environment managers are established techniques; this evaluation must test the added diagnosis value rather than claim those techniques as new.

## Reproduce the offline contribution checks

From the repository root:

```sh
go build -o ./agentpack ./cmd/agentpack
python3 evaluation/icse/run.py --binary ./agentpack
go test ./internal/harness ./internal/harness/claude ./internal/harness/codex
```

The Python runner creates disposable external definitions and two relocated checkout directories. It invokes the actual built CLI, not a native agent. Its independent oracles assert:

- equal portable source identities across relocated workspaces;
- an explicit no-workspace-configuration-write contract succeeds for Claude planning;
- observed skill presence without a receipt stays unknown and returns exit 4;
- a strict Cursor workspace overlay requirement fails and returns exit 3;
- lock/definition, checkout, native-home and agentpack state contents, permissions, directories and symlink targets stay unchanged during preflight;
- authored inherited user settings, a mismatched named skill, and an incomplete receipt are rejected while the lock hash stays unchanged.

The suite prints its actual outcomes. This is deterministic regression evidence on synthetic fixtures, not a developer study, native discovery experiment or competition benchmark. The temporary workspaces disappear after the run. The initial definition is intentionally empty to keep the offline control independent of network and package registries.

Probe tests use synthetic executable fixtures for invalid outputs, nonzero native exits, empty versions, bounded capture and cancellation. They prove adapter error handling, not native catalog discovery. Native versions outside recorded parser coverage return unknown. Default probes request no model inference, but agentpack does not monitor native network activity or every runtime write; receipts say those effects are unmeasured. Unix cancellation terminates the owned process group. Windows cancellation currently terminates the direct child; descendant-producing Windows probes are not validated.

## Native evidence gate

The current Claude parser covers the recorded 2.1.295 and validated 2.1.296 inventory shape. Plugin presence and names discovered in plugin details can be observed; source bytes/digests, ambient user skills and model use remain unknown. A malformed or truncated inventory cannot become an empty successful inventory. Skill inventory coverage remains partial, so it cannot prove complete skill absence.

The Codex 0.159.2 adapter uses `debug prompt-input` with a disposable HOME/CODEX_HOME, cloud skills disabled, file credential store, and a regular-file copy of staged skills. It does not copy raw user config, credentials, history or hooks. It observes positive names in native developer skill instructions; this is a **controlled projection**, not proof of the full interactive launch configuration. Symlinked skill inputs are refused. Codex 0.159.3 remains version-only support. Neither adapter can prove model use or complete ambient-skill absence.

Controlled real-native positive/removed-sentinel tests passed on macOS arm64 with Claude 2.1.296 and Codex 0.159.2. Run them explicitly:

```sh
AGENTPACK_NATIVE_PROBES=1 go test -v ./internal/harness/claude ./internal/harness/codex -run '^TestNative.*Sentinel$'
AGENTPACK_NATIVE_PROBES=1 go test -v ./internal/cli -run '^TestNativeRestoredPackConformance$'
```

These tests create benign disposable inputs and isolate native homes. They request no model inference or production MCP/hook execution. They check that the known-positive sentinel appears, that its observation disappears after the authored fixture is removed, and that the fixture workspace stays unchanged. Missing observations remain unknown; this is not a general negative-discovery guarantee. A second integration test freezes an independently authored, content-hashed local skill package, restores it through the actual materialization pipeline for both targets, and confirms the native positive sentinel in both generated configurations. It exercises the full CLI probe, private receipt save and latest-receipt validation. An independently defined empty mode removes the locked skill: neither adapter emits a positive removed-sentinel observation, and a required observed contract stays unknown under partial coverage. It checks equal source identity, unchanged lock bytes and no checkout files. This passed on the same native versions. Native network/global effects are unmeasured. Native writes observed in the spike include Claude config/backups and Codex installation/migration/temp/system-skill files inside diagnostic homes. Sanitized run metadata is in `evaluation/icse/native-evidence-macos-arm64.json`; bounded stdout/stderr and process status are retained as 0600 JSON evidence under 0700 managed receipt directories, with paths on the local receipt. Support export omits these diagnostic references and raw files.

Before calling the artifact submission-ready, extend the restored-pack controlled sentinel results to held-out ambient/precedence faults and independently grounded source/override expectations, exact executable/version identities, effects and parser fixtures. Use disposable owned diagnostic roots. Do not run production hooks, MCP servers or model requests to obtain deterministic configuration evidence. OpenCode `debug config` and `debug skill` are candidates, not implemented validated support: its bootstrap/config loading may install dependencies, write config, run plugins or access remote settings. Full resolved config can contain secrets.

## Evaluation protocol

Pin the agentpack revision, native executable versions, platform and every baseline revision. Separate cold/warm runs, static reports, recorded native receipts and live observations. Write expected outcomes independently of the report, and retain clean controls and held-out fault instances.

| Experiment | Ground truth and outcome | Comparison |
| --- | --- | --- |
| External restore in two relocated checkouts | Same locked source identity; expected rendered files; no checkout config writes | Current agentpack; best-practice native external flags with a small wrapper |
| Local artifact or server collision | Authored duplicate sources and independently checked winner; diagnosis/recovery steps | Package-only checks; native diagnostics wrapper |
| Scoped rule or unsupported hook | Declared target support; visible loss/omission; no enforcement claim | Current agentpack renderer checks; Ruler on supported shared tasks |
| Required native observation unavailable | Unknown must fail required observed contract, not masquerade as absence | Native diagnostic wrapper |
| Stale receipt | Change policy, mode, generation, executable or readable ambient inputs independently; receipt rejected | Plain package hashes |
| Skill restoration | Equivalent benign skill inputs and target; restore success and drift detection | Vercel skills on overlapping tasks |
| Concurrent environments | Two projects/modes; active launch plus failed restore; previous generation remains usable | Current agentpack where equivalent |

Record detection precision/recall by fault class, clean false alarms, unknown/coverage rate, restore success, repository writes, runtime overhead and recovery steps. Unsupported baseline tasks are N/A, not failures. Do not infer productivity improvements from command counts alone. A formative, counterbalanced walkthrough with unfamiliar developers is a separate optional study; report sample, recruitment and failures honestly.

Relevant comparison sources: [Vercel skills](https://github.com/vercel-labs/skills), [Ruler](https://github.com/intellectronica/ruler), [Skilldex preprint](https://arxiv.org/abs/2604.16911), [Nix](https://nix.dev/manual/nix/2.28/command-ref/new-cli/nix3-flake.html). Credit established mechanisms. No comparative result is supplied by this guide.

## A 4:30 demo storyboard

| Time | Action | Evidence on screen |
| --- | --- | --- |
| 0:00–0:35 | Explain a team switching CLI agents while keeping project configuration untouched | Project tree and external definition/lock locations |
| 0:35–1:15 | Select one external environment and inspect generated configuration | Source ID, target, artifact origins, inheritance and planned writes; distinguish generated from observed |
| 1:15–2:00 | Restore in another checkout and run the offline fixture suite | Equal source IDs and full file-tree unchanged checks, not just Git diff |
| 2:00–2:50 | Show a controlled ambient/precedence mismatch | Native observation receipt only if live validated; otherwise explicitly recorded or unknown; package-only baseline misses authored fault |
| 2:50–3:35 | Apply one narrow recovery and check again | Concrete remedy, new generation/receipt; strict contract failure followed by supported pass |
| 3:35–4:10 | Demonstrate unsupported overlay and missing native evidence | Exit 3 versus exit 4 and actionable findings; no silent checkout writes |
| 4:10–4:30 | Show public artifact, tested-version matrix and limits | Config reproducibility is not deterministic LLM behavior; two-adapter native gate status |

Only film commands that exist in the released artifact. The deterministic runner is runnable now; native fault/recovery scenes remain contingent on real validation. Do not replace an unavailable live observation with an unlabeled mock.

The [ICSE 2027 Tool Demonstration call](https://conf.researchr.org/track/icse-2027/icse-2027-demonstrations) requires a usable public tool and a 3–5 minute video. Acceptance is not guaranteed by a completed checklist. A narrow truthful static contribution is preferable to unsupported native-observation claims.
