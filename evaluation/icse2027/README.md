# Agentpack ICSE 2027 evaluation artifact

Scripts and recorded results for the paper's evaluation. Every Agentpack number in the paper was measured with the released **Agentpack 0.7.2** (https://github.com/olegpustovit/agentpack/releases/tag/v0.7.2; linux arm64 binary sha256 `bc84f06b2877f02203e9980dc21bd4681563ddb3c06cbb8678f5436f1ce8759d`) on 2026-10-17, on Linux 6.8 aarch64, with Claude Code 2.1.299, Codex 0.159.3, OpenCode 1.17.11, Cursor 2026.08.25-3e8eec8, Grok 1.0.44 and Antigravity 1.4.0. Claude Code updated itself to 2.1.302 during the day; for the delivery tables 2.1.299 was installed into a private npm prefix and put first on `PATH`, so every tool's rows use the same agents. The supply-chain experiment also runs release 0.6.2 as the top-level-pin setup. The IDE check by hand used 0.6.0, whose `eject` differs from 0.7.2's in Windows paths, the locked npm tree of MCP servers and the Cursor and Antigravity rule, hook and subagent fixes; `eject_headless.py` repeats it on the agents' CLIs with 0.7.2 (`eject-headless-0.7.2.json`). Earlier results (0.5.x, 0.6.1) are kept under `history/`.

| Experiment | Script | Results | Calls a model |
|---|---|---|---|
| What 16 tools deliver from 9 pinned published inputs (Table I of the paper) | `ecosystem.py` | `ecosystem-results.json` | no |
| The same on 12 packages drawn after the code was frozen, and on 10 more that hold hooks or subagents | `heldout.py`, `ecosystem.py` | `heldout-inputs.json`, `heldout-results.json`, `heldout-kinds-inputs.json`, `heldout-kinds-results.json` | no |
| Table II and the held-out sample on skills alone | `scope.py` | `ecosystem-scope.json`, `heldout-scope.json` | no |
| One test package, installed by five tools, used by six agents (Table III) | `competitors_model.py`, `competitors_summary.py` | `competitors-model-results.json` (Table II of the paper) | yes |
| IDE front ends on an ejected project | `agentpack eject`, by hand; `eject_headless.py` for the agents' CLIs | `ide-check.json`, `eject-headless-0.6.2.json` | yes |
| How often the official catalogs' npx-started MCP servers change | `drift.py` | `drift-results.json` | no |
| How much of each of those servers' dependency tree a top-level pin leaves open | `treedrift.py` | `treedrift-results.json` | no |
| What an npx-started server runs after the registry changes: no pin, a top-level pin (0.6.2), a locked tree (0.7.2) | `supplychain.py` | `supplychain-results.json` | no |
| Whether converted items keep their behaviour: a command's argument, an always-apply rule, a hook that blocks, a read-only subagent | `semantics.py` | `semantics-results.json` | yes |
| The commands of Fig. 1 | by hand | `fig1-0.6.1.txt` | no |

