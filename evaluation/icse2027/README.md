# Agentpack ICSE 2027 evaluation artifact

The four experiments of the paper's evaluation section, with the scripts that produced the results. Nothing here requests model inference. All results were recorded on 2026-10-12 on Linux 6.8 aarch64 with the released Agentpack 0.4.5 `linux_arm64` binary.

| Experiment | Script | Results | Needs |
|---|---|---|---|
| Same-named skills in public repositories | `prevalence.py` | `prevalence-results.json` | `gh` (signed in), `git` |
| What three agents do with a same-named skill | `collision.py` | `collision-results.json` | Claude Code 2.1.296, Codex 0.159.3, OpenCode 1.17.11 |
| Receipts over time (16 states) | `scenario.py` | `scenario-results.json` | Claude Code 2.1.296 |
| Seven fault classes | `faults.py` | `fault-results.json` | nothing for five classes; Claude Code 2.1.296 for the last two |

## Quickstart: released binary and Python 3 only

```sh
curl -LO https://github.com/OlegHQ/agentpack/releases/download/v0.4.5/agentpack_0.4.5_linux_arm64.tar.gz
curl -LO https://github.com/OlegHQ/agentpack/releases/download/v0.4.5/checksums.txt
sha256sum -c checksums.txt --ignore-missing
mkdir bin && tar -xzf agentpack_0.4.5_linux_arm64.tar.gz -C bin

# five offline fault classes and three ready controls, about one second, no agent needed
python3 faults.py --agentpack bin/agentpack --output /tmp/fault-results.json
```

Archives for `linux_amd64`, `darwin_arm64`, `darwin_amd64` and `windows_amd64` are in the same release. Every script creates its homes, definitions and checkouts under a temporary directory and removes them. Each exits 0 only when every check holds.

## With the agents installed

The scripts refuse other agent versions, because the probes are validated for these versions only. Agents run in an empty temporary home: no account, credential, hook or user setting is read.

```sh
python3 faults.py    --agentpack bin/agentpack --claude "$(command -v claude)"
python3 scenario.py  --agentpack bin/agentpack      # clones OlegHQ/agentpack@v0.4.5, fetches anthropics/skills@dbd4588
python3 collision.py --agentpack bin/agentpack      # about 25 minutes; --opencode-repeats 10 for a quick pass
python3 prevalence.py                               # about 8 minutes; GitHub code search is rate limited
```

## What each experiment does

**`prevalence.py`** takes the 19 skills of `anthropics/skills` at commit `dbd4588` and asks GitHub code search for `<root>/<name>/SKILL.md` in other repositories, for `.claude/skills`, `.agents/skills` and `.codex/skills`. Each hit's blob hash is compared with every version of the upstream file in the upstream history (renames followed). Result: the first 100 hits of each of the 57 queries (35 had more) give 3,816 files in 1,731 repositories; 1,193 equal the pinned upstream file, 304 equal an older upstream version and 2,319 differ from every upstream version. A differing file can be an edited copy or an unrelated skill with the same name; either way two skills share one name. Repository and blob hash of every file are in the results, so the split can be re-audited. There is no denominator: this shows the situation exists at scale, not how frequent it is.

**`collision.py`** has two parts. Part A does not use Agentpack: it fetches `webapp-testing` with git, installs it the way each agent documents (a plugin for Claude Code, `CODEX_HOME/skills` for Codex, the configuration directory for OpenCode), places a project skill with the same name in one project root at a time, and asks the agent what a session in the checkout lists. Codex and OpenCode print descriptions, so the two copies are told apart by their text; Claude Code prints names, where a plugin skill carries its plugin prefix. Part B restores the same pin with Agentpack and runs `probe` and `preflight` with two requirements: the planned winner (generated evidence, from Agentpack's scan of the checkout) and the observed winner (from Agentpack's probe). The expected observed result follows from part A by one rule, stated in the script: satisfied exactly when every run lists the pinned copy and nothing else under the name. Part B also takes a receipt in the clean checkout first and checks that adding the project copy retires it.

**`scenario.py`** follows one public checkout through 16 states. Expected outcomes were written from the documentation before each state was first run. With release 0.4.5 all 14 stated expectations hold. Every state is also judged by a lock-hash check and by a script of about 25 lines that starts a Claude Code session on every check and reads its catalog; it keeps no receipt.

**`faults.py`** runs seven fault classes and five ready controls. A case passes only if exit status, named finding and requirement result are as specified and neither the lock nor the checkout changed. 7 of 7 and 5 of 5 pass.

