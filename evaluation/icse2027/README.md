# Agentpack ICSE 2027 evaluation artifact

Scripts and recorded results for every experiment in the paper's evaluation section. All results were recorded on 2026-10-12 with the released Agentpack 0.4.7 binary, on Linux 6.8 aarch64 unless a CI runner is named.

| Experiment | Script | Results | Calls a model | Needs |
|---|---|---|---|---|
| One pinned package on six agents | `delivery.py` | `delivery-results.json` | yes | the six agents, signed in |
| Same-named skills in public repositories | `prevalence.py` | `prevalence-results.json` | no | `gh` (signed in), `git` |
| What three agents list for a same-named skill | `collision.py` | `collision-results.json` | no | Claude Code 2.1.296, Codex 0.159.3, OpenCode 1.17.11 |
| Which copy the agent follows | `behaviour.py` | `behaviour-results.json` | yes | Claude Code and Codex, signed in |
| Thirty real repositories | `realrepos.py` | `realrepos-results.json` | no | Claude Code 2.1.296, Codex 0.159.3 |
| CI gate on three operating systems | `ci_gate.py` | `ci-results/` | no | nothing: it runs in GitHub Actions |
| Receipts over 16 states | `scenario.py` | `scenario-results.json` | no | Claude Code 2.1.296 |
| Eight fault classes | `faults.py` | `fault-results.json` | no | nothing for five classes; Claude Code for three |

## Quickstart: released binary and Python 3 only

```sh
curl -LO https://github.com/OlegHQ/agentpack/releases/download/v0.4.7/agentpack_0.4.7_linux_arm64.tar.gz
curl -LO https://github.com/OlegHQ/agentpack/releases/download/v0.4.7/checksums.txt
sha256sum -c checksums.txt --ignore-missing
mkdir bin && tar -xzf agentpack_0.4.7_linux_arm64.tar.gz -C bin

# five offline fault classes and three ready controls, about one second, no agent needed
python3 faults.py --agentpack bin/agentpack --output /tmp/fault-results.json
```

