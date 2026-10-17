# Changelog

All notable changes to `agentpack` are documented here. The format is based on
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/). `agentpack` is pre-release: see the
"Pre-release" note in `AGENTS.md` — breaking changes may land between versions without a migration
window.

## Unreleased

## [0.6.2] - 2026-10-17

### Fixed

- Hooks of a Codex plugin that keeps them in `hooks.json` at the plugin root and declares none in `.codex-plugin/plugin.json` (openai/plugins: figma) are read; Codex reads that file. They were left out for every agent.

## [0.6.1] - 2026-10-17

### Fixed

- `agentpack eject` on Windows. The staging paths it rewrites appear JSON-escaped in `.mcp.json` and the other JSON files (`C:\\Users\\...`); they were rewritten with single backslashes, which left the files invalid. Both forms are now rewritten, each with its own escaping.

## [0.6.0] - 2026-10-17

### Added

- `agentpack eject [claude|cursor|codex|agy ...]` writes what Agentpack stages into the project, for IDEs and other front ends that are not started through `agentpack <agent>`. Claude Code gets plain project files (`.claude/skills`, `.claude/commands`, `.claude/agents`, `.claude/rules`, servers in `.mcp.json` approved by name, hooks in `.claude/settings.json`); Cursor gets `.cursor/` (skills, commands, subagents, rules, `mcp.json`, `hooks.json`); Codex gets `.agents/skills`, `.codex/agents`, `.codex/config.toml`, `.codex/hooks.json` and a marked block in `AGENTS.md`; Antigravity gets the bundle in `.agents/plugins/agentpack-bundle`. Server and hook paths point at a copy in `.agentpack/eject/`, so nothing refers to the staging directory. Eject refuses to replace a file or setting it did not write, records everything in `.agentpack/eject/manifest.json`, and `agentpack eject --remove` takes out exactly that. Language servers are not ejected for Claude Code, which reads them only from plugins; OpenCode and Grok are refused (use the launcher).

### Fixed

- Starting another agent while one launched by Agentpack is running. The launch digest covered the agent, so `agentpack codex` next to a running `agentpack claude` asked for a full rebuild of the staging that session holds and stopped with "staging for mode is in use by an active launch". Agents without a workspace overlay now share one digest; Cursor and Antigravity keep their own and, when the staged tree is current, write only their overlay.

## [0.5.2] - 2026-10-16

No change to what agentpack stages: the GitHub owner in import paths, links and installers is now `olegpustovit`.

## [0.5.1] - 2026-10-15

No change to what agentpack stages; the binaries behave as 0.5.0.

### Fixed

- The test suite passes on Windows again. Three tests removed the directory above their Codex home when they finished; on Windows that directory holds the Codex homes of every project, so tests running at the same time lost theirs. Other tests compared paths as written to JSON with unescaped backslashes, or set `TMPDIR`, which Windows does not read.

## [0.5.0] - 2026-10-13

Found by adding 40 published packages (Claude Code, Cursor and Codex plugins, skills, community marketplaces) with 0.4.8, and by comparing what Agentpack and other tools deliver from the same nine packages.

### Added

- `agentpack add <marketplace> --plugin <name>` adds one named plugin of a marketplace catalog, following relative-path, `local`, `github`, `url` and `git-subdir` entries (`sha` before `ref`, `renames` honoured). The manifest records the plugin's own repository and path; `pack.lock` pins its commit.
- `agentpack add <marketplace>` without `--plugin`, on a catalog that lists several plugins, adds every plugin it lists, each as its own package, or none of them if one cannot be added. It used to stop and ask for `--plugin`.
- Language servers. A plugin's `.lsp.json` or `lspServers` is written into Claude Code's bundle (`.lsp.json`), Grok's home (`lsp.json`) and OpenCode's `opencode.json` (`lsp`); a warning names Codex, Cursor and Antigravity, which have no language server setting. So that the model can use them, Grok's `[features] lsp_tools` is set and `agentpack opencode` sets `OPENCODE_EXPERIMENTAL_LSP_TOOL`, unless the user has set them. A marketplace entry that defines its plugin in the catalog itself (`gopls-lsp`, `pyright-lsp` and the other language server plugins of the official Claude Code marketplace) is installed with the entry as its manifest; it used to be refused.