## Result of the three-agent experiment

| Project copy in | Agent | Lists, installed natively (runs) | Planned winner | Observed winner | Preflight exit | Earlier receipt retired |
|---|---|---|---|---|---|---|
| none | claude | pinned (5) | satisfied | satisfied | 0 |  |
| none | codex | pinned (5) | satisfied | satisfied | 0 |  |
| none | opencode | pinned (100) | satisfied | no probe | 4 |  |
| .claude/skills | claude | pinned and project (5) | violated | violated | 3 | True |
| .claude/skills | codex | pinned (5) | violated | satisfied | 3 | True |
| .claude/skills | opencode | pinned (96), project (4) | violated | no probe | 4 |  |
| .agents/skills | claude | pinned (5) | violated | satisfied | 3 | True |
| .agents/skills | codex | pinned and project (5) | violated | violated | 3 | True |
| .agents/skills | opencode | pinned (100) | violated | no probe | 4 |  |
| .codex/skills | claude | pinned (5) | satisfied | satisfied | 0 | True |
| .codex/skills | codex | pinned and project (5) | violated | violated | 3 | True |
| .codex/skills | opencode | pinned (100) | satisfied | no probe | 4 |  |
| .opencode/skills | claude | pinned (5) | satisfied | satisfied | 0 | True |
| .opencode/skills | codex | pinned (5) | satisfied | satisfied | 0 | True |
| .opencode/skills | opencode | project (4), pinned (96) | violated | no probe | 4 |  |

The observed result matches part A in 10 of 10 probed cells, and the earlier receipt is retired in 8 of 8. Two cells show why both grades are stated. With the project copy in `.claude/skills`, the planned check also flags Codex, which does not read that root; the observation shows Codex unaffected. With the project copy in `.agents/skills`, Claude Code itself ignores the directory, but Agentpack shares `.agents` with every agent and its staging lets the project skill replace the pinned one in the Claude plugin (`staged_skill_is_the_project_copy` in the results); the session still lists one entry with the expected name, so only the planned check reports it. OpenCode has no probe. It listed the project copy in 4 of 100 runs for `.claude/skills` and for `.opencode/skills`; earlier runs showed the same instability at other rates and are kept under `history/`.

## History

Earlier results are kept under `history/`, including runs that went wrong.

- `scenario-results-0.4.2-run1.json`, `-run2.json`, `-run3.json`: the scenario against release 0.4.2. Twelve of fourteen expectations held; in the other two a never-probed checkout returned error 5 where unknown 4 was expected. Release 0.4.3 changed that outcome to unknown, as the documentation describes. Run 2 also contains a harness ordering mistake that broke one recovery.
- `scenario-results-0.4.3-false-alarms.json`: release 0.4.3 reported a checkout's own `.agents/skills` as name clashes and rejected ready states. The scenario caught it; 0.4.4 fixes it and adds a regression test to the tool.
- `scenario-results-0.4.4-plugin-listing-baseline.json`: the comparison script still read Claude Code's plugin listing, which cannot see the checkout. It now reads the session catalog, the strongest stateless check we know.
- `collision-results-0.4.4-shared-staging.json`, `collision-results-0.4.5-first.json`: earlier runs of the three-agent experiment. In the first, the direct reading reused Agentpack's staged directory, so it was not independent of the tool; part A now installs the skill natively. Release 0.4.4 also did not retire a receipt when a skill appeared in `.codex/skills`; 0.4.5 does.
- `macos-0.4.2/`: the first evaluation, on macOS arm64 with Codex 0.159.2, Vercel Skills 1.7.2 and Ruler 0.3.44. Its runner asserted exit status only; `audit_saved.py` checks the saved rows for the same findings that `faults.py` now asserts. The timing rows compare different tasks and are not a ranking.

## Limits

- One author wrote the tool, the fixtures and the expectations, with AI assistance (OpenAI Codex for release 0.4.2 and the first runner; Anthropic Claude for the session observation in 0.4.5 and the scripts here). This is not an independent replication.
- The three-agent experiment uses one skill, one version of each agent and one platform. Agents change their discovery rules between releases.
- Probes and ground-truth queries run with an empty user home. User-level skills are outside the observation, and absence cannot be shown.
- Search counts are GitHub's on the day of the run and are capped at 100 files per query.
- OpenCode has no Agentpack probe. Its catalog shows one entry per name, so a clash resolved differently from run to run is invisible in a single observation.
