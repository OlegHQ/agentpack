# Agentpack ICSE 2027 evaluation artifact

The four experiments of the paper's evaluation section, with the scripts that produced the results. Nothing here requests model inference. All results were recorded on 2026-10-12 on Linux 6.8 aarch64 with the released Agentpack 0.4.4 `linux_arm64` binary.

| Experiment | Script | Results | Needs |
|---|---|---|---|
| Same-named skills in public repositories | `prevalence.py` | `prevalence-results.json` | `gh` (signed in), `git` |
| What three agents do with a same-named skill | `collision.py` | `collision-results.json` | Claude Code 2.1.296, Codex 0.159.3, OpenCode 1.17.11 |
| Receipts over time (16 states) | `scenario.py` | `scenario-results.json` | Claude Code 2.1.296 |
| Seven fault classes | `faults.py` | `fault-results.json` | nothing for five classes; Claude Code 2.1.296 for the last two |

## Quickstart: released binary and Python 3 only

```sh
curl -LO https://github.com/OlegHQ/agentpack/releases/download/v0.4.4/agentpack_0.4.4_linux_arm64.tar.gz
curl -LO https://github.com/OlegHQ/agentpack/releases/download/v0.4.4/checksums.txt
sha256sum -c checksums.txt --ignore-missing
mkdir bin && tar -xzf agentpack_0.4.4_linux_arm64.tar.gz -C bin

# five offline fault classes and three ready controls, about one second, no agent needed
python3 faults.py --agentpack bin/agentpack --output /tmp/fault-results.json
```

Archives for `linux_amd64`, `darwin_arm64`, `darwin_amd64` and `windows_amd64` are in the same release. Every script creates its homes, definitions and checkouts under a temporary directory and removes them. Each exits 0 only when every check holds.

## With the agents installed

The scripts refuse other agent versions, because the probes are validated for these versions only. Agents run in an empty temporary home: no account, credential, hook or user setting is read.

```sh
python3 faults.py    --agentpack bin/agentpack --claude "$(command -v claude)"
python3 scenario.py  --agentpack bin/agentpack      # clones OlegHQ/agentpack@v0.4.4, fetches anthropics/skills@dbd4588
python3 collision.py --agentpack bin/agentpack      # about 25 minutes; --opencode-repeats 10 for a quick pass
python3 prevalence.py                               # about 8 minutes; GitHub code search is rate limited
```

## What each experiment does

**`prevalence.py`** takes the 19 skills of `anthropics/skills` at commit `dbd4588` and asks GitHub code search for `<root>/<name>/SKILL.md` in other repositories, for `.claude/skills`, `.agents/skills` and `.codex/skills`. Each hit's blob hash is compared with every version of the upstream file in the upstream history. Result: GitHub reported 10,014 matches; the first 100 per query give 3,816 files, of which 1,193 equal the pinned upstream file, 267 equal an older upstream version and 2,356 differ from every upstream version. A differing file can be an edited copy or an unrelated skill with the same name; either way two skills share one name.

**`collision.py`** pins `webapp-testing`, restores it, and puts a project skill with the same name in `.claude/skills`, `.agents/skills`, `.codex/skills` or `.opencode/skills` (plus a control without one). For each location and agent it first asks the agent directly, without Agentpack, what a session in the checkout lists. That is the ground truth. It then runs Agentpack `probe` and `preflight` with two requirements: the planned winner (generated evidence, from Agentpack's scan of the checkout) and the observed winner (from the agent's session catalog). The expected observed result is derived from the ground truth by one rule, stated in the script: satisfied exactly when the staged copy alone carries the name. See the table below.

**`scenario.py`** follows one public checkout through 16 states. Expected outcomes were written from the documentation before each state was first run. With release 0.4.4 all 14 stated expectations hold. Every state is also judged by a lock-hash check and by a short script that queries Claude Code's plugin listing directly.

