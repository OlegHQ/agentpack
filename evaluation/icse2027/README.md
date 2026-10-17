# Agentpack ICSE 2027 evaluation artifact

Scripts and recorded results for the paper's evaluation. Every Agentpack number in the paper was measured with the released **Agentpack 0.6.1** (https://github.com/olegpustovit/agentpack/releases/tag/v0.6.1; linux arm64 binary sha256 `4bf10e9955ee46e10880c469af7efe428db39150ae99d01d10020abbf38e4fcf`) on 2026-10-17, on Linux 6.8 aarch64, with Claude Code 2.1.299, Codex 0.159.3, OpenCode 1.17.11, Cursor 2026.08.25-3e8eec8, Grok 1.0.44 and Antigravity 1.4.0. Claude Code 2.1.299 is put first on `PATH` with `DISABLE_AUTOUPDATER=1`. Antigravity updated itself from 1.3.5 to 1.4.0 on 2026-10-16; every tool's rows were run again with 1.4.0. The one exception to 0.6.1 is the Antigravity row of the model-call test (below), run with build `9952a1f`: the account's Antigravity quota was used up before the 0.6.1 rerun, and `9952a1f` differs from `v0.6.1` only in the `eject` command and the version string (`git diff 9952a1f v0.6.1 -- internal/ ':!internal/eject'`).

| Experiment | Script | Results | Calls a model |
|---|---|---|---|
| What 13 tools deliver from 9 pinned published inputs (Table II) | `ecosystem.py` | `ecosystem-results.json` | no |
| The same on 12 packages drawn after the code was frozen | `heldout.py`, `ecosystem.py` | `heldout-inputs.json`, `heldout-results.json` | no |
| Table II and the held-out sample on skills alone | `scope.py` | `ecosystem-scope.json`, `heldout-scope.json` | no |
| One test package, installed by five tools, used by six agents (Table III) | `competitors_model.py`, `competitors_summary.py` | `competitors-model-results.json` | yes |
| IDE front ends on an ejected project | `agentpack eject`, by hand | `ide-check.json` | yes |
| How often the official catalogs' npx-started MCP servers change | `drift.py` | `drift-results.json` | no |
| The commands of Fig. 1 | by hand | `fig1-0.6.1.txt` | no |

```sh
SHA=4bf10e9955ee46e10880c469af7efe428db39150ae99d01d10020abbf38e4fcf
uv run ecosystem.py setup --tools-dir TOOLS            # downloads the pinned tools
GITHUB_TOKEN=$(gh auth token) uv run ecosystem.py run --tools-dir TOOLS --agentpack bin/agentpack --agentpack-sha256 $SHA --work WORK --jobs 3
GITHUB_TOKEN=$(gh auth token) uv run ecosystem.py run --inputs-file heldout-inputs.json --output heldout-results.json --tools-dir TOOLS --agentpack bin/agentpack --agentpack-sha256 $SHA --work WORK3 --jobs 3
uv run ecosystem.py rescore                            # recomputes every cell and the summary from the stored evidence
uv run scope.py && uv run scope.py --results heldout-results.json
uv run competitors_model.py --tools-dir TOOLS --agentpack bin/agentpack --agentpack-sha256 $SHA --runs 3 --jobs 1 --backup-dir BACKUP   # real model calls on your accounts
uv run competitors_summary.py --rescore WORK4/*-result.json
uv run drift.py
```

`ecosystem.py` runs every tool and agent in empty temporary homes; no account or user setting is read. `competitors_model.py` copies the agents' logins into its temporary homes (and copies a refreshed token back), runs your signed-in agents and calls their models.

Antigravity 1.4.0 prints its plugin check as one summary line (`skills 1 · agents 1 · rules 1 · hooks 1 · mcpServers 0`) where 1.3.5 printed `hooks : 1 processed`; `ecosystem.py` reads both (`agy_hooks`), and `rescore` parses the stored output again. With the old parser alone, Agentpack's four Antigravity hook cells read as absent.

Earlier builds: Table II and the functional test of the previous version of the paper used the 0.5.0 release candidate `ee0ffb3`, and the held-out comparison release 0.5.2; every number they gave is unchanged with 0.6.1 (Table II, all 13 rows; held-out 89 of 90). Their recordings are in `history/`.

