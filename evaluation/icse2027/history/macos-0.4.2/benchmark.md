# Agentpack 0.4.2 measured artifact evaluation (macOS record, 2026-10-11)

This note describes the first run only. Its runner asserted exit status, not named findings; `audit_saved.py` checks the saved rows semantically and `faults.py` reruns the seven classes with semantic oracles. "Independent" in the first version of this note meant that expected outcomes were specified by the author without reading the tool's report; it did not mean a second person or a replication. See [README.md](README.md) for the current artifact.

Frozen run date: 2026-10-11. This is a single-machine synthetic artifact evaluation, not a developer study or an acceptance guarantee. All commands invoked real released/built CLIs in disposable isolated HOME/USERPROFILE/XDG state and workspace directories. No production credential/configuration/hook/MCP inputs were copied; native metadata probes requested no model inference. Network/global effects of native executables were not monitored and remain unmeasured.

## Pinned artifacts

- Agentpack released macOS arm64 0.4.2 executable SHA-256 `7f08b4b33ed641bc6163536be7b1ba152477befa49746fdb5c983603a53f33b3`; [public release](https://github.com/OlegHQ/agentpack/releases/tag/v0.4.2).
- Vercel skills 1.7.2, commit `13e4063a1cf913f5606d57d42ab83a86f5001e04`; [pinned source](https://github.com/vercel-labs/skills/tree/13e4063a1cf913f5606d57d42ab83a86f5001e04).
- Ruler 0.3.44, commit `0fa2caeef4efaef4c5f8ffbedee8feca4b0b00e2`; [pinned source](https://github.com/intellectronica/ruler/tree/0fa2caeef4efaef4c5f8ffbedee8feca4b0b00e2).
- Native wrapper: `codex-cli 0.159.2`, macOS-27.0.1-arm64-arm-64bit-Mach-O, Node v24.19.0.

## Descriptive timings and file effects

Seven process invocations per task, sharing the prepared definition/home and disk caches. Most task loops used new checkout directories; native discovery reused one checkout and diagnostic home, and global skill installation reused its user-level target (corrected 2026-10-12; the 2026-10-11 text said every repetition used a new checkout). Build, dependency installation, package-add, and preflight materialization preparation are excluded. The first native metadata invocation is included; no formal cold/warm stratification or confidence interval is claimed. Median and inclusive quartiles describe this run. Timing tasks deliver different artifact scopes and are **not a speedup ranking**. Full checkout snapshots include files, directories, modes, and symlink targets.

| Actual CLI task | Success | Median ms | Q1–Q3 ms | Checkout changed |
| --- | ---: | ---: | ---: | ---: |
| agentpack: external_restore | 7/7 | 96.1 | 92.2–98.5 | 0/7 |
| skills: skill_install | 7/7 | 148.0 | 144.4–154.1 | 7/7 |
| ruler: external_rule_apply | 7/7 | 249.5 | 235.5–256.8 | 7/7 |
| skills: global_skill_install | 7/7 | 143.0 | 141.9–147.2 | 0/7 |
| skills: temporary_prompt_use | 7/7 | 133.4 | 132.3–134.5 | 0/7 |
| agentpack: pure_preflight | 7/7 | 30.6 | 30.4–30.6 | 0/7 |
| native_wrapper: positive_skill_discovery | 7/7 | 271.2 | 268.2–274.6 | 0/7 |

Skill installation or rule application bytes were checked against author-specified benign inputs. Agentpack restore checks the exact selected definition/default-mode Claude bundle path; Skills checks its actual installed SKILL.md; Ruler checks emitted CLAUDE.md; Skills temporary use checks emitted prompt content. Native wrapper checks positive sentinel presence only. Agentpack source identity is equal across seven relocated workspaces, and all pure preflights leave checkout snapshots unchanged.

Skills project installation and Ruler rule application write native configuration by design; these are successful supported operations, not failures. Skills global installation and temporary `skills use` also avoid checkout writes. Therefore repository-free use is not unique to Agentpack. Ruler also offers custom paths and a dry-run preview. Agentpack's candidate added value is a selected external locked environment combined with evidence-scoped, read-only contracts and receipt freshness checks. Skills already maintains skill lock metadata; generic skill installation, locking, multi-agent translation, and external files are not claimed as new.

## Author-specified fault gates

The package-hash-only baseline checks the unchanged pack.lock hash. It misses all seven non-package faults by construction, establishing why package identity alone is insufficient. It is not a claim that Skills or Ruler promise or fail these predicates: their unsupported receipt/CI-contract fault tasks are N/A. The hand-written native Codex wrapper positively discovers a benign skill but does not implement contract policy, receipt identity/freshness, or raw-evidence validation. Native observations cover a controlled projection, not full interactive configuration or complete absence.

| Fault | Exit | First three finding codes in the saved row |
| --- | ---: | --- |
| missing_native_receipt | 4 | STRICT_EXTERNAL, BINARY_FOUND, OBSERVATION_UNAVAILABLE |
| unsupported_workspace_overlay | 3 | WORKSPACE_WRITE_REQUIRED, BINARY_FOUND, WORKSPACE_WRITE_REQUIRED |
| inherited_user_config | 3 | STRICT_EXTERNAL, BINARY_FOUND, STAGING_INPUTS_STALE |
| named_artifact_mismatch | 3 | INHERITANCE_BOUNDARY, BINARY_FOUND, CONTRACT_MISMATCH |
| malformed_complete_receipt | 5 | BINARY_FOUND, RECEIPT_STALE, OBSERVATION_UNAVAILABLE |
| stale_receipt_workspace_edit | 4 | STAGING_INPUTS_STALE, RECEIPT_STALE, OBSERVATION_UNAVAILABLE |
| missing_diagnostic_evidence | 5 | BINARY_FOUND, OBSERVATION_UNAVAILABLE, OBSERVATION_UNAVAILABLE |

All seven trials fail closed with unchanged lock bytes. Exit 3 denotes violation, 4 unknown required observation, 5 operational/evidence error. Seven of seven is a bounded fixture result, not population recall. Four clean controls passed with exit 0: supported no-checkout-write contract, valid native receipt, restored original workspace inputs, and the exact observed contract before deleting its evidence. The same observed contract is installed before probe and used for both clean and evidence-deletion trials, preventing contract-digest confounding. The native name comes from the fixture's SKILL.md frontmatter (`held-out-review`); generated package path name (`skill`) differs.

## Bundle controls

An empty definition exports/imports successfully with exact lock bytes. An archive containing `../escaped` is refused without destination or escape file creation. Export of a local filesystem payload is refused without bundle publication; this is an honest limitation, not offline local-cache portability. These are separate bundle correctness checks, not competitor diagnosis scores.

## Reproduction and public data

The portable `run.py` accepts `--agentpack`, `--baseline-root` (or individual `--skills`/`--ruler`), `--codex`, `--fixtures`, `--root`, and `--output`. No production repository is required: all benign fixture inputs and contracts are included/generated. Exact pinned setup is in [README.md](README.md). The supplied npm locks reproduce dependency resolution; dependency build/setup is excluded from timings and Skills license-notice generation is not fully hermetic. Source SHAs and the controlled Codex version are checked before the full experiment. `--control-only` runs a clean restore/preflight check without competitor/native dependencies. Explicit-path clean control, `--help`, and Python syntax validation passed after portable export.

`comparative-results.json` contains every measured command's normalized arguments, status, time, filesystem effect, findings/contract outcomes, and output byte counts/hashes. Raw native output is omitted from public artifacts; local raw captures are private 0600 and contain no exported evidence files. Native child-global writes/network remain unmeasured. Repetitions are process measurements sharing caches; they do not establish developer time savings, model determinism, comprehensive adapter coverage, concurrency safety, or real-world fault prevalence.