**`faults.py`** runs seven fault classes and five ready controls. A case passes only if exit status, named finding and requirement result are as specified and neither the lock nor the checkout changed. 7 of 7 and 5 of 5 pass.

## Result of the three-agent experiment

| Project copy in | Agent | What the agent's session lists (runs) | Planned winner | Observed winner | Preflight exit |
|---|---|---|---|---|---|
| none | claude | agentpack-bundle:webapp-testing (5) | satisfied | satisfied | 0 |
| none | codex | staged (5) | satisfied | satisfied | 0 |
| none | opencode | staged (100) | no probe | no probe |  |
| .claude/skills | claude | webapp-testing (5) | violated | violated | 3 |
| .claude/skills | codex | staged (5) | violated | satisfied | 3 |
| .claude/skills | opencode | .claude/skills (1); staged (99) | no probe | no probe |  |
| .agents/skills | claude | agentpack-bundle:webapp-testing (5) | violated | satisfied | 3 |
| .agents/skills | codex | .agents/skills, staged (5) | violated | violated | 3 |
| .agents/skills | opencode | staged (100) | no probe | no probe |  |
| .codex/skills | claude | agentpack-bundle:webapp-testing (5) | satisfied | satisfied | 0 |
| .codex/skills | codex | .codex/skills, staged (5) | violated | violated | 3 |
| .codex/skills | opencode | staged (100) | no probe | no probe |  |
| .opencode/skills | claude | agentpack-bundle:webapp-testing (5) | satisfied | satisfied | 0 |
| .opencode/skills | codex | staged (5) | satisfied | satisfied | 0 |
| .opencode/skills | opencode | .opencode/skills (1); staged (99) | no probe | no probe |  |

`staged` is the copy Agentpack materialized from the lock; `agentpack-bundle:webapp-testing` is that copy inside the Claude plugin. The observed result matches the ground truth in 10 of 10 cells with a probe. Two cells show why both grades are stated. With the project copy in `.claude/skills`, the planned check also flags Codex, which does not read that directory; the observation shows Codex unaffected. With the project copy in `.agents/skills`, Agentpack's staging lets it replace the pinned bytes in the Claude bundle while the session still lists one entry with the expected name; only the planned check reports it. OpenCode listed the project copy in 1 of 100 runs for two locations; shorter pilot runs during development showed the same instability at other rates (1 of 8, 2 of 20, 1 of 50).

## History

Earlier results are kept under `history/`, including runs that went wrong.

- `scenario-results-0.4.2-run1.json`, `-run2.json`, `-run3.json`: the scenario against release 0.4.2. Twelve of fourteen expectations held; in the other two a never-probed checkout returned error 5 where unknown 4 was expected. Release 0.4.3 changed that outcome to unknown, as the documentation describes. Run 2 also contains a harness ordering mistake that broke one recovery.
- `scenario-results-0.4.3-false-alarms.json`: release 0.4.3 reported a checkout's own `.agents/skills` as name clashes and rejected ready states. The scenario caught it; 0.4.4 fixes it and adds a regression test to the tool.
- `macos-0.4.2/`: the first evaluation, on macOS arm64 with Codex 0.159.2, Vercel Skills 1.7.2 and Ruler 0.3.44. Its runner asserted exit status only; `audit_saved.py` checks the saved rows for the same findings that `faults.py` now asserts. The timing rows compare different tasks and are not a ranking.

## Limits

- One author wrote the tool, the fixtures and the expectations, with AI assistance (OpenAI Codex for release 0.4.2 and the first runner; Anthropic Claude for the session observation in 0.4.4 and the scripts here). This is not an independent replication.
- The three-agent experiment uses one skill, one version of each agent and one platform. Agents change their discovery rules between releases.
- Probes and ground-truth queries run with an empty user home. User-level skills are outside the observation, and absence cannot be shown.
- Search counts are GitHub's on the day of the run and are capped at 100 files per query.
- OpenCode has no Agentpack probe. Its catalog shows one entry per name, so a clash resolved differently from run to run is invisible in a single observation.