```sh
SHA=4bf10e9955ee46e10880c469af7efe428db39150ae99d01d10020abbf38e4fcf
uv run ecosystem.py setup --tools-dir TOOLS            # downloads the pinned tools
uv run ecosystem.py setup --tools-dir TOOLS --cargo-root CARGO --tool nodus --tool mars   # copies in and checks the cargo-built Nodus and mars
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
| Agentpack 0.7.2 | 9 (9) | 148 | 14 | 0 |
| Microsoft APM 0.33.0 | 9 (8) | 124 | 0 | 38 |
| rulesync 33.0.0 | 9 (5) | 99 | 9 | 54 |
| Ruler 0.3.44 (content copied in by hand) | 0 | 81 | 0 | 81 |
| Each agent's own install command | per agent | 58 | 4 | 100 |
| mars 0.16.0 (not pinned: default-branch head) | 8 (6) | 43 | 0 | 119 |
| Vercel skills 1.7.2 | 6 (6) | 36 | 0 | 126 |
| skills-lock 0.1.0 | 6 (6) | 36 | 0 | 126 |
| skillpm 4.0.0 | 5 (5) | 25 | 0 | 137 |
| liqiongyu/agentpack 0.9.1 | 1 (1) | 22 | 0 | 140 |
| Nodus 0.17.0 | 4 (3) | 15 | 0 | 147 |
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

## Held-out comparison (`heldout.py`, drawn 2026-10-16; all tools run again on 2026-10-17; Agentpack row on 0.7.2)

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
| Agentpack 0.7.2 (0.5.2 and 0.6.1 gave the same cells) | 12 | 85 | 4 | 1 | 0 |
| Ruler 0.3.44 (content copied in by hand) | 0 | 74 | 0 | 1 | 15 |
| Microsoft APM 0.33.0 | 11 | 69 | 0 | 1 | 20 |
| rulesync 33.0.0, Claude Code importer on every plugin (not documented) | 12 | 63 | 2 | 1 | 24 |
| Vercel skills 1.7.2 | 7 | 41 | 0 | 1 | 48 |
| skills-lock 0.1.0 | 7 | 41 | 0 | 1 | 48 |
| mars 0.16.0 (not pinned: default-branch head) | 12 (content delivered for 7) | 34 | 0 | 1 | 55 |
| rulesync 33.0.0 | 12 (content delivered for 4) | 28 | 2 | 0 | 60 |
| skillpm 4.0.0 | 6 | 29 | 0 | 1 | 60 |
| Each agent's own install command | 12 | 27 | 1 | 0 | 62 |
| Nodus 0.17.0 | 5 | 18 | 0 | 0 | 72 |
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

## Second held-out draw: packages with hooks or subagents (`heldout.py --need hook agent`, 2026-10-17)

The first draw held no hooks or subagents, the kinds where Agentpack's lead in Table II comes from. A second draw, from the same three repositories and commits, with its own seed and only packages that hold a hook or a subagent, excluding everything used before:

```sh
uv run heldout.py --src WORK/src --seed icse2027-heldout-kinds-2026-10-17 --need hook agent --exclude-inputs heldout-inputs.json --out heldout-kinds-inputs.json
GITHUB_TOKEN=$(gh auth token) uv run ecosystem.py run --inputs-file heldout-kinds-inputs.json --output heldout-kinds-results.json --tools-dir TOOLS --agentpack bin/agentpack --agentpack-sha256 $SHA --work WORK5 --jobs 4
```

10 packages (4 Claude Code, 4 Cursor, 2 Codex) hold 156 cells: subagents in 8, skills in 7, hooks in 4, commands in 4, MCP servers in 2, a rule in 1.

| Tool | Inputs accepted | Native | Converted | Partial | Absent |
|---|---|---|---|---|---|
| Agentpack 0.7.2 (0.6.1: 134 native, 11 converted, 3 partial, 8 absent) | 10 | 137 | 12 | 3 | 4 |
| rulesync 33.0.0, Claude Code importer on every plugin (not documented) | 8 | 92 | 6 | 4 | 54 |
| Ruler 0.3.44 (content copied in by hand) | 0 | 77 | 0 | 16 | 63 |
| Microsoft APM 0.33.0 | 8 | 67 | 0 | 9 | 80 |
| mars 0.16.0 (not pinned: default-branch head) | 10 | 66 | 0 | 12 | 78 |
| Each agent's own install command | 10 | 40 | 2 | 3 | 111 |
| rulesync 33.0.0 | 9 | 34 | 4 | 0 | 118 |
| skills-lock 0.1.0 | 6 | 36 | 0 | 18 | 102 |
| Vercel skills 1.7.2 | 6 | 34 | 0 | 20 | 102 |
| skillpm 4.0.0 | 6 | 28 | 0 | 12 | 116 |
| OpenPackage 0.11.3 | 8 | 23 | 0 | 3 | 130 |
| liqiongyu/agentpack 0.9.1 | 0 | 11 | 0 | 5 | 140 |
| Nodus 0.17.0 | 2 | 7 | 0 | 0 | 149 |
| dotagents 3.3.0 | 10 | 5 | 0 | 4 | 147 |
| retr0h/agentpack 1.1.0 | 10 | 0 | 0 | 0 | 156 |
| openskills 1.5.0 | 0 | 0 | 0 | 0 | 156 |
| skillshare 0.25.10 | 0 | 0 | 0 | 0 | 156 |

- Agentpack's misses with 0.7.2 (7 cells): one hook event OpenCode lacks (`ralph-loop`, Claude Code and Cursor versions, 2 cells); the hook of `openai/plugins/plugins/figma`, which Grok and Antigravity do not list (2 cells); two commands of `vercel` that share a name with a skill and arrive as skills on Claude Code and Grok (2 partial cells); Codex listing 50 of `pstack`'s 52 skills (1 partial cell, its skill-list budget). With 0.6.1 the figma hook, kept in `hooks.json` at the plugin root and not declared in its manifest, was missed on all six agents; release 0.6.2 reads that file.

## OpenPackage (added 2026-10-17)

OpenPackage (`opkg` 0.11.3, https://github.com/enulus/OpenPackage), a package manager for rules, commands, subagents, skills and MCP servers on many agents (no Grok platform), was added to every comparison after the first review round, with the command its README gives: `opkg install https://github.com/<repo>/tree/<commit>/<path> --platforms claude codex opencode cursor antigravity` (`--plugins NAME` for a catalog). `ecosystem.py setup` installs it into `TOOLS/opkg` at that version and checks its npm integrity. Table II: 50 of 162 (6 of 9 inputs accepted); first held-out draw: 29 of 90; second: 23 of 156. It records no commit or content hash, keeps `@latest`, and wrote up to 2,198 files into the checkout.

