# Agentpack ICSE 2027 evaluation artifact

Scripts and recorded results for the three experiments of the paper's evaluation. All three were recorded on 2026-10-14 and 2026-10-15 on Linux 6.8 aarch64 with one binary, the Agentpack 0.5.0 release candidate (sha256 `8592b2cf2c8d157a349165fba52ecb5f370cae7b68ba4b17b21affac9759e96a`), and these agents: Claude Code 2.1.299, Codex 0.159.3, OpenCode 1.17.11, Cursor 2026.08.25-3e8eec8, Grok 1.0.44, Antigravity 1.3.5. Claude Code updated itself to 2.1.300 during the work; the runs put the 2.1.299 binary first on `PATH` with `DISABLE_AUTOUPDATER=1`.

The release candidate was built with `make release` from commit ee0ffb3 of OlegHQ/agentpack. The published release v0.5.0 (https://github.com/OlegHQ/agentpack/releases/tag/v0.5.0) was built by CI (GoReleaser) from tag commit 215a1cf, which differs from ee0ffb3 only in `README.md`; its binaries therefore have other checksums (linux arm64: `3fabcebbde9409cb14118842edd1128847bbc5186a6908f3b03b30e83e726e8f`). With that binary, the demonstration commands without model calls gave the same pinned version, catalog and language-server output on 2026-10-15. To rerun the scripts against the release, pass its binary and its own checksum. Release v0.5.1 (2026-10-15, tag commit daa8fee) changes only tests, so that the suite passes on Windows, and the version string; it stages exactly what 0.5.0 stages (linux arm64: `5c764a8fe3174c863d6107460b181d8a5c3733a5362b14ca4247bc865c76a943`), and the commands typed in the demonstration video, model calls included, gave the expected output with it.

| Experiment | Script | Results | Calls a model |
|---|---|---|---|
| What 13 tools deliver from 9 pinned published inputs | `ecosystem.py` | `ecosystem-results.json` | no |
| 40 published packages through Agentpack | `plugins.py` | `plugins-results.json`, `plugins-corpus.json` | no |
| One package on six agents, item by item | `delivery.py` | `delivery-results.json` | yes |

```sh
SHA=8592b2cf2c8d157a349165fba52ecb5f370cae7b68ba4b17b21affac9759e96a
uv run ecosystem.py setup --tools-dir TOOLS            # downloads the pinned tools
GITHUB_TOKEN=$(gh auth token) uv run ecosystem.py run --tools-dir TOOLS --agentpack bin/agentpack --agentpack-sha256 $SHA --work WORK --jobs 4
uv run ecosystem.py rescore                            # recomputes every cell and the summary from the stored evidence
uv run plugins.py  --agentpack bin/agentpack --agentpack-sha256 $SHA --work WORK2 --jobs 3
uv run delivery.py --agentpack bin/agentpack --agentpack-sha256 $SHA --runs 3 --codex-unsandboxed   # real model calls on your accounts
```

`ecosystem.py` and `plugins.py` run every tool and agent in empty temporary homes; no account or user setting is read. `delivery.py` uses your signed-in agents through the Agentpack launcher, in a temporary checkout.

## What each tool delivers (`ecosystem.py`)

Nine inputs pinned by commit (three Claude Code plugins, two Cursor plugins, a Codex plugin, a bare skill, two marketplace repositories) are given to each tool with the command its documentation gives. A cell is one input, one agent and one kind of content (skill, command, subagent, rule, hook, MCP server); 162 cells exist. The scoring rule is the docstring of `ecosystem.py`, is copied into the results (`scoring_rule`) and is applied by code to every tool's stored evidence: an item counts when the agent's own listing shows it, or, where the agent has no listing without a model, when a file holds it where the agent documents it. Numbers are in `summary.<tool>`.

| Tool | Inputs accepted (and something delivered) | Native | Converted | Absent |
|---|---|---|---|---|
| Agentpack 0.5.0 | 9 (9) | 148 | 14 | 0 |
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

- The 8 subagents that Claude Code and Codex do not list are the personas of `alirezarezvani/claude-skills`, whose declared names contain spaces (`name: Startup CTO`). The skills and commands Codex does not list are in the same repository. With the previous candidate (b9330796, kept in `history/`), which staged nested subagents in their subdirectories, Claude Code, Grok and Antigravity listed 17 of 50.
- Cursor has no listing without a model for skills, commands and subagents; Antigravity none for skills and commands.
- Every listing finished in this recording (`packages_without_observation` is empty for every agent and kind). In the run on the previous candidate, three experiments shared the host and three packages' listings timed out; those rows are kept in `history/0.5.0-rc-b9330796/`.
- Six MCP servers in these repositories start through an npm launcher. Locked through the package that ships them, 5 get an exact version; locked as a manifest entry, 6. The sixth package also defines its server as a local `node` command, which Agentpack uses and does not pin. In every pinned case the exact version is in all six agents' configurations, and sync with the network blocked changes nothing.

## One package on six agents (`delivery.py`)

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
- Cursor did not fire the prompt hook in non-interactive mode.
- Codex's first subagent run did not finish within the 240-second limit; its hook line is missing because the run was stopped. In the recording on the previous candidate (`history/0.5.0-rc-b9330796/`) Codex answered 3 of 3 and Grok's third subagent run timed out instead.
- Grok needs `--always-approve`, which its user guide gives for scripts, for the subagent and for the MCP call. With it, the subagent answered in 3 of 3 runs. With the invocation used for the other agents the subagent run ends before the subagent answers: 0 of 3, kept in the results as `agent (original invocation)`; that is why Grok has 21 hook runs.
- For the MCP call Claude Code is given `--allowedTools` for the one tool and Cursor `--approve-mcps` (`mcp_launch_options`). The server recorded the call in 18 of 18 runs.
- OpenCode runs `openai/gpt-6.1-sol` (`--opencode-model`); on 2026-10-14 the ChatGPT-account sign-in refused the earlier default models.
- Codex runs a hook only after a person has reviewed it; these runs pass Codex's flag that skips the review. Codex runs without its sandbox, which needs user namespaces this host lacks.
- The lockfile is unchanged; the checkout holds only the files the script put there (`README.md`, `sample.dlv`).

## Earlier experiments in this folder

`collision.py`, `kinds.py`, `behaviour.py`, `realrepos.py`, `scenario.py`, `faults.py`, `prevalence.py` and `ci_gate.py`, with their result files and `ci-results/`, were recorded on 2026-10-12 with Agentpack 0.4.8 for an earlier draft and are not used by the paper. `history/` keeps older recordings; `history/0.4.8/` holds the 0.4.8 recordings of the three experiments above and the scripts that made them; `history/0.5.0-rc-b9330796/` holds the recordings on the previous candidate (b9330796), which staged nested subagents in their subdirectories, and the rows whose listings timed out there.

## Limits

One author wrote the tool and the scripts, with AI assistance; this is not an independent replication. One version of each agent, one host. The functional test has three runs per item, one prompt and one model per agent.

## mcp-lock on the two Playwright packages (2026-10-15)

mcp-lock 0.1.0 (https://github.com/mcpguards/mcp-lock) pins npm-started MCP servers in configuration files. Run in an empty directory with an empty `HOME` as `npx -y @mcpguards/mcp-lock@0.1.0 init --config <file> --pin` on each package's own MCP file:

- `cursor/plugins@1dcc2ba:third_party/playwright/mcp.json` (`mcpServers` form): "1 locked"; `.mcp.lock` written with version 0.0.83 and a sha512 integrity; the file rewritten to `@playwright/mcp@0.0.83`.
- `anthropics/claude-plugins-official@f359d43:external_plugins/playwright/.mcp.json` (bare form, no `mcpServers` key, as Claude Code plugins use): "No MCP servers found in any discovered config."

It installs nothing for any agent; the pin stays in the file it was given.