Archives for `linux_amd64`, `darwin_arm64`, `darwin_amd64` and `windows_amd64` are in the same release. The CI experiment needs nothing local at all: see [the workflow](../../.github/workflows/agent-config-gate.yml) and [its run](https://github.com/OlegHQ/agentpack/actions/runs/38262489514).

With the agents installed (the scripts refuse other agent versions, because the probes are validated for these versions only):

```sh
python3 collision.py --agentpack bin/agentpack      # about 25 minutes; --repeats 5 for a quick pass
python3 realrepos.py --agentpack bin/agentpack      # clones 30 public repositories, skill directories only
python3 scenario.py  --agentpack bin/agentpack
python3 faults.py    --agentpack bin/agentpack --claude "$(command -v claude)"
python3 prevalence.py                               # about 8 minutes; GitHub code search is rate limited
python3 delivery.py  --agentpack bin/agentpack --codex-unsandboxed   # real model calls on your accounts
python3 behaviour.py --codex-unsandboxed            # real model calls on your Claude Code and Codex accounts
```

Scripts that call no model run every agent in an empty temporary home: no account, credential, hook or user setting is read. `delivery.py` and `behaviour.py` are the exception by design and say so in their headers.

## One pinned package on six agents (`delivery.py`)

A package with a skill, a slash command, a subagent, a rule and a hook is pinned and restored once, and each agent is launched through Agentpack in non-interactive mode, three times per artifact. Each artifact tells the agent to answer with its own token; the hook appends a line to a file when a prompt is submitted.

| Agent | Version | Skill | Command | Subagent | Rule | Hook fired |
|---|---|---|---|---|---|---|
| Claude Code | 2.1.296 (Claude Code) | 3/3 | 3/3 | 3/3 | 3/3 | 12/12 |
| Codex | codex-cli 0.159.3 | 3/3 | 3/3 | 3/3 | 3/3 | 12/12 |
| OpenCode | 1.17.11 | 3/3 | 3/3 | 3/3 | 3/3 | 12/12 |
| Cursor | 2026.08.25-3e8eec8 | 3/3 | 3/3 | 3/3 | 3/3 | 0/12 |
| Grok | grok 1.0.44 (5b807183dd79) [stable] | 3/3 | 3/3 | 0/3 | 3/3 | 12/12 |
| Antigravity | 1.3.3 | 3/3 | 3/3 | 3/3 | 3/3 | 0/12 |

- 23 of 24 artifact cells work in 3 of 3 runs. Grok's subagent is staged and Grok names it, but its non-interactive mode did not return the subagent's answer.
- Codex runs a hook only after a person has reviewed it; these runs pass Codex's own flag that skips the review. Without it the staged hook does not fire.
- Cursor did not fire the prompt hook in non-interactive mode. Agentpack does not stage hooks for Antigravity.
- OpenCode's slash command is invoked with `opencode run --command`; a first run that sent `/delivery-command` as a prompt is kept in `history/`.
- The first runs of this experiment exposed a defect in Agentpack: the hooks plugin it generated for OpenCode failed on OpenCode 1.17.11. Fixed in 0.4.7.
- The lockfile is unchanged and no regular file is added to the checkout (`files_left_in_checkout` lists only the fixture's own `README.md`). Cursor and Antigravity get one link in the checkout that points to staging.

## Same-named skills

**`prevalence.py`.** For the 19 skills of `anthropics/skills` at commit `dbd4588`: the first 100 hits of each of 57 code-search queries (35 had more) give 3,816 files in 1,731 repositories; 1,193 equal the pinned upstream file, 304 equal an older upstream version and 2,319 differ from every upstream version (renames followed). Repository and blob hash of every file are in the results. There is no denominator: this shows the situation exists at scale, not how frequent it is.

**`collision.py`.** Part A does not use Agentpack: three skills are fetched with git and installed the way each agent documents, project skills with the same names are placed in one project root at a time in a checkout no Agentpack command touches, and each agent is asked 100 times what a session lists. Codex and OpenCode print descriptions, so copies are told apart by their text; Claude Code prints names, where a plugin skill carries its plugin prefix. Part B restores the same pins with Agentpack and runs the planned and the observed check; the expected observed result follows from part A by one rule in the script. It also takes a receipt in the clean checkout first and checks that the project copy retires it.

| Project copy in | Agent | Skill | Lists, installed natively (of 100 runs) | Planned | Observed | Earlier receipt retired |
|---|---|---|---|---|---|---|
| none | claude | webapp-testing | pinned 100 | satisfied | satisfied |  |
| none | claude | mcp-builder | pinned 100 | satisfied | satisfied |  |
| none | claude | internal-comms | pinned 100 | satisfied | satisfied |  |
| none | codex | webapp-testing | pinned 100 | satisfied | satisfied |  |
| none | codex | mcp-builder | pinned 100 | satisfied | satisfied |  |
| none | codex | internal-comms | pinned 100 | satisfied | satisfied |  |
| none | opencode | webapp-testing | pinned 100 | satisfied | satisfied |  |
| none | opencode | mcp-builder | pinned 100 | satisfied | satisfied |  |
| none | opencode | internal-comms | pinned 100 | satisfied | satisfied |  |
| .claude/skills | claude | webapp-testing | pinned and project 100 | violated | violated | True |
| .claude/skills | claude | mcp-builder | pinned and project 100 | violated | violated | True |
| .claude/skills | claude | internal-comms | pinned and project 100 | violated | violated | True |
| .claude/skills | codex | webapp-testing | pinned 100 | violated | satisfied | True |
| .claude/skills | codex | mcp-builder | pinned 100 | violated | satisfied | True |
| .claude/skills | codex | internal-comms | pinned 100 | violated | satisfied | True |
| .claude/skills | opencode | webapp-testing | pinned 92, project 8 | violated | violated | True |
| .claude/skills | opencode | mcp-builder | pinned 90, project 10 | violated | violated | True |
| .claude/skills | opencode | internal-comms | pinned 94, project 6 | violated | violated | True |
| .agents/skills | claude | webapp-testing | pinned 100 | violated | satisfied | True |
| .agents/skills | claude | mcp-builder | pinned 100 | violated | satisfied | True |
| .agents/skills | claude | internal-comms | pinned 100 | violated | satisfied | True |
| .agents/skills | codex | webapp-testing | pinned and project 100 | violated | violated | True |
| .agents/skills | codex | mcp-builder | pinned and project 100 | violated | violated | True |
| .agents/skills | codex | internal-comms | pinned and project 100 | violated | violated | True |
| .agents/skills | opencode | webapp-testing | pinned 92, project 8 | violated | violated | True |
| .agents/skills | opencode | mcp-builder | pinned 89, project 11 | violated | violated | True |
| .agents/skills | opencode | internal-comms | pinned 91, project 9 | violated | violated | True |
| .codex/skills | claude | webapp-testing | pinned 100 | satisfied | satisfied | True |
| .codex/skills | claude | mcp-builder | pinned 100 | satisfied | satisfied | True |
| .codex/skills | claude | internal-comms | pinned 100 | satisfied | satisfied | True |
| .codex/skills | codex | webapp-testing | pinned and project 100 | violated | violated | True |
| .codex/skills | codex | mcp-builder | pinned and project 100 | violated | violated | True |
| .codex/skills | codex | internal-comms | pinned and project 100 | violated | violated | True |
| .codex/skills | opencode | webapp-testing | pinned 100 | satisfied | satisfied | True |
| .codex/skills | opencode | mcp-builder | pinned 100 | satisfied | satisfied | True |
| .codex/skills | opencode | internal-comms | pinned 100 | satisfied | satisfied | True |
| .opencode/skills | claude | webapp-testing | pinned 100 | satisfied | satisfied | True |
| .opencode/skills | claude | mcp-builder | pinned 100 | satisfied | satisfied | True |
| .opencode/skills | claude | internal-comms | pinned 100 | satisfied | satisfied | True |
| .opencode/skills | codex | webapp-testing | pinned 100 | satisfied | satisfied | True |
| .opencode/skills | codex | mcp-builder | pinned 100 | satisfied | satisfied | True |
| .opencode/skills | codex | internal-comms | pinned 100 | satisfied | satisfied | True |
| .opencode/skills | opencode | webapp-testing | pinned 89, project 11 | violated | violated | True |
| .opencode/skills | opencode | mcp-builder | pinned 88, project 12 | violated | violated | True |
| .opencode/skills | opencode | internal-comms | pinned 94, project 6 | violated | violated | True |

The observed check matches part A in 45 of 45 cells, and the earlier receipt is retired in 36 of 36. For OpenCode the probe samples 60 listings, because a single listing cannot show that its answer varies.

**`behaviour.py`.** Two tiny skills with one name answer differently. Claude Code 2.1.296 and Codex 0.159.3 each followed the pinned one in 20 of 20 runs when it was alone and the project copy in 20 of 20 runs when both existed (project copy in `.claude/skills` for Claude Code, `.agents/skills` for Codex; one prompt, one model per agent). Codex ran without its sandbox, which needs user namespaces this host lacks. A first run with 5 runs on Claude Code only is in `history/`.

**`realrepos.py`.** Thirty repositories from the search results, ten per project root, each with a differing copy of an upstream skill; only the skill root is checked out. Agentpack reported the clash in 30 of 30; the observed verdict was the one predicted for the root in 55 of 60 agent runs. The five exceptions are violated or unknown where satisfied or violated was predicted, never the reverse: `claude-api` is also a built-in Claude Code skill (two runs), one project copy is marked `user-invocable: false` and so disappears from the session list, and one repository has 1,467 project skills, for which neither agent's catalog was complete.

## CI gate (`ci_gate.py`, `ci-results/`)

On GitHub's `ubuntu-latest`, `macos-latest` and `windows-latest` runners the workflow installs the released binary and Claude Code, Codex and OpenCode from npm, with no account. For each agent: unknown (4) before a probe, ready (0) after it, unknown (4) when a same-named project skill appears, violated (3) after a new probe. All 36 steps gave the expected exit status.

## Regression suites

`scenario.py`: 16 states, 14 outcomes written down before the runs, all hold. `faults.py`: 8 of 8 fault classes and 6 of 6 ready controls.

## History

`history/` keeps earlier results, including runs that went wrong: the scenario against 0.4.2 (two expectations failed, which led to a tool change), the false alarms of 0.4.3, the first three-agent runs whose direct reading was not yet independent of Agentpack's staging, the missing receipt expiry for `.codex/skills` in 0.4.4, and the first delivery run. `history/macos-0.4.2/` is the first evaluation on macOS with Codex 0.159.2.

## Limits

- One author wrote the tool, the fixtures and the expectations, with AI assistance (OpenAI Codex for release 0.4.2 and the first runner; Anthropic Claude for the session observation, later fixes and the scripts here). This is not an independent replication.
- One version of each agent. Agents change their discovery rules between releases.
- Model-call experiments use 3 runs per cell (delivery) or 20 (behaviour), one prompt, and one model per agent.
- Probes run with an empty user home, so user-level skills are outside the observation. Observation covers skills, not subagents, commands or hooks.
- Search counts are GitHub's on the day of the run, capped at 100 files per query.