## Nodus and mars (added 2026-10-17)

A reviewer named Nodus and mars as the tools closest to Agentpack (a manifest and a lockfile, several agents). Both are Rust crates without a Linux arm64 release binary, so each was built with `cargo install --locked` (rustc 1.96.0, aarch64-unknown-linux-gnu) and the measured binary is recorded in `TOOLS`:

| Tool | Crate (crates.io checksum) | Binary sha256 |
|---|---|---|
| Nodus 0.17.0 (https://github.com/nodus-rs/nodus) | `nodus` 0.17.0 (`982449375cc106d8f28d9bfcd0091b9c1281e151b7682c664c0076aa7713e08e`) | `038c20a57ed26b060e842b251f27568ff54984ba47c00fedfa73a91fa78b8778` |
| mars 0.16.0 (https://github.com/haowjy/mars-agents) | `mars-agents` 0.16.0 (`9e366682d3595f867ad6185c82f0f1da27575162f2df48e58e12104f7c2da134`) | `d14af508b9bd6956383f656a7ba6cd2dbfadc6349d272eac0b6e7716bf422930` |

A cargo build is not byte-identical on another machine; `setup --cargo-root` copies the binary from a `cargo install --root` directory, refuses one with another sha256, and checks the crate in the local cargo cache against the crates.io checksum.

Each tool was given every input with the commands its own documentation gives (`documented_usage` and `note` in each run):

- **Nodus** (README Quick Start, `nodus add --help`): `nodus add <owner/repo> --revision <commit> --adapter claude --adapter codex --adapter cursor --adapter opencode --adapter agents`, then `nodus members enable <repo> <plugin>` where Nodus records the repository as a wrapper of several child packages, then `nodus doctor`. `nodus add` takes a whole repository (its package page reduces a tree URL to `owner/repo`); where `nodus members list` reports no child packages (`stripe/ai`, `anthropics/skills`) the whole repository is installed and the members step is not run. Nodus has no adapter for Grok or Antigravity; `copilot` (`.github/`) is not one of the six agents.
- **mars** (README Quick Start, `docs/cli/commands.md`): `mars init`, `mars add <owner/repo/path>`, `mars link .claude`, `.codex`, `.opencode`, `.cursor`. mars cannot pin a commit: it resolves a ref with `git ls-remote <url> <ref>`, which finds no commit ("reference ... not found"), and takes its first line, so `@main` resolved to `refs/heads/daisy/caffeinate/main` in `anthropics/claude-plugins-official` in a first run (discarded). The ref is left out, which mars documents as "HEAD for untagged repos" (no input repository has a tag), so this row uses each repository's head on 2026-10-17, not the pinned commit. mars has no target for Grok or Antigravity (its generic `.agents` target is deprecated), installs agents and skills only, and has no marketplace support: a marketplace repository is given at its root with the input's skill and agent names (`--skills`, `--agents`).

```sh
uv run ecosystem.py setup --tools-dir TOOLS --cargo-root CARGO --tool nodus --tool mars
GITHUB_TOKEN=$(gh auth token) uv run ecosystem.py run --tools-dir TOOLS --agentpack bin/agentpack --agentpack-sha256 $SHA --work WORK6 --tool nodus --tool mars --jobs 3
GITHUB_TOKEN=$(gh auth token) uv run ecosystem.py run --tools-dir TOOLS --agentpack bin/agentpack --agentpack-sha256 $SHA --work WORK7 --tool nodus --tool mars --jobs 3 --inputs-file heldout-inputs.json --output heldout-results.json
GITHUB_TOKEN=$(gh auth token) uv run ecosystem.py run --tools-dir TOOLS --agentpack bin/agentpack --agentpack-sha256 $SHA --work WORK8 --tool nodus --tool mars --jobs 3 --inputs-file heldout-kinds-inputs.json --output heldout-kinds-results.json
uv run ecosystem.py rescore; uv run ecosystem.py rescore --output heldout-results.json; uv run ecosystem.py rescore --output heldout-kinds-results.json
uv run table1.py; uv run table1.py --results heldout-results.json; uv run table1.py --results heldout-kinds-results.json
```

With `--tool`, only these two tools' runs were replaced; every other run in the three results files is unchanged (and the rescore changed no cell).

| Inputs | Tool | Accepted | Delivered (native) | Lockfile: commit / hash | Most files in the checkout |
|---|---|---|---|---|---|
| Table I (162 cells) | Nodus 0.17.0 | 4 of 9 | 15 | 8 / 8 | 1,325 |
| | mars 0.16.0 | 8 of 9 | 43 | 2 / 6 | 1,273 |
| First held-out draw (90) | Nodus 0.17.0 | 5 of 12 | 18 | 8 / 8 | 26 |
| | mars 0.16.0 | 12 of 12 | 34 | 4 / 7 | 3,024 |
| Second held-out draw (156) | Nodus 0.17.0 | 2 of 10 | 7 | 8 / 8 | 24 |
| | mars 0.16.0 | 10 of 10 | 66 | 2 / 9 | 1,435 |

- **What Nodus did not accept:** Codex plugins (`openai/plugins` marketplace: "missing field `path` at line 801"); Cursor plugins (Nodus reads the repository's Claude Code marketplace, which lists only `origin-apps`: "does not expose child package ..."); Claude Code plugins with hooks and the Stripe plugin from the catalog ("multiple packages want to manage .../claude-plugin/.claude-plugin/plugin.json", with the `claude` adapter only). Its lockfile also counts for those inputs: `nodus add` locks the whole repository at the pinned commit before the member step fails.
- **What Nodus delivers where it installs:** for Claude Code it writes `enabledPlugins` and `extraKnownMarketplaces` into `.claude/settings.json`; Claude Code lists no such plugin in a run without a person (it installs a project's marketplace plugins after the user trusts the folder), so no Claude Code cell counts. For Codex it enables `<plugin>@nodus` in `~/.codex/config.toml`, which `codex plugin list` reports as "not installed", so plugin MCP servers do not reach Codex. It writes the Playwright server to OpenCode as `playwright__playwright`, which the name rule does not match. It kept `@playwright/mcp@latest`.
- **What mars did not accept:** the 315-plugin catalog (discovery collision: two plugins ship an agent `code-reviewer`). Its lockfile records the commit it resolved and a hash; the commit is the pinned one only where the repository head had not moved. It wrote no MCP server or hook into any agent's configuration: a plugin's MCP file is only copied, with `@latest`, into its own store `.mars/staging/`. Grok receives skills and subagents through the `.claude/` files; Antigravity nothing.

## Skills alone (`scope.py`)

Every compared tool installs skills, so `scope.py` counts the skill cells only, over the six agents and over the agents each tool was given as targets. Table II: Agentpack, Ruler and rulesync's plugin importer 42 of 42, APM 41, skills-lock and Vercel skills 36 (skills-lock 30 of its 35). Held-out: Agentpack and Vercel skills 41 of 42, APM 35. Agentpack's lead in Table II comes from the other kinds on every agent.

## Does the content reach the model? (`competitors_model.py`, 2026-10-17)

The package of `delivery.py` (a skill, a slash command, a subagent, a rule, a prompt hook, a local MCP server and a language server, each answering with its own token) is put in a local marketplace repository and installed by Agentpack, APM, rulesync, Vercel skills and the agents' own install commands, each with its documented command for a local source, into an empty home and a fresh checkout. The agents are then started with real model calls, three runs per item; every tool gets its own random tokens, so a file of another tool's run cannot answer.

Strict rule. Before each agent runs, every file that holds a token outside the locations that agent loads content from (`OWN` in the script) is made unreadable, and the package repository's git history is removed (an agent was seen reading a hidden file with `git show`). A run counts only when the token is in the agent's final answer (parsed from its JSON events; for the language server, the token its hover returns) and the agent did not touch the package source. Agentpack reads a local package live at every launch, and Claude Code and Cursor load a plugin from a local marketplace in place, so for those the source stays readable. For every tool, a run in which the agent opened or tried to open the package source counts as a failure: Agentpack 4 runs (0.7.2; 7 with 0.6.1), APM 16, rulesync 10, Vercel skills 22, the agents' own commands 2 (in-place plugins of the agents' own commands are exempt, since their source is the installed copy). A combination works in at least 2 of 3 runs.

| Installed by | Claude Code | Codex | OpenCode | Cursor | Grok | Antigravity | All |
|---|---|---|---|---|---|---|---|
| Agentpack 0.7.2 | 7/7 | 6/6 | 7/7 | 5/6 | 6/7 | 6/6 | 37/39 |
| Agents' own commands | 7/7 | 5/6 | 0/7 | 5/6 | 4/7 | 2/6 | 23/39 |
| rulesync 33.0.0 | 4/7 | 4/6 | 4/7 | 3/6 | 0/7 | 3/6 | 18/39 |
| APM 0.33.0 | 4/7 | 3/6 | 3/7 | 4/6 | 1/7 | 1/6 | 16/39 |
| Vercel skills 1.7.2 | 0/7 | 1/6 | 1/7 | 1/6 | 0/7 | 1/6 | 4/39 |

- Agentpack's misses: Cursor does not fire the prompt hook in non-interactive mode (https://forum.cursor.com/t/169059); Cursor and Antigravity found the rule's token, but in two of three runs by opening the package source; Grok answered the subagent item by reading the subagent's file instead of delegating, in two of three runs.
- OpenCode hung at the model call when five tools used one ChatGPT account at once; its rows for Agentpack, rulesync and Vercel skills were run again alone and replace the hung ones (`sources` in the results). Native Claude Code and Cursor were run again after the source of an in-place plugin had been hidden from them by mistake. The language server was first scored by its own request log; that log sits in the source directory for a plugin installed in place, which hid the native Claude Code server's requests, so every language-server run is now scored by the hover token in the answer, like the other items (`competitors_summary.py --rescore`).
- Options every tool gets: Codex without its sandbox and with hook review skipped; Grok `--always-approve` for subagent and MCP items; Cursor `--force` and `--approve-mcps` for MCP; Claude Code project MCP servers enabled. Logins copied, and tokens refreshed during the run copied back.

## IDE front ends (`agentpack eject`, 2026-10-17)

`agentpack eject` (new in 0.6.0) writes the staged content into each agent's own project files, for front ends that are not started through a launcher. On a MacBook (macOS 27.0.1, arm64) a project was prepared with `agentpack add` and `agentpack eject` (build `8bcfcf6`, released as 0.6.0; Cursor checked on `fb97f97`, whose Cursor part is the same), and the owner asked the test prompts in each IDE. VS Code with the Claude Code extension, Cursor and Antigravity returned the token for skill, command, subagent, rule and MCP server; VS Code with the Codex extension and the Codex app for skill, rule and MCP server (prompts asked). Cursor ran the project's MCP server after the user approved it. `ide-check.json` has each answer. Before that, an eject that registered the bundle as a Claude Code project plugin was not loaded by the VS Code extension without an interactive install; eject now writes plain `.claude/` files. The same eject, checked with each agent's CLI and only the ejected files present (staging and package source deleted), delivered every kind on Claude Code, Codex, Cursor and Antigravity except Cursor's hook; repeated with release 0.6.2 on 2026-10-17 (`eject-headless-0.6.2.json`, one run per kind) with the same result. The MacBook test project was ejected again with 0.6.2 for a last IDE recheck.

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

## The dependency tree a top-level pin leaves open (`treedrift.py`, 2026-10-17)

For each of the four npm packages that the official catalogs start through npx (`drift-results.json`), `treedrift.py` resolves the version `latest` names today to its full dependency tree with npm (`install --package-lock-only --ignore-scripts`, as `agentpack lock` does) and compares each package's publish time with the server version's.

| Server version | Released | Packages in its tree | Published after the server version | Dependency specs that are ranges |
|---|---|---|---|---|
| `@playwright/mcp` 0.0.83 | 2026-09-28 | 3 | 0 | 0 |
| `@xeroapi/xero-mcp-server` 0.0.17 | 2026-05-26 | 119 | 29 | 185 |
| `firebase-tools` 15.34.0 | 2026-10-14 | 634 | 9 | 970 |
| `xcodebuildmcp` 2.7.0 | 2026-07-23 | 151 | 38 | 221 |

A package published after the server version could not have been installed on the server's release day: a pin of the server alone leaves it to the day of installation. `@playwright/mcp` pins its two dependencies exactly.

## What a locked server runs after the registry changes (`supplychain.py`, 2026-10-17)

A local npm registry (in the script; nothing is published) serves a toy MCP server `approbe-srv-7f3c@1.0.0` that depends on `approbe-dep-7f3c@^1.0.0` and answers `initialize` with the marker of the dependency code it loaded. The names do not exist on registry.npmjs.org. Three setups are locked once against the original registry: the definition as plugins publish it (`npx -y srv@latest`), Agentpack 0.6.2 (the server pinned to 1.0.0 with its integrity, started by npx: the top-level pin), and Agentpack 0.7.2 (the server and its dependency locked, started by `agentpack mcp-exec`). Then one change is made at a time and the server is started as an agent would start it (the command its configuration holds), by a teammate with empty npm and Agentpack caches; for the edited cache, by the same user again.

| Change | No pin | Top-level pin (0.6.2) | Locked tree (0.7.2) |
|---|---|---|---|
| none | dep 1.0.0 | dep 1.0.0 | dep 1.0.0 |
| dep 1.0.1 published | dep 1.0.1 | dep 1.0.1 | dep 1.0.0 |
| dep 1.0.0 republished with other code (registry integrity changed to match) | replaced code | replaced code | refused to start |
| srv 1.0.0 republished with other code | replaced server | replaced server | refused to start |
| installed dep code edited in the local cache | edited code | edited code | dep 1.0.0 (unpacked again from the verified tarballs) |

mcp-lock 0.1.0 is not a setup: it reads only `https://registry.npmjs.org` (hard-coded in `dist/mcp-lock.js`), so it cannot be pointed at a test registry; by its README it records and pins the top-level package, as the 0.6.2 setup does. `uv run supplychain.py --agentpack-top AP062 --agentpack-tree AP072` reproduces the table in about 15 seconds with node and npm on PATH.

## Does converted content keep its behaviour? (`semantics.py`, 2026-10-17)

A probe package (a Claude Code plugin) holds four items whose behaviour a conversion could change. (1) `commands/delivery-args.md` takes an argument (`$ARGUMENTS`): invoked by name with a random word, the answer must hold the command's token joined to that word. (2) `rules/always-sign.mdc` (`alwaysApply: true`) asks for a closing line; the prompt is "What is 17 plus 25?". (3) A `PreToolUse` hook on `Bash` reads the event from stdin, records it, and exits 2 for a command naming a probe file; the agent is asked to `touch` that file, and the probe passes when the file is absent afterwards and the hook's record holds the command. (4) `agents/readonly-agent.md` (`tools: Read, Grep, Glob`) is asked through delegation to create a file. Each tool installs the package as in `competitors_model.py`, with the same logins, grants, hiding of token files and package-source rule; shell commands run with each agent's skip-permission flag, for every tool alike. Three runs per probe and agent; a cell passes in at least 2 of 3. A control with nothing installed runs each probe once per agent.

`semantics_summary.py` merges the result files (a later file's tool replaces an earlier one's) and prints Table III: `uv run semantics_summary.py RUN1.json RUN071.json RUN072.json`. Agentpack's row is release 0.7.2; APM, rulesync, the agents' own commands and the control do not depend on the Agentpack build and come from the runs of the same day.

| Installed by | Command argument | Rule applied unasked | Hook blocks | Read-only subagent |
|---|---|---|---|---|
| Agentpack 0.7.2 | 6 of 6 | 6 of 6 | 6 of 6 | 5 of 6 |
| Microsoft APM 0.33.0 | 3 of 5 | 0 of 5 | 3 of 5 | 4 of 5 |
| rulesync 33.0.0 | 6 of 6 | 0 of 6 | 0 of 6 | 5 of 6 |
| Agents' own commands | 5 of 6 | 0 of 6 | 4 of 6 | 3 of 6 |
| Nothing installed (runs) | 0 of 6 | 0 of 6 | 0 of 6 (file created on all six) | 3 of 6 |

APM's runs on OpenCode could not start ("could not answer a plain prompt"), so OpenCode is left out of its row. The read-only probe is reported but is not in the paper's table: with nothing installed it passed on three agents, whose main agent declined to create the file. Agentpack's one read-only miss is Grok, whose main agent created the file itself.

The probes found four faults in Agentpack, each fixed before the 0.7.2 measurements: Cursor applied no staged rule (0.7.0 links the rules into `.cursor/rules`); Antigravity denied every tool call while a `PreToolUse` hook was installed, because Agentpack answered without a `decision` (0.7.1 answers `"allow"`, which hands the call to the default policy); Antigravity discarded rules without a `trigger` (0.7.1 writes `trigger: always_on`, `glob`, `model_decision` or `manual`); Cursor ignored a subagent's `tools` list (0.7.2 stages a read-only Claude subagent with `readonly: true`). `semantics-results.json` holds every row used.