## What each tool delivers (`ecosystem.py`)

Nine inputs pinned by commit (three Claude Code plugins, two Cursor plugins, a Codex plugin, a bare skill, two marketplace repositories) are given to each tool with the command its documentation gives. A cell is one input, one agent and one kind of content (skill, command, subagent, rule, hook, MCP server); 162 cells exist. The scoring rule is the docstring of `ecosystem.py`, is copied into the results (`scoring_rule`) and is applied by code to every tool's stored evidence: an item counts when the agent's own listing shows it, or, where the agent has no listing without a model, when a file holds it where the agent documents it. Numbers are in `summary.<tool>`.

| Tool | Inputs accepted (and something delivered) | Native | Converted | Absent |
|---|---|---|---|---|
| Agentpack 0.6.1 | 9 (9) | 148 | 14 | 0 |
| Microsoft APM 0.33.0 | 9 (8) | 124 | 0 | 38 |
| rulesync 33.0.0 | 9 (5) | 99 | 9 | 54 |
| Ruler 0.3.44 (content copied in by hand) | 0 | 81 | 0 | 81 |
| Each agent's own install command | per agent | 58 | 4 | 100 |
| Vercel skills 1.7.2 | 6 (6) | 36 | 0 | 126 |
| skills-lock 0.1.0 | 6 (6) | 36 | 0 | 126 |
| skillpm 4.0.0 | 5 (5) | 25 | 0 | 137 |
| liqiongyu/agentpack 0.9.1 | 1 (1) | 22 | 0 | 140 |
| openskills 1.5.0 | 3 (2) | 8 | 0 | 154 |
| dotagents 3.3.0 | 8 (6) | 6 | 0 | 156 |
| skillshare 0.25.10 | 1 (1) | 6 | 0 | 156 |
| retr0h/agentpack 1.1.0 | 7 (1) | 5 | 0 | 157 |