### Fixed

- A subagent that a package keeps in a subdirectory of `agents/` is staged at the top of `agents/` for Claude Code, Grok, Cursor and Antigravity, which read only that level; Claude Code, Grok and Antigravity listed none of the 33 subagents of alirezarezvani/claude-skills. OpenCode keeps the subdirectory. A second subagent with the same file name keeps its subdirectory, with a warning.
- A fetched directory that turned out not to be a package stayed in the cache.
- A launched agent started in the workspace, but `PWD` still named the directory agentpack was started from; OpenCode took its project from `PWD` and worked in the wrong directory under `--workspace`. `PWD` names the workspace.
- `agentpack probe` accepted only the exact Claude Code, Codex and OpenCode releases it was validated on, so it reported nothing after an agent updated itself (Claude Code 2.1.299). It accepts later releases of the same series; a parser that cannot read one says so.
- A command hook written in Claude Code's exec form (`"command": "node", "args": [...]`, the stripe plugin) lost its arguments and ran the program alone. The arguments are kept and the hook runs without a shell.
- Subagents are also staged as Codex custom agent files (`agents/<name>.toml`), next to the skill fallback.
- A failed `add` left the package in `agentpack.toml` and `pack.lock` although nothing was staged. Both files are now put back as they were.
- Staged hooks ran `agentpack` by bare name, so they used whatever `agentpack` the agent's `PATH` had. They name the executable that staged them.
- Saving `pack.lock` left an `agentpack-<hash>` directory in the system temporary directory for every project, and Codex homes under `/var/tmp/ap-c` were never removed. These directories are marked with their project and removed by a later `sync` once the project is gone. Codex homes go under `AGENTPACK_STAGING_ROOT` when that path is short enough for Codex's control socket.
- A remote MCP server's `headers` and OAuth client settings and a stdio server's `cwd` were dropped, so a server that needs a header did not work. They are written into every agent's configuration that has a setting for them, and a warning names the agent and the setting where there is none. They never reach `pack.lock`.
- Hooks for Antigravity were a copy of the Claude Code file at `hooks/hooks.json`, where Antigravity does not look and in a shape it does not read (`agy plugin validate`: "hooks : skipped (not found)"). They are rendered as `hooks.json` at the plugin root in Antigravity's format, with its events (`SessionStart`, `PreToolUse`, `PostToolUse`, `PreInvocation`, `Stop`); a hook for an event it lacks is skipped with a warning.
- The Claude probe's "plugin inventory unreadable" finding names the entry that is listed twice (a command and a skill that a package publishes under one name).
- Hooks for Codex now cover its current events: `PermissionRequest` (natively, no longer as `PreToolUse`), `PreCompact`, `PostCompact`, `SubagentStart`, `SubagentStop`, `SessionEnd`.
- How a hook was rendered was printed as a `warning:` line, also with `--quiet`. It is an information line that `--quiet` omits.
- `README.md` and similar documentation files in `commands/`, `agents/` and `rules/` were staged as artifacts named `README`.
- The Cursor launch home merged the pack's `mcp.json` with yours through a fixed set of fields, dropping any other field of your own servers. Servers are merged as written.
- A plugin's `.mcp.json` written as a bare `name → server` map (the official playwright, firebase, gitlab and laravel-boost plugins) was ignored without a message. Both forms are read.
- MCP servers defined inline in `plugin.json` (`mcpServers`), or in a file it names such as `.codex-plugin/mcp.json`, were not read. All three vendors' locations are read; a server or location that cannot be read is skipped with a warning.
- An unknown hook event (`PostToolUseFailure`, Cursor's `afterAgentResponse`) failed the whole package after the lock was written. The event lists follow the current Claude Code and Cursor references, Cursor hook files are parsed in their own format and stay native on Cursor, hooks are read from the manifests' `hooks` fields too, and each agent gets the hooks published for it. A hook the target has no event for is skipped with a warning.
- A command with frontmatter that is not strict YAML (`argument-hint: [system] [--source <path>]`) was skipped. Such frontmatter is read key by key.
- `add` of a marketplace entry that only configures a language server (`gopls-lsp`, `pyright-lsp`, …) failed with `Invalid cache layout`. The error now says what the entry is and why nothing is installed.
- A manifest's `skills`, `commands`, `agents` and `rules` paths were not followed, and symbolic links in a GitHub archive became empty files, so `alirezarezvani/claude-skills` staged none of its 372 skills. Declared paths and in-package links are followed; a path or link that leaves the package is skipped with a warning.
- A Claude subagent's `tools` list, named `color` or model alias made OpenCode refuse its whole configuration. They are converted to the forms OpenCode accepts.
- `${CLAUDE_PLUGIN_ROOT}` in an MCP server definition reached five agents as literal text, and named the wrong directory for Claude Code. Each such package gets a staged copy of itself, and every harness's config names it.
- `add` did not record the added package's MCP servers in `pack.lock`.
- On Codex, a command or agent with the name of a skill of the same package was overwritten by it. It is staged as `<name>-command` / `<name>-agent`.
- `preflight` and `probe` planned artifacts with their own walk of the cache; they now use the files and names staging uses.

### Changed

- A `content_hash` for a package that contains symbolic links differs from the one 0.4.8 recorded (links are links, not empty files). Lock entries written by 0.4.8 and earlier still verify and keep their hash; an entry locked from now on records the links.
- One hook, server or artifact that cannot be staged no longer fails the package: it is skipped with a warning that names it.

## [0.4.8] - 2026-10-12

### Added

- Session observation covers slash commands and subagents, not only skills. Claude Code 2.1.296 lists them in the same init event (`slash_commands`, `agents`); a packaged one is listed as `agentpack-bundle:<name>` and a project one under the bare name. For Codex, where commands and subagents are rendered as skills, they are observed in the skill listing and reported under their own category. A contract can now require `"category": "command"` or `"agent"` with `"scope": "workspace_session"`.

## [0.4.7] - 2026-10-12

### Fixed

- The hooks plugin staged for OpenCode crashed on OpenCode 1.17.11 (`proc.stdin.getWriter is not a function`), so every prompt failed once a package carried a prompt hook. The payload is now passed to `hook-exec` as bytes.
- The Claude probe no longer gives up when a very large bundle abbreviates its `plugin details` inventory; it records the session catalog instead.

## [0.4.6] - 2026-10-12

### Added

- `probe --agent opencode` for OpenCode 1.17.11. OpenCode lists one skill per name with its file location, and with two same-named skills the listed one differs between runs in a few percent of them. The probe therefore samples 60 listings and records the observed `winner` as `package`, `ambient` or `unstable`.

## [0.4.5] - 2026-10-12

### Fixed

- The workspace digest covers the project's `.codex/skills` and `.opencode/skills`. A skill added there changes what Codex or OpenCode lists, but did not retire an earlier probe receipt.

## [0.4.4] - 2026-10-12

### Fixed

- 0.4.3 reported every skill a project keeps in its own `.agents/skills` as having a packaged skill's name, and so rejected clean workspaces under CI policy. Only a project skill that carries a locked skill's name is reported.

## [0.4.3] - 2026-10-12

### Added

- Session observation (`scope: "workspace_session"`). `probe` now also records what a session started in the checkout lists, with the staged configuration and an empty user home, and no model request. Claude Code 2.1.296 is read from the print-mode init event; Codex 0.159.2 and 0.159.3 from `debug prompt-input`. For every packaged skill the receipt holds an observed `presence` and an observed `winner`: `package` when the staged copy alone carries the name, `ambient` when another skill holds the bare name in Claude Code, `ambiguous` when Codex lists several entries with that name.
- Codex 0.159.3 skill discovery is validated; it was version-only before.
- Preflight reports a project skill that has a packaged skill's name in `.agents/skills`, and in `.codex/skills` or `.opencode/skills` for those agents, as `AMBIENT_ARTIFACT`, and marks the planned winner `project`. Before, only `.claude/skills` was scanned, so a same-named `.agents/skills` entry replaced the packaged copy in the Claude bundle without a finding.

### Fixed

- `preflight --receipt latest` in a workspace that has never been probed reports `RECEIPT_MISSING` and leaves required observations unknown (exit 4). It returned an operational error (exit 5).
- The Claude probe accepts a bundle whose skill inventory is empty and a bundle that staging left without a plugin manifest, so a session is still observed after a project skill replaced every packaged one.

### Compatibility

- Probe capability revisions changed; run `probe` again for fresh receipts.

## [0.4.2] - 2026-10-10

### Fixed

- Named artifact contracts match effective mode-filtered artifacts; supported property predicates evaluate explicit evidence. Missing observations and incomplete/stale receipts cannot satisfy required native checks.
- Receipt freshness binds source, lock, workspace/ambient inputs, staged generation, policy and executable bytes. Strict external launches check the contract under their staging lease.
- Explicit external selectors and modes reach restore, export, preflight and probes. Native processes run in the selected workspace; launch caches include workspace identity and inputs.
- CI inheritance findings reflect detected configuration rather than explanatory text. JSON preflight failures remain machine-readable.
- Failed native processes, malformed output, timeouts and capture limits are failures; Unix cancellation terminates the process group.
- Bundle import validates checksums, schemas and definitions before atomic publication; it refuses existing destinations. Export preserves contracts and rejects inline credentials and nonportable local resources.
- Support exports omit arbitrary native prose and values that can contain private data.
- Per-project staging under a shared override, independent Grok mode homes and per-bundle Claude settings prevent configuration cross-contamination. Returned rebuild errors restore prior staging; interrupted rebuilds recover from a validated journal before the next rebuild.

### Added

- Controlled positive native skill discovery for Codex 0.159.2, restored-pack conformance for Claude 2.1.296 and Codex 0.159.2, and explicit observation scopes.
- Offline contribution fixtures and a documented ICSE evaluation/demo workflow with explicit native-evidence boundaries.

### Compatibility

- Re-export old bundles; metadata-less archives are rejected. Old receipts require a new probe.
- `AGENTPACK_STAGING_ROOT` now contains per-definition subdirectories; restage with `env restore` or `sync`. Grok configuration homes are mode-specific; native/shared credentials and history remain separate.
- Claude receives selected settings as inline JSON and leaves `CLAUDE_CONFIG_DIR` unset. The old shared settings file is ignored.
- Returned errors roll back staging; journal recovery on the next rebuild is not a single atomic transaction across all harnesses. Native observations do not guarantee model answers or enforce network isolation.

## [0.4.1] - 2026-10-10

### Added

- **Portable external environments.** `agentpack env init|use|unuse|list|status|export|import|restore` keeps
  `agentpack.toml`/`pack.lock` outside a checkout, with a machine-local binding under
  `$AGENTPACK_HOME/projects/<hash>/environment.toml`. Global `--definition-root` and `--workspace`
  split definition from agent cwd; legacy `--project-root` remains the single-root workflow.
- **`agentpack preflight`.** Pure offline check (no lock/cache/staging/repo writes) with human and
  `--json` findings (`code`/`severity`/`source`/`target`/`remedy`), inheritance notes, MCP pin status,
  hook diagnostics, and planned workspace writes. `--policy local|ci` and `--strict-external`.
  Reports ambient `CONFIG_SHADOWED` / `AMBIENT_ARTIFACT` from user/project skills (generated evidence),
  optional `contract.json` evaluation, `overall_status`, and exit codes `0/2/3/4/5`.
- **`--env`.** Explicit external environment name or path; invalid selectors fail without falling
  through to a binding or project manifest. `env bind` is an alias for `env use`. MCP
  `--env KEY=VALUE` assignments still pass through (values containing `=`).
- **`agentpack probe`.** Explicit native observation for Claude (`plugin list --json` +
  `plugin details`) and Codex (`--version`, skills unknown). Writes receipts under
  `$AGENTPACK_HOME/projects/<hash>/probe-receipts/`; never runs inside pure preflight.
- **`agentpack config compare`.** Diff two probe receipts; unobserved fields stay unknown.
- **`agentpack support export`.** Redacted opt-in diagnostic capsule from a probe receipt.
- **Frozen restore publishes staging.** `env restore` / `RestoreFrozen` rebuilds staging from the
  exact lock without re-resolving pins or rewriting `pack.lock`; missing locked inputs fail hard.
  Staging rebuild takes an exclusive flock; launches hold a shared lock so restore cannot wipe an
  active session's staged tree.
- **Effective-plan winners.** Preflight artifact records mark package/user/project/plugin winners.
- **Contract schema.** `schemas/contract.v1.schema.json` plus valid/invalid examples.
- **`--strict-external`.** Refuses Cursor/Antigravity workspace overlay writes before they happen;
  Claude and OpenCode remain the validated no-checkout-write adapters.

### Changed

- **`pack.lock` is now lockfile version 3** once it holds a content hash or an MCP record. Version 2
  locks still load and still stage, with a warning that their packages are not verified; run
  `agentpack lock` to upgrade. Binaries older than this change cannot read a version 3 lock.
- `sync` and the launchers no longer rewrite `pack.lock` unless what it records changed, never change
  the pins of entries that are already locked, and print one line for every entry they add, drop or
  move because `agentpack.toml` changed. A `pack.lock` that cannot be parsed is now an error for
  every command except `agentpack lock`, which regenerates it with a warning; before, it was
  silently replaced. `[config] disabled_plugins` now survives a re-resolve.
- A cache entry is no longer accepted because `SKILL.md` or a plugin manifest exists. A partly
  written or edited entry stops the command instead of being used.
- The documentation no longer calls `cache_key` a content hash. It is a hash of the package
  identity (repository, path, commit) and names the cache slot; `content_hash` covers the files.
- Reimplemented agentpack as a Go 1.24 CLI while preserving the manifest, v2 lockfile, cache paths,
  six harnesses, modes/TUI, hooks, MCP merge, durable auth/history, fast launch sync, and supervised
  Claude proxy behavior.
- Replaced the legacy packaging pipeline with GoReleaser archives, SHA-256 checksums, GitHub Releases, and a
  Homebrew cask for macOS and Linux.
- GitHub ref/tag and tarball fallbacks now use an embedded Go Git client, so installed binaries do
  not require an external `git` executable when the REST or codeload endpoints fail.

### Added

- **Content hashes in `pack.lock`.** Every package now carries `content_hash`, a `sha256-tree-v1`
  digest of its cached file tree (sorted paths, file kind, bytes). `lock`, `add`, `remove`, and
  `update` record it from freshly fetched bytes.
- **Verification on every use.** `sync`, `sync --verify-only`, and the launchers compare the cache
  with the lock before anything is staged and stop with a non-zero exit on a mismatch, naming the
  package, both digests, and the cache path. Downloads are verified before they enter the cache.
  `agentpack sync --repair` re-fetches the pinned commit, verifies it, and reports what it replaced.
- **MCP servers in `pack.lock`.** `lock` and `mcp add` record each staged MCP server under
  `[[mcp_servers]]` with a launcher kind and a `pinned` / `unpinned` / `unpinnable` status.
  `npx`-style servers are resolved to an exact version and registry integrity, and staged as that
  version in every harness. An unreachable registry fails the lock unless `--allow-unpinned-mcp`
  (`mcp add --allow-unpinned`) records the server as unpinned.
- **Lock entries are checked before use.** A `content_hash` that is present but malformed, and a
  `commit` that does not match its `cache_key`, stop every command before anything is fetched or
  staged, and the lock is left untouched.
- **Staged files are verified.** `sync --verify-only` hashes the staged skills, commands, agents,
  rules, hooks and MCP config against a record written by the last sync and fails naming each
  modified, added or missing file. A plain `sync` reports the changed files it replaces.
- `AGENTPACK_REQUIRE_VERIFIED` refuses a lock that still has packages without a content hash, and
  `AGENTPACK_FULL_VERIFY` hashes the cache on every launch.
- `agentpack extra sync-claude` reconciles a project's `.claude/skills` and `.agents/skills`
  directories so a local skill authored under either one reaches both, since Claude Code only
  discovers project-local skills under `.claude/skills` while dot-agents shares them under
  `.agents/skills`.
- Durable, project-scoped Codex MCP OAuth credentials shared across staging modes.
- Linux, macOS, and Windows Go CI, race detection, compiled CLI integration coverage, and benchmark
  baselines for cache hashing and mode filtering.
- Checksum-verifying shell and PowerShell release installers, preserving the one-command install
  path from the previous release pipeline.

### Fixed

- Existing Rust RedDB metadata indexes are preserved and replaced automatically on first Go use,
  so upgrading does not require users to delete `$AGENTPACK_HOME/cache/db.reddb` manually.

## [0.3.10]

### Fixed
- **Cursor Agent login persistence with current Linux CLI builds.** The staged fake home now links
  the lowercase `$XDG_CONFIG_HOME/cursor` profile used for `auth.json`, in addition to Cursor's
  uppercase Electron profile, so browser login is shared across Agentpack projects and survives
  staging rebuilds.

## [0.3.8]

### Fixed
- **Cursor Agent login persistence on Linux.** `agentpack agent` now bridges mutable Cursor
  auth/session files and platform profile directories to durable real-profile paths even before
  they exist, so a first login performed inside the staged fake home survives later staging
  rebuilds.

## [0.3.7]

### Added
- **Claude proxy diagnostics.** `agentpack --proxy claude` now writes per-request JSONL diagnostics under `$AGENTPACK_HOME/projects/<project-hash>/proxy-logs` by default, with `AGENTPACK_PROXY_LOG_DIR` for custom locations and opt-in payload snippets via `AGENTPACK_PROXY_LOG_PAYLOADS=1`.
- **Proxy log analysis skill.** The repository now includes a local `.agents` skill that summarizes proxy logs and flags stalled requests, WebSocket setup failures, auth refresh issues, upstream HTTP errors, translation failures, and downstream disconnects.

### Changed
- **Proxy launch tracing.** Claude proxy launch now passes the resolved project-state log directory into the proxy supervisor so diagnostics follow the same per-project storage layout as other agentpack state.

## [0.3.6]

### Fixed
- **Codex first-login preservation.** Staged `CODEX_HOME/auth.json` now always points at the shared
  agentpack auth file, even before that file exists, so the first Codex login in a staged home is
  kept for later launches. Rebuilds also preserve legacy regular staged `auth.json` files before the
  Codex staging root is reset.

## [0.3.3]

### Security
- **Archive path-traversal guard.** Tarball extraction now rejects any entry whose path contains
  `..`, an absolute root, or a drive prefix instead of joining it onto the destination, so a
  hand-crafted ("zip slip") archive can no longer write outside the content-addressed cache. GitHub
  git trees can't produce such entries, but agentpack extracts untrusted third-party archives, so
  the check is enforced regardless (`internal/github/archive.go`).

### Fixed
- **`add` with the canonical module-id form.** `agentpack add github.com/<owner>/<repo>/<path>` (the
  exact shape shown in docs, the manifest, and `pack.lock`) previously treated `github.com` as the
  owner and 404'd. The leading host segment is now stripped, so both that form and the bare
  `<owner>/<repo>/<path>` form resolve.
- **Case-sensitive in-repo paths.** `ModuleId::parse` lowercased the whole id, breaking `lock`/`sync`
  for any dependency whose in-repo path had uppercase letters (e.g. `.../PDF-Tools`). Only the
  `github.com/<owner>/<repo>` prefix is lowercased now; the path is preserved verbatim.
- **`add <owner>/<repo>/<path>@<ref>`.** A shorthand `@ref` was silently folded into the path
  segment (wrong fetch). It is now parsed, used to fetch the requested revision, and persisted into
  `agentpack.toml` so `lock`/`sync` re-resolve the same pin.
- **`lock --update` / `sync --update-lock` now bypass the GitHub metadata cache.** Floating pins no
  longer fail to advance within the ref/tag freshness window; the cached value is still used as a
  stale fallback when the network is unavailable.
- **Claude MCP servers marked `disabled` are dropped** from the staged `.mcp.json` (Claude's schema
  has no `disabled` field, so they were being launched anyway).
- **MCP pre-approval no longer depends on attribution.** With `AGENTPACK_KEEP_ATTRIBUTION=1`, the
  `enabledMcpjsonServers` allowlist is still written (the `--settings` overlay is created on demand),
  so Claude no longer drops staged MCP servers as untrusted.
- **A malformed markdown frontmatter in a pack no longer aborts `sync`.** The offending file is
  logged and skipped instead of failing staging for every harness.
- **dot-agents `agents/` and `commands/` now reach Codex.** They are rendered as Codex skills via the
  artifact pipeline (Codex only reads `skills/`), matching the documented behavior; the Claude bundle
  continues to receive them natively.
- **Frontmatter with a leading UTF-8 BOM** is parsed instead of being treated as body.
- **Atomic write for the shared Codex auth file** (`$AGENTPACK_HOME/shared/codex/auth.json`) — write
  to a per-process temp file then rename, avoiding a torn read when two launches materialize it
  concurrently.
- **`mcp remove <name>`** now errors when no such server exists instead of reporting a false success.
- **`mcp add --args`** accepts values that start with `-` (e.g. `--args -y pkg`) without requiring
  `--args=-y`.
- **Ambiguous single-segment `remove`** now errors and asks for a fuller `owner/repo/path` instead of
  removing an arbitrary match.
- **`pack.lock` is validated on load** — an unsupported `lockfile-version` is rejected with a clear
  message instead of being silently accepted.
- **Monorepo workspace overlays.** The launch fast-path digest now includes the resolved workspace
  directory for Cursor (`agent`) and Antigravity (`agy`), so `cd`-ing to a sibling subdirectory
  re-creates the `.cursor/agents` / `.agents/plugins/agentpack-bundle` overlay instead of skipping it.
- Mode TUI: neutral `package:` rows now render the correct base-derived glyph.
- Cursor (Linux): an empty `XDG_CONFIG_HOME` / `XDG_DATA_HOME` no longer mislocates the staged
  Electron-profile symlinks.

### Changed
- Internal: centralized the `github.com` host and default `HEAD` ref as `GITHUB_HOST` /
  `DEFAULT_GIT_REF` constants instead of scattered string literals.
- Docs: harness notes for Grok and Antigravity re-verified against `grok 0.2.14` and `agy 1.0.3`
  (assumptions unchanged; launcher behavior is identical).