- Agentpack's 14 conversions: commands on Codex and on Antigravity (as skills, 4 inputs each), hooks on OpenCode (as a generated plugin, 4), a rule on Claude Code and on Grok (as a skill, 1 each).
- One cell has another tool ahead of Agentpack: Ruler puts a Cursor rule into `CLAUDE.md`, Agentpack delivers it to Claude Code as a skill.
- rulesync documents a plugin importer for Claude Code plugins only. Pointed at the Codex and Cursor plugins as well (not documented), it reaches 123 native and 9 converted; this is the row `rulesync-claude-importer` in the results.
- Lockfile: Agentpack's `pack.lock` records the pinned commit and a content hash in 9 of 9 inputs, and delivers both npx-started Playwright servers as `@playwright/mcp@0.0.83`. APM records the commit in 8 and a hash in 9 and delivers `@latest`; rulesync has a lockfile for the bare skill only. Claude Code's own plugin record holds the commit its catalog was at when installed, which is the pinned one for 2 inputs (2026-10-14; it was 4 on 2026-10-13, before the official catalog moved on).
- Where files go: Agentpack writes `agentpack.toml` and `pack.lock` to the checkout and one link at launch; the rest is in its own home and staging directory, a Codex home under `/var/tmp/ap-c`, and an empty `~/.codex/history.jsonl`. APM writes up to 137 files into the checkout, rulesync up to 264.
- Limits: hooks are one item per input (a hook file that names one of the input's scripts counts; events are not compared). Cursor skills, commands, subagents, rules and hooks, and Antigravity skills, subagents and rules, have no listing without a model and are decided by files. Cursor's own install command needs a login and was not run.

## Held-out comparison (`heldout.py`, drawn 2026-10-16; all 13 tools run again on 2026-10-17 with Agentpack 0.6.1 and Antigravity 1.4.0)

The nine inputs above were chosen by the authors, and the 40 packages below were used during development. After the code was frozen (release 0.5.2, which prints `0.5.2`; sha256 of the linux arm64 binary `089f2dada14209c86ccf94f889bb5c27c4e0054bdcc74940455818d6882b34b1`), `heldout.py` drew 12 new packages by a fixed rule:
- **Sources:** the official Claude Code, Cursor and Codex plugin repositories at the commits pinned in `ecosystem.py`.
- **Exclusions:** every input above and every package of `plugins-corpus.json`.
- **Eligibility:** a plugin directory that holds at least one item of the six kinds.
- **Order:** sha256 of the seed `icse2027-heldout-2026-10-16`, the repository and the path, ascending, taking the first 4 eligible packages per repository (`heldout-inputs.json` lists every directory drawn, in order).

The draw is in `heldout-inputs.json`. `ecosystem.py` ran them unchanged, with the same scoring rule and agent versions (Claude Code 2.1.299 first on `PATH`):

```sh
uv run heldout.py --src WORK/src
GITHUB_TOKEN=$(gh auth token) uv run ecosystem.py run --inputs-file heldout-inputs.json --output heldout-results.json --tools-dir TOOLS --agentpack bin/agentpack --agentpack-sha256 4bf10e9955ee46e10880c469af7efe428db39150ae99d01d10020abbf38e4fcf --work WORK --jobs 3      # every tool; add --tool T to run only T
```

The 12 packages hold 90 cells: skills in 7 packages, MCP servers in 6, one command and one rule, and no hooks or subagents.

| Tool | Inputs accepted | Native | Converted | Partial | Absent |
|---|---|---|---|---|---|
| Agentpack 0.6.1 (0.5.2 gave the same cells) | 12 | 85 | 4 | 1 | 0 |
| Ruler 0.3.44 (content copied in by hand) | 0 | 74 | 0 | 1 | 15 |
| Microsoft APM 0.33.0 | 11 | 69 | 0 | 1 | 20 |
| rulesync 33.0.0, Claude Code importer on every plugin (not documented) | 12 | 63 | 2 | 1 | 24 |
| Vercel skills 1.7.2 | 7 | 41 | 0 | 1 | 48 |
| skills-lock 0.1.0 | 7 | 41 | 0 | 1 | 48 |
| rulesync 33.0.0 | 12 (content delivered for 4) | 28 | 2 | 0 | 60 |
| skillpm 4.0.0 | 6 | 29 | 0 | 1 | 60 |
| Each agent's own install command | 12 | 27 | 1 | 0 | 62 |
| liqiongyu/agentpack 0.9.1 (modules declared by hand) | 0 | 15 | 0 | 1 | 74 |
| dotagents 3.3.0 | 12 | 7 | 0 | 0 | 83 |
| retr0h/agentpack 1.1.0 | 12 | 0 | 0 | 0 | 90 |
| openskills 1.5.0 | 0 | 0 | 0 | 0 | 90 |
| skillshare 0.25.10 | 0 | 0 | 0 | 0 | 90 |

- **Scoring:** a cell counts as delivered only when every item of it arrives (partial counts as not delivered).
- **The partial cell** is the same for Agentpack, APM and Vercel skills: Codex lists 1 of the 23 skills of `public-equity-investing`, which is the skill-list budget Codex documents.
- **Agentpack's conversions:** one command on Codex and Antigravity, and one rule on Claude Code and Grok, as in Table I of the paper.
- **MCP servers started through npm:** there are two, `xcodebuildmcp@latest` and `@xeroapi/xero-mcp-server@latest`.
  - Agentpack delivered them as `xcodebuildmcp@2.7.0` and `@xeroapi/xero-mcp-server@0.0.17` to every agent.
  - APM and the agents' own commands kept `@latest`.
  - rulesync and Vercel skills wrote neither.
- **Files written into the checkout:** Agentpack wrote `agentpack.toml` and `pack.lock` and nothing else. APM wrote up to 1,476 files, Vercel skills up to 538 and rulesync up to 71.

## Skills alone (`scope.py`)

Every compared tool installs skills, so `scope.py` counts the skill cells only, over the six agents and over the agents each tool was given as targets. Table II: Agentpack, Ruler and rulesync's plugin importer 42 of 42, APM 41, skills-lock and Vercel skills 36 (skills-lock 30 of its 35). Held-out: Agentpack and Vercel skills 41 of 42, APM 35. Agentpack's lead in Table II comes from the other kinds on every agent.

## Does the content reach the model? (`competitors_model.py`, 2026-10-17)

The package of `delivery.py` (a skill, a slash command, a subagent, a rule, a prompt hook, a local MCP server and a language server, each answering with its own token) is put in a local marketplace repository and installed by Agentpack, APM, rulesync, Vercel skills and the agents' own install commands, each with its documented command for a local source, into an empty home and a fresh checkout. The agents are then started with real model calls, three runs per item; every tool gets its own random tokens, so a file of another tool's run cannot answer.

Strict rule. Before each agent runs, every file that holds a token outside the locations that agent loads content from (`OWN` in the script) is made unreadable, and the package repository's git history is removed (an agent was seen reading a hidden file with `git show`). A run counts only when the token is in the agent's final answer (parsed from its JSON events) and the agent did not touch the package source. Agentpack reads a local package live at every launch, and Claude Code and Cursor load a plugin from a local marketplace in place, so for those the source stays readable; for Agentpack, a run that opens it is not counted. A combination works in at least 2 of 3 runs.

| Installed by | Claude Code | Codex | OpenCode | Cursor | Grok | Antigravity | All |
|---|---|---|---|---|---|---|---|
| Agentpack 0.6.1 | 7/7 | 6/6 | 7/7 | 4/6 | 6/7 | 5/6 | 35/39 |
| Agents' own commands | 6/7 | 5/6 | 0/7 | 5/6 | 4/7 | 2/6 | 22/39 |
| rulesync 33.0.0 | 4/7 | 4/6 | 4/7 | 3/6 | 0/7 | 3/6 | 18/39 |
| APM 0.33.0 | 4/7 | 3/6 | 3/7 | 4/6 | 1/7 | 1/6 | 16/39 |
| Vercel skills 1.7.2 | 0/7 | 1/6 | 1/7 | 1/6 | 0/7 | 1/6 | 4/39 |

- Agentpack's misses: Cursor does not fire the prompt hook in non-interactive mode (https://forum.cursor.com/t/169059); Cursor and Antigravity found the rule's token, but in two of three runs by opening the package source; Grok answered the subagent item by reading the subagent's file instead of delegating, in two of three runs.
- OpenCode hung at the model call when five tools used one ChatGPT account at once; its rows for Agentpack, rulesync and Vercel skills were run again alone and replace the hung ones (`sources` in the results). Native Claude Code and Cursor were run again after the source of an in-place plugin had been hidden from them by mistake.
- Options every tool gets: Codex without its sandbox and with hook review skipped; Grok `--always-approve` for subagent and MCP items; Cursor `--force` and `--approve-mcps` for MCP; Claude Code project MCP servers enabled. Logins copied, and tokens refreshed during the run copied back.

## IDE front ends (`agentpack eject`, 2026-10-17)

`agentpack eject` (new in 0.6.0) writes the staged content into each agent's own project files, for front ends that are not started through a launcher. On a MacBook (macOS 27.0.1, arm64) a project was prepared with `agentpack add` and `agentpack eject` (build `8bcfcf6`, released as 0.6.0; Cursor checked on `fb97f97`, whose Cursor part is the same), and the owner asked the test prompts in each IDE. VS Code with the Claude Code extension, Cursor and Antigravity returned the token for skill, command, subagent, rule and MCP server; VS Code with the Codex extension and the Codex app for skill, rule and MCP server (prompts asked). Cursor ran the project's MCP server after the user approved it. `ide-check.json` has each answer. Before that, an eject that registered the bundle as a Claude Code project plugin was not loaded by the VS Code extension without an interactive install; eject now writes plain `.claude/` files. The same eject, checked with each agent's CLI and only the ejected files present (staging and package source deleted), delivered every kind on Claude Code, Codex, Cursor and Antigravity except Cursor's hook.

## How often npx-started servers change (`drift.py`)

The three official catalogs at the commits of Table II declare 126 MCP servers: 110 remote, 16 local commands. The 5 started through npx all request no exact version; their 4 npm packages published versions in the 90 days before 2026-10-16 as follows: `@playwright/mcp` 45, `firebase-tools` 28, `xcodebuildmcp` 1, `@xeroapi/xero-mcp-server` 0 (3 of 4 changed).

## Forty published packages (`plugins.py`)

Forty packages chosen by a fixed rule from the official Claude Code, Cursor and Codex plugin repositories, `anthropics/skills` and the three most-starred community marketplaces (`plugins-corpus.json`) are each added, locked and synced in a fresh project. Numbers are in `summary`.

- 40 of 40 lock and stage. Four of them are catalog entries that define the plugin, a language server only, in the catalog; one is a marketplace root whose nine plugins are all added.
- Sync repeated with the network blocked: 40 of 40 succeed with the same lock and the same staged items.

| Kind (source items) | In the staged tree | Listed by the agent's own CLI |
|---|---|---|
| Skills (594) | 594 on six agents | Claude Code 594, OpenCode 594, Grok 594, Codex 498 of 567 (27 hidden by the packages' own metadata) |
| Commands (130) | 130 on five agents; on Codex 130 as skills, 20 of them under `<name>-command` | Claude Code 130, OpenCode 130, Grok 130, Codex 117 |
| Subagents (50) | 50 on six agents (Codex: `agents/<name>.toml`; subagents a package keeps in subdirectories of `agents/` are staged at its top for every agent but OpenCode) | OpenCode 50, Grok 50, Antigravity 50, Claude Code 42, Codex 42 |
| Rule (1) | Cursor and Antigravity as a rule, the other four as a skill | no listing |
| Hooks (4 packages) | 4 on Claude Code, Codex, Cursor, Grok, Antigravity; OpenCode 3 as a generated plugin, 1 dropped | Grok lists 4; `agy plugin validate` reports hooks processed in 4 |
| MCP servers (16) | 16 on six agents | 16 on five agents; `agy mcp list` shows none |
| Language servers (4) | 4 on Claude Code, OpenCode, Grok; Codex, Cursor and Antigravity have no setting (warning) | Claude Code 4, OpenCode 4, Grok 4 |

- The 8 subagents that Claude Code and Codex do not list are the personas of `alirezarezvani/claude-skills`, whose declared names contain spaces (`name: Startup CTO`). The skills and commands Codex does not list are in the same repository, whose two packages hold 420 of the 594 skills; they are mostly the alphabetical tail of its catalog. Codex documents that its initial skills list uses at most 2% of the model's context window and that "for large skill sets, Codex may omit some skills from the initial list and show a warning" (https://developers.openai.com/codex/skills, read 2026-10-15); the Codex 0.159.3 binary contains the message "Host skills are available but omitted from the model-visible skills list because the skills context budget was exceeded." With the previous candidate (b9330796, kept in `history/`), which staged nested subagents in their subdirectories, Claude Code, Grok and Antigravity listed 17 of 50.
- Cursor has no listing without a model for skills, commands and subagents; Antigravity none for skills and commands.
- Every listing finished in this recording (`packages_without_observation` is empty for every agent and kind). In the run on the previous candidate, three experiments shared the host and three packages' listings timed out; those rows are kept in `history/0.5.0-rc-b9330796/`.
- Six MCP servers in these repositories start through an npm launcher. Locked through the package that ships them, 5 get an exact version; locked as a manifest entry, 6. The sixth package also defines its server as a local `node` command, which Agentpack uses and does not pin. In every pinned case the exact version is in all six agents' configurations, and sync with the network blocked changes nothing.

## One package on six agents through Agentpack (`delivery.py`, 0.5.0 release candidate, superseded by the table above)

A package with a skill, a slash command, a subagent, a rule, a prompt hook, a local stdio MCP server and a language server is locked and restored once, and each agent is started through Agentpack in non-interactive mode, three times per item. Each item makes the agent answer with its own token; the hook appends a line to a file; the MCP server (Node, inside the package, declared once in `.mcp.json`) returns a token that is nowhere in the package as text and records each call it receives. The language server (Node, declared in `.lsp.json` for the file extension `.dlv`) logs every request and answers a hover with its own token; the prompt asks for the hover text of a word in `sample.dlv`, and the item counts when the server logged the agent's `initialize` (the answer containing the hover token is recorded as `lsp_hover_token_in_answer`). It runs only on the three agents that have a language-server setting. Numbers are in `matrix`.

| Agent | Skill | Command | Subagent | Rule | MCP tool call | Language server | Hook fired |
|---|---|---|---|---|---|---|---|
| Claude Code | 3/3 | 3/3 | 3/3 | 3/3 | 3/3 | 3/3 | 18/18 |
| Codex | 3/3 | 3/3 | 2/3 | 3/3 | 3/3 | no setting | 14/15 |
| OpenCode | 3/3 | 3/3 | 3/3 | 3/3 | 3/3 | 3/3 | 18/18 |
| Cursor | 3/3 | 3/3 | 3/3 | 3/3 | 3/3 | no setting | 0/15 |
| Grok | 3/3 | 3/3 | 3/3 | 3/3 | 3/3 | 3/3 | 21/21 |
| Antigravity | 3/3 | 3/3 | 3/3 | 3/3 | 3/3 | no setting | 15/15 |

- The hover token was in the answer in all 9 language-server runs. OpenCode is launched with `OPENCODE_EXPERIMENTAL_LSP_TOOL=true` (set by Agentpack when the staged configuration has language servers) and Grok with `[features] lsp_tools = true` in the staged `config.toml`, so the model can call the language server.
- Cursor did not fire the prompt hook in non-interactive mode. Cursor's forum staff describe this as a known gap of the CLI (https://forum.cursor.com/t/169059, 2026-08-21). In an interactive Cursor session the same hook ran once for each of three prompts: `cursor-interactive-hook.md` (2026-10-15, release 0.5.1, which stages exactly what 0.5.0 stages).
- Codex's first subagent run did not finish within the 240-second limit; its hook line is missing because the run was stopped. In the recording on the previous candidate (`history/0.5.0-rc-b9330796/`) Codex answered 3 of 3 and Grok's third subagent run timed out instead.
- Grok needs `--always-approve`, which its user guide gives for scripts, for the subagent and for the MCP call. With it, the subagent answered in 3 of 3 runs. With the invocation used for the other agents the subagent run ends before the subagent answers: 0 of 3, kept in the results as `agent (original invocation)`; that is why Grok has 21 hook runs.
- For the MCP call Claude Code is given `--allowedTools` for the one tool and Cursor `--approve-mcps` (`mcp_launch_options`). The server recorded the call in 18 of 18 runs.
- OpenCode runs `openai/gpt-6.1-sol` (`--opencode-model`); on 2026-10-14 the ChatGPT-account sign-in refused the earlier default models.
- Codex runs a hook only after a person has reviewed it; these runs pass Codex's flag that skips the review. Codex runs without its sandbox, which needs user namespaces this host lacks.
- The lockfile is unchanged; the checkout holds only the files the script put there (`README.md`, `sample.dlv`).

## Earlier experiments in this folder

`collision.py`, `kinds.py`, `behaviour.py`, `realrepos.py`, `scenario.py`, `faults.py`, `prevalence.py` and `ci_gate.py`, with their result files and `ci-results/`, were recorded on 2026-10-12 with Agentpack 0.4.8 for an earlier draft and are not used by the paper. `history/` keeps older recordings; `history/0.4.8/` holds the 0.4.8 recordings of the three experiments above and the scripts that made them; `history/0.5.0-rc-b9330796/` holds the recordings on the previous candidate (b9330796), which staged nested subagents in their subdirectories, and the rows whose listings timed out there.

## Limits

One author wrote the tool and the scripts, with AI assistance; this is not an independent replication. One version of each agent, one Linux host, and one Mac for the IDE check. The model-call test has three runs per item, one prompt and one model per agent. No user study.

## mcp-lock on the two Playwright packages (2026-10-15)

mcp-lock 0.1.0 (https://github.com/mcpguards/mcp-lock) pins npm-started MCP servers in configuration files. Run in an empty directory with an empty `HOME` as `npx -y @mcpguards/mcp-lock@0.1.0 init --config <file> --pin` on each package's own MCP file:

- `cursor/plugins@1dcc2ba:third_party/playwright/mcp.json` (`mcpServers` form): "1 locked"; `.mcp.lock` written with version 0.0.83 and a sha512 integrity; the file rewritten to `@playwright/mcp@0.0.83`.
- `anthropics/claude-plugins-official@f359d43:external_plugins/playwright/.mcp.json` (bare form, no `mcpServers` key, as Claude Code plugins use): "No MCP servers found in any discovered config."

It installs nothing for any agent; the pin stays in the file it was given.
