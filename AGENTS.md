# agentpack

`agentpack` is a Rust CLI that pins **GitHub-hosted skills** and **plugin directories** (`.claude-plugin` and/or `.cursor-plugin`) for a project.

**Source of truth for what to install** is **`agentpack.toml`** at the repo root (direct dependencies, project-local modes, and MCP settings). **`pack.lock`** (v2) lists every resolved **package** (direct and transitive from nested `agentpack.toml` files inside dependencies) with pinned commits and `cache_key`s. Both files live in the **project repo**.

All downloaded trees, the RedDB index, and your **`local/`** mirror live under a **user-wide agentpack home** (see below)—not under a repo-local `.agentpack/` directory. Staging for harnesses still uses a **per-project temp** directory (or **`AGENTPACK_STAGING_ROOT`**).

### Pre-release

**No backwards compatibility.** `agentpack` is pre-release: CLI behavior, lockfile shape, staging layout, env vars, and defaults may change without a migration period or deprecation window. Assume **breaking changes** between versions until a stable release is declared.

### Code structure (contributor rule)

**Per-harness code lives in `src/harness/<name>/`. Shared code lives in its subsystem. No re-export shims, no orphaned glue. No single-file folders except the uniform harness folders, no <40-line orphan modules.**

- **One folder per harness, uniformly.** Each coding agent is a folder **`src/harness/<name>/`** with `mod.rs` (+ submodules as needed) implementing the `Harness` trait — even small harnesses (`grok/`, `agy/`) are folders, so the layout is identical for all six. **Everything used by only that one harness lives there** — seed, attribution writer, MCP-config writer (including codex/grok's own copy of the `[mcp_servers]` TOML writer), hook renderer + support fn, credential bridging (`codex/auth.rs`), fake-home (`cursor/fake_home.rs`), workspace overlays, and `launch_command`. To understand or add a harness you read/create **one folder**, nothing else. The only files at `harness/` root are the shared harness *system*: `mod.rs` (trait + registry + contexts), `target.rs` (`HarnessTarget`), `launch.rs` (launch dispatch).
- **Shared infrastructure stays in its subsystem and never holds per-harness logic:** `staging/` = cross-harness staging passes (`pack_overlay`, `collision`, `guidance` collection, `dot_agents`, `mcp` collect+merge, `pipeline`) plus the shared `keep_attribution`/`NO_ATTRIBUTION_BODY` primitives; `hooks/` = the hook engine (`ir`, `collect`, `parse`, `stage`, `render` trait + `SupportLevel`, `runtime/{bridge,dispatch,handlers,output}`); `artifacts/` = artifact rendering; `fs_util.rs` = generic file-tree helpers (`copy_selected_entries`, `copy_merge_tree`). A file named after a harness (`cursor.rs`, `parse/claude.rs`) sitting in a shared subsystem is a smell — colocate it or, if it's actually a shared format adapter, name it for the format not the harness.
- **No single-file folders, no tiny orphans.** A module is a flat `foo.rs` unless it has real internal structure (the uniform harness folders are the one deliberate exception). A <40-line helper used by ≥2 modules lives *in* its natural home (its trait file, its caller, or `fs_util`), not in a file of its own. API-surface `pub use` in a subsystem `mod.rs` (e.g. `sync`, `github`) is a module boundary, not a shim — those stay.
- **One canonical home per type; no re-export shims.** `HarnessTarget` is defined only in `src/harness/target.rs` and imported everywhere as **`crate::harness::HarnessTarget`** — there are no `pub use …::HarnessTarget` chains through `artifacts`/`staging`/`sync`/`hooks::ir`. Don't add a re-export to "keep an old path resolving"; repoint the callers.
- **No orphaned glue modules.** Launch dispatch + shared launch helpers live in `harness/launch.rs` (called via `harness::launch`), not a separate top-level `launcher/`. If a module exists only to forward to another, delete it and inline the call.
- The trait owns *per-harness divergence*; genuinely cross-harness passes that take "all roots at once" (`pack_overlay`, `guidance`, `collision`) stay shared loops. Adding a 7th harness should mean: one new `src/harness/<name>/` + one line in `harness::all()` + one `HarnessTarget` variant + one `cli` subcommand.

### User data layout (`AGENTPACK_HOME`)

| Path | Purpose |
| --- | --- |
| **`$AGENTPACK_HOME/cache/<cache_key>/`** | Content-addressed package trees (GitHub tarball, or **copies** from filesystem / `local/`). |
| **`$AGENTPACK_HOME/cache/db.reddb`** | Metadata + alias map for fast repeat **`add`**, plus cached GitHub ref/tag lookups to reduce API calls. |
| **`$AGENTPACK_HOME/local/<owner>/<repo>/…`** | Optional offline mirror; same slash layout as **`owner/repo/…`** specs. |
| **`$AGENTPACK_HOME/projects/<hash>/cursor-overlay.manifest`** | Per-project Cursor overlay bookkeeping (not stored in the repo). |
| **`$AGENTPACK_HOME/projects/<hash>/agy-overlay.manifest`** | Per-project Antigravity workspace plugin overlay bookkeeping (not stored in the repo). |
| **`$AGENTPACK_HOME/shared/codex/auth.json`** | Shared Codex auth cache used by staged **`CODEX_HOME`** trees when the real user config stores credentials in the OS keychain instead of **`~/.codex/auth.json`**. |
| **`$AGENTPACK_HOME/claude-settings.json`** | Stable attribution-off overlay passed to Claude as **`--settings <path>`**. Project-independent so Claude Code's keychain credential namespace stays user-global (see "Claude attribution" below). |

**Default `AGENTPACK_HOME`:** if unset, **Windows** uses **`%LOCALAPPDATA%\agentpack`**; **Unix** uses **`$XDG_DATA_HOME/agentpack`** when **`XDG_DATA_HOME`** is set, otherwise **`$HOME/.local/share/agentpack`**.

### Module IDs

Dependency keys and lockfile **`module`** fields use a **Go-style path** (lowercase):

- **`github.com/<owner>/<repo>`** — repository root (`path` empty).
- **`github.com/<owner>/<repo>/<p1>/<p2>/...`** — subdirectory package inside the repo.

Optional **`@ref`** may appear in human input; identity and `cache_key` always use the **resolved commit SHA**, not branch or tag names.

### `agentpack.toml`

| Section | Role |
| --- | --- |
| **`[dependencies]`** | Direct dependencies only. Each key is a **module id**; values are **`""`**, a **short string** (branch/tag/ref), or a **table** (`branch`, `tag`, `commit`, `version` for semver against tags, etc.). |
| **`[modes.<name>]`** | Project-local staging presets. Use **`base = "all" | "none"`** plus **`enable = [...]`** / **`disable = [...]`** selectors such as **`package:...`**, **`package-path:...:...`**, **`mcp:...`**, and **`.agents:...`**. |
| **`[mcp.servers.<name>]`** | Project-level MCP server definitions. Each key under **`[mcp.servers]`** is a server name; values are tables with **`command`**, **`args`** (string array), **`env`** (string map), and optional **`disabled`** (bool). Merged with plugin `mcp.json` files and **`.agents/mcp.json`** during **`sync`**, then written to every harness staging directory. |

Transitive dependencies come **only** from a **`agentpack.toml`** (dependencies table) **inside** an fetched package cache root. There is no implicit scratchpad: **`add`** edits the project manifest; **`lock`** / **`sync`** (when dependencies are non-empty) recompute **`pack.lock`**.

### Golden rules for **`add <spec>`**

Resolution order (network/local):

1. **`https://github.com/…`** — tree or blob URL; the **directory** containing **`SKILL.md`** or a plugin manifest is fetched; the module id is derived from **owner / repo / in-repo path**.
2. **`owner/repo`** — tries **`$AGENTPACK_HOME/local/<owner>/<repo>`** first (copy); else **GitHub** at **repo root**.
3. **`owner/repo/p1/p2/...`** — tries **`local/…/full/slash/spec`** first; else **GitHub** with in-repo path **`p1/p2/...`**.
4. **Single segment** **`name`** — **`local/<name>`** only, or **alias** in RedDB to reuse a **`cache_key`** without network.

Repeat **`owner/repo`** and **`owner/repo/path`** adds also consult the RedDB alias/index after checking **`local/`**, so previously fetched GitHub packages are reused before any new GitHub request is made.

5. **Filesystem path** (`./rel/dir`, `/abs/dir`) — the directory is copied to cache; an entry like **`name = { path = "rel/path" }`** is written to **`agentpack.toml`** where **`name`** is the directory basename and the path is relative to the project root. On **`lock`** / **`sync`**, path deps are always re-copied from source (content hash detects changes). **`sync`** will error on other machines if the path is missing and the cache slot is empty.

Duplicate content for the same **`owner` / `repo` / in-repo `path` / commit** hits the same **`cache_key`**. Plugins may expose **`.claude-plugin`**, **`.cursor-plugin`**, or both; layouts are normalized after fetch.

### Lockfile v2 and **`sync`**

- **`pack.lock`** with **`lockfile-version = 2`** stores **`[[packages]]`** only. Legacy **`[[skills]]`** / **`[[plugins]]`** sections are rejected. In-memory **`skills`** / **`plugins`** are derived views rebuilt from canonical packages after load.
- **`sync`** refreshes **`pack.lock`** from **`agentpack.toml`** only when **`[dependencies]`** is **non-empty**. With an **empty** dependency table, **`sync`** treats the existing lock as authoritative (manual edits, tests, or hybrid workflows).
- Run **`agentpack lock`** to force a full resolve from the manifest (requires **`agentpack.toml`**).
- Harness launchers (**`agentpack claude`**, **`opencode`**, **`codex`**, **`agent`**) run a **fast pre-sync** when **`agentpack.toml`**, **`pack.lock`**, and **`./.agents/`** are unchanged since the last successful launch sync: they verify cache + staging integrity and **skip** full lock resolve, re-download, and staging rebuild. Floating pins (branch / floating semver) therefore **do not advance** on launch alone — run **`agentpack sync`** or **`agentpack lock`** when you need **`pack.lock`** refreshed from the manifest.
- GitHub **ref → commit** and **tag list** lookups are cached in **`db.reddb`** and reused across **`add`**, **`lock`**, and **`sync`**. Fresh cached metadata avoids repeat API calls; exact tag-name ref lookups also reuse the cached tag list directly.
- When GitHub REST ref/tag lookups fail, agentpack falls back to the Git protocol via embedded **`gix`** `ls-refs` against **`https://github.com/<owner>/<repo>.git`** before using stale cached metadata. This removes the hard dependency on the throttled REST API for ref and tag resolution.

## Harness launch research summary

Claude Code layers configuration by **scope** (see [settings docs](https://code.claude.com/docs/en/settings)):

| Scope | Typical locations |
| --- | --- |
| **User** | **`~/.claude/settings.json`**, **`~/.claude.json`** (plus preferences, OAuth, MCP entries, and per-project UI state in the latter) |
| **Project** | **`.claude/settings.json`**, `.claude/settings.local.json`, **`CLAUDE.md`**, **`.mcp.json`** (MCP in project) |

Filesystem assets from the user profile (still loaded by Claude from **`$HOME`**):

- **`~/.claude/commands/`**, **`agents/`**, **`skills/`**, **`hooks/`**, etc.

Claude reads those directories **from home** for normal user scope. **`agentpack` does not copy** those trees into the staging bundle, so you do not get duplicate slash commands (e.g. `/code-tutor` and `/agentpack-bundle:code-tutor` for the same skill).

**`--plugin-dir`** is **additive** (see [plugins](https://code.claude.com/docs/en/plugins)).

**Precedence** for **settings** scopes: managed and CLI beat **local → project → user**. Copying user JSON into the bundle may affect how Claude treats **project-scoped** files **inside** that plugin path; your originals under **`~`** still exist.

OpenCode uses a **config root override** instead of an additive plugin dir. Official docs describe **`OPENCODE_CONFIG_DIR`** as a custom directory searched like the standard **`.opencode`** / **`~/.config/opencode`** root, with config in **`opencode.json`** and global assets under **`agents/`**, **`commands/`**, **`plugins/`**, and **`skills/`**.

Codex uses a **home root override** instead of an additive plugin dir. Official docs and source use **`CODEX_HOME`** for the user config root (default **`~/.codex`**) and still auto-discover user skills from **`$CODEX_HOME/skills`**. Codex plugin marketplaces are repo- or home-rooted **`.agents/plugins/marketplace.json`** files, so this is **not** equivalent to Claude’s additive **`--plugin-dir`** model.

Grok uses a **home root override** via **`GROK_HOME`**. The installed `grok 0.2.14` rejects **`--plugin-dir`** and **`--settings`**, even after login, so agentpack stages **`$STAGING/grok-home`**, writes pack plugin paths into staged **`config.toml`** under **`[plugins].paths`**, and launches with **`GROK_HOME=$STAGING/grok-home`**. Grok still reads Claude-compatible user sources from the real **`~/.claude`**, so duplicate handling must account for both **`~/.grok`** and **`~/.claude`**.

**`agentpack agent`** runs the Cursor CLI with **`HOME=$STAGING/cursor-home`**. **`$HOME/.cursor/commands`** (etc.) symlink into the staged **`pack.lock`** tree. **`agentpack`** also sets **`CURSOR_CONFIG_DIR=$HOME/.cursor`** on the child process. Cursor workspace trust still uses **`CURSOR_DATA_DIR`**; when it is unset, **`agentpack`** points it at real **`~/.cursor`** so trust state survives staging rebuilds. To keep shell tools working inside the staged HOME, agentpack also bridges **`CARGO_HOME=~/.cargo`**, **`RUSTUP_HOME=~/.rustup`**, and **`DOCKER_CONFIG=~/.docker`** unless the user already set them.

The Cursor CLI also reads workspace **`.cursor/`** for some features; behavior may combine configured **`CURSOR_CONFIG_DIR`**, **`--workspace`**, and **`CURSOR_DATA_DIR`** (workspace trust / projects).

Antigravity (`agy`) has no config-root override in the installed CLI and shares auth/settings with the desktop app under the user's real profile. agentpack therefore leaves **`HOME`** and **`~/.gemini`** untouched and exposes pack content as a workspace plugin: **`./.agents/plugins/agentpack-bundle`** symlinks to **`$STAGING/agy/agentpack-bundle`**. Add **`.agents/plugins/agentpack-bundle`** to **`.gitignore`** if you do not want the symlink in version control.

## What agentpack does

**Isolation (like **`venv` / `uv`**) —** Pin resolution from **`agentpack.toml`** stays under **`$AGENTPACK_HOME`** and ephemeral **`$STAGING`**. **`agentpack` does not copy pack trees into your git workspace** (no materialized commands/skills/rules under **`./.cursor/`** for pack content) **and does not symlink pack agents into your real `~/.cursor` or `~/.claude`** — that would leak project-specific pins into global userspace.

**Cursor `agent` subagents —** The bundled **`agent`** CLI resolves [subagents](https://cursor.com/docs/subagents) from **`resolve(--workspace)/.cursor/agents`** only (not from fake **`$HOME/.cursor/agents`** for that list). **Only `agentpack agent`** creates **`./.cursor/agents`** as a **symlink** into **`$STAGING/cursor/agentpack-bundle/agents`** (when the pack exposes agent markdown) and records the path in **`cursor-overlay.manifest`** under **`$AGENTPACK_HOME/projects/<hash>/`**. Bare **`agentpack sync`** and other launchers (**`claude`**, **`opencode`**, **`codex`**, **`grok`**, **`agy`**) leave the workspace alone and clean up any prior **`./.cursor/agents`** symlink from a previous **`agent`** run. If **`./.cursor/agents`** already exists as a **directory** or **file**, **`agentpack agent`** leaves it alone and logs a warning. Add **`./.cursor/agents`** (or **`.cursor/agents`**) to **`.gitignore`** if you do not want the symlink in version control.

**Project `./.agents/` ([dot-agents](https://github.com/dot-agents/dot-agents)-style)** — Optional. After pack content is staged, **`sync`** merges **`./.agents/`** into harness trees under **`$STAGING`** that do **not** natively read the directory: **Claude bundle** and **Codex home**. Shared **`rules/**/*.mdc`** (hard-linked into staged **`rules/`** when possible), **`skills/`**, **`agents/`**, **`commands/`**, **`hooks/`**, top-level **`AGENTS.md`** (Codex), **`CLAUDE.md`** (Claude bundle), **`mcp.json`**, and optional subtrees **`claude/`**, **`codex/`** that mirror each harness layout. **Cursor, OpenCode, and Antigravity are excluded** — they natively discover workspace-scoped customization, and merging into their staging would duplicate content. The workspace pointers agentpack may add are **`./.cursor/agents`** (only when launching via **`agentpack agent`**) for Cursor subagents and **`./.agents/plugins/agentpack-bundle`** (only when launching via **`agentpack agy`**) for Antigravity pack content. Bare **`agentpack sync`** and the other harness launchers do not write either symlink and clean up stale ones from a previous run. Set **`AGENTPACK_DOT_AGENTS=0`** to skip dot-agents merging.

1. **Cache** — **`add`**, **`lock`**, and **`sync`** populate **`$AGENTPACK_HOME/cache/<cache_key>/`**.  
2. **Index** — **`$AGENTPACK_HOME/cache/db.reddb`** stores metadata (`kind`: `skill` | `plugin`) and shorthand **aliases** → `cache_key`.  
3. **Artifact conversion** — **`sync`** parses supported markdown artifacts from cached pack content and re-renders them per target harness instead of copying frontmatter blindly:
   - **Commands**: Cursor plain markdown, OpenCode markdown frontmatter, Claude command/skill frontmatter, Codex skill fallback.
   - **Agents**: Claude / OpenCode / Cursor agent markdown frontmatter, Codex skill fallback.
   - **Skills**: skill frontmatter normalized and rendered as target skills.
   - **Rules**: Cursor and Antigravity rules are preserved as rules; other harnesses get a best-effort skill fallback with original rule scope noted when the target lacks first-class rule files.
4. **Claude bundle** — **`sync`** rebuilds **`$STAGING/plugins/agentpack-bundle/`** with **`.claude-plugin/plugin.json`** and:
   - **Packages:** target-specific converted markdown artifacts plus raw Claude support dirs (`hooks`, `matchers`, `core`, `examples`, `utils`), filtered through the selected **mode**.
   - **MCP:** merged `mcp.json` written to bundle root (see MCP merge below).
4a. **Claude attribution overlay** — **`agentpack` does not redirect `CLAUDE_CONFIG_DIR`**. Claude Code (verified against the v2.1.119 bundle) namespaces both the macOS keychain service name (`Claude Code-credentials-<sha256(CLAUDE_CONFIG_DIR)[:8]>`) and the file fallback (`$CLAUDE_CONFIG_DIR/.credentials.json`) by `CLAUDE_CONFIG_DIR`. Pointing it at per-project staging would forget login on every project switch, mode switch, and macOS reboot. Instead, **`sync`** writes **`$AGENTPACK_HOME/claude-settings.json`** with attribution forced off, and the launcher passes **`claude --settings <path>`** which loads as **`flagSettings`** scope (precedence above `user` / `project` / `local`). Claude continues to read the user's real **`~/.claude.json`**, **`~/.claude/settings.json`**, and the user-global keychain entry.
5. **OpenCode root** — **`sync`** rebuilds **`$STAGING/opencode/`**:
   - **Optional:** seeds from **`~/.config/opencode/`** (`opencode.json`, `agents`, `commands`, `modes`, `plugins`, `skills`) so provider/auth config still works when **`OPENCODE_CONFIG_DIR`** is redirected.
   - **Overlay:** converted pack commands / agents / skills / rules written into OpenCode’s supported markdown locations.
   - **MCP:** merged `mcp.json` written to config root (see MCP merge below).
6. **Codex home** — **`sync`** rebuilds **`$STAGING/codex-home/`**:
   - **Optional:** seeds from **`~/.codex/`** (`config.toml`, `skills`, `themes`) so user config still works when **`CODEX_HOME`** is redirected. The Codex CLI stores OAuth/API material in **`auth.json`** or in the OS keychain keyed by the **canonical `CODEX_HOME` path**; a staged path would otherwise miss keychain entries. agentpack therefore links each staged **`$STAGING/codex-home/auth.json`** to a **shared source** instead of copying credentials per project: it uses **`~/.codex/auth.json`** when that file already exists, otherwise it materializes the real **`~/.codex`** keychain entry (service **`Codex Auth`**) into **`$AGENTPACK_HOME/shared/codex/auth.json`** and links staged homes there. The staged **`config.toml`** is forced to **`cli_auth_credentials_store = "file"`** so every project shares refresh-token updates through the same file.
   - **Overlay:** portable pack content is rendered into Codex **skills** under **`$STAGING/codex-home/skills/`**. Full Claude plugins are **not** translated into Codex plugin marketplaces.
   - **MCP:** merged `mcp.json` written to Codex home (see MCP merge below).
7. **Cursor staging** — **`sync`** rebuilds **`$STAGING/cursor/`** as a [Cursor plugins](https://cursor.com/docs/reference/plugins) layout, then builds **`$STAGING/cursor-home/`** as a **fake `HOME`** for the CLI:
   - **Plugin / pack tree:** **`$STAGING/cursor/.cursor-plugin/marketplace.json`**, **`$STAGING/cursor/agentpack-bundle/.cursor-plugin/plugin.json`**, plus **`commands/`**, **`agents/`**, **`skills/`**, **`rules/`**, **`hooks/`**, **`assets/`**, **`scripts/`**, **`mcp.json`** when present.
   - **Fake `HOME`:** **`$STAGING/cursor-home/.cursor/`** symlinks pack dirs and (when present on disk) **`cli-config.json`**, **`machineid`**, **`agent-cli-state.json`**, **`argv.json`**, **`ide_state.json`**, **`mcp.json`**, **`User/globalStorage`**. **macOS:** symlink **`~/Library/Application Support/Cursor`** → **`$HOME/Library/.../Cursor`**. **Linux:** **`~/.config/Cursor`** and **`~/.local/share/Cursor`**. **Windows:** **`%USERPROFILE%\\AppData\\Roaming\\Cursor`**.
   - **Optional:** copies **`cli-config.json`** and **`mcp.json`** from **`~/.cursor/`** into **`$STAGING/cursor/`** when user-settings seeding is enabled. User **`agents/`**, **`commands`**, and similar are not merged from **`~/.cursor`** into the pack tree.
   - **Workspace subagents symlink:** **`./.cursor/agents`** → staged pack **`agents/`** (created **only by `agentpack agent`**; other launchers and bare **`sync`** clean up any stale symlink). **`cursor-overlay.manifest`** tracks agentpack-owned overlay paths for safe cleanup (symlinks/files only — never deletes a real directory).
   - **Migration:** older **`cursor-overlay.manifest`** entries under **`$AGENTPACK_HOME/projects/<hash>/`** are still removed at the start of **`sync`** when present.
8. **Grok home** — **`sync`** rebuilds **`$STAGING/grok-home/`** and **`$STAGING/grok/agentpack-bundle/`**:
   - **Optional:** seeds from **`~/.grok/`** (`config.toml`, `skills`, `agents`, `commands`, `plugins`) so user Grok config still works when **`GROK_HOME`** is redirected.
   - **Auth:** links real **`~/.grok/auth.json`** and **`mcp_credentials.json`** when present so login survives staging rebuilds.
   - **Overlay:** converted pack content is rendered into **`$STAGING/grok/agentpack-bundle`** with root **`plugin.json`**, and staged **`config.toml`** gets **`[plugins].paths`** pointing at that bundle.
   - **MCP:** merged MCP is written as Grok-native **`[mcp_servers]`** TOML in staged **`config.toml`**.
   - **Hooks:** not staged — and empirically *cannot* be, in isolation. Grok 0.2.14's `hooks.json` is Claude-compatible (native tool-name matcher), but `grok inspect --json` confirms it reads global hooks from the **real `~/.grok/hooks`** (hardcoded — a redirected **`GROK_HOME`** is honored for `config.toml`/MCP but **not** for hook discovery); `[plugins].paths` bundle hooks load `enabled: false` (need interactive trust), and the only alternative — `grok plugin install --trust` — writes into the user's real `~/.grok/installed-plugins`, which **agentpack never runs**. Since agentpack only writes to its staging dir and never touches the real `~/.grok`, no isolated, headless path to load grok hooks exists; `hook_support` stays `Unsupported`.
9. **Antigravity staging** — **`sync`** rebuilds **`$STAGING/agy/agentpack-bundle/`** with root **`plugin.json`**, plus **`skills/`**, **`agents/`**, **`commands/`**, **`rules/`**, **`hooks/`**, and **`mcp_config.json`** when present.
   - **Workspace plugin symlink:** **`./.agents/plugins/agentpack-bundle`** → staged pack plugin (created **only by `agentpack agy`**; other launchers and bare **`sync`** clean up any stale symlink). **`agy-overlay.manifest`** tracks agentpack-owned overlay paths for safe cleanup (symlinks/files only — never deletes a real directory).
   - **Hooks:** not staged — and empirically *cannot* be, in isolation. `agy` has a real hook engine and `agy plugin validate` statically accepts a Claude-shape `hooks.json` (root `plugin.json` + `hooks/hooks.json`), but it does **not** auto-load hooks from the workspace `.agents/plugins/<name>/` overlay (a live `agy --print` session never fired a `SessionStart` hook; the binary documents `.agents/` as *agent metadata*, not a plugin source). agy has **no config-root redirect**, and the only way to load hooks would be `agy plugin import/install`, which writes into the user's real `~/.gemini`. **agentpack never runs those commands and never writes to `~/.gemini` or the workspace** — it deliberately leaves them untouched, which is exactly why no isolated, side-effect-free path to load agy hooks exists. `hook_support` therefore stays `Unsupported`. *(Re-verified on `agy 1.0.3`: `--add-dir`/`--dangerously-skip-permissions`/`agy plugin validate` unchanged.)*
10. **Launchers**
   - **`agentpack claude`** runs **`claude`** with **`--plugin-dir`** pointing at **`agentpack-bundle`** and (when attribution is forced off) **`--settings $AGENTPACK_HOME/claude-settings.json`**. **`CLAUDE_CONFIG_DIR`** is **never** set so the user's keychain login (`Claude Code-credentials`) is reused as-is across all projects.
   - **`agentpack opencode`** runs **`opencode`** with **`OPENCODE_CONFIG_DIR=$STAGING/opencode`**.
   - **`agentpack codex`** runs **`codex`** with **`CODEX_HOME=$STAGING/codex-home`**.
   - **`agentpack grok`** runs **`grok`** with **`GROK_HOME=$STAGING/grok-home`** and injects **`--cwd <project-root>`** when absent. It does **not** pass **`--plugin-dir`** or **`--settings`** for `grok 0.2.14`.
   - **`agentpack agent`** runs Cursor Agent with **`HOME=$STAGING/cursor-home`**. **`--workspace`** defaults to the **current working directory** (where you invoked `agentpack`) — *not* the pack root (`project_root`), which in a monorepo may be a parent where `agentpack.toml` lives above the subproject you `cd`'d into. The workspace **`./.cursor/agents`** subagent overlay and Cursor's workspace-trust follow this same CWD; pass **`--workspace <path>`** to override. **`CURSOR_CONFIG_DIR`** is **`$HOME/.cursor`** under the fake home. **Workspace trust** uses **`$CURSOR_DATA_DIR/projects/<slug>/.workspace-trusted`**; **`agentpack`** sets **`CURSOR_DATA_DIR`** to **real `~/.cursor`** when unset so trust state is not lost when **`$STAGING`** is recreated. **`agentpack` itself writes nothing to `~/.cursor`** — Cursor manages its own workspace trust *and* MCP approvals there (MCP servers are auto-allowed under **`--trust`** / **`--force`** headless, or approved once interactively and persisted by Cursor). It also preserves **`CARGO_HOME`**, **`RUSTUP_HOME`**, and **`DOCKER_CONFIG`** from the real home unless those env vars are already set. For a **stable** staging path when your OS rotates temp dirs, set **`AGENTPACK_STAGING_ROOT`**. The Cursor Agent CLI binary is **`cursor-agent`** (default; override with **`CURSOR_AGENT_PATH`**) — not `agent`, which collides with other tools (e.g. Grok ships an `agent` binary). Cursor’s **`cursor-agent`** only accepts **`--trust`** with **`--print`** / headless; **`agentpack`** prepends **`--trust`** automatically in that case.
   - **`agentpack agy`** runs **`agy`** with **`--add-dir <cwd>`** when absent (the current working dir where you invoked `agentpack`, **not** the pack root — same CWD rule as Cursor `--workspace`; the **`./.agents/plugins/agentpack-bundle`** overlay follows it) and leaves **`HOME`** / **`~/.gemini`** untouched.

**MCP merge pipeline** — after pack content and **`.agents/`** overlay are staged, **`sync`** collects MCP server definitions from three sources (merge order; later wins on same server name): **(1)** plugin root **`mcp.json`** files (sorted by `cache_key`, filtered through the selected **mode**), **(2)** manifest **`[mcp.servers]`**, **(3)** **`.agents/mcp.json`**. The merged result is written in each harness's native format: Claude/Cursor JSON **`mcpServers`**, OpenCode **`opencode.json`**, Codex/Grok **`[mcp_servers]`** TOML, and Antigravity plugin **`mcp_config.json`** using **`serverUrl`** for remote servers. For the **Cursor fake HOME**, the merged pack `mcp.json` is further merged with the user’s real **`~/.cursor/mcp.json`** (user entries win on conflict) so agentpack-managed servers coexist with user-defined ones.

After staging, **`sync`** verifies that **skill directory names** under **`bundle/skills/`** and **`.md` file stems** under **`bundle/commands/`** and **`bundle/agents/`** do not **also** appear under **`~/.claude`** or **`~/.grok`** user roots. If they do, the staged pack copy is removed so the user install wins.

Overlay order for staged roots: user config copies first, then **plugins** (by `cache_key`), then **bare skills**, then **project `./.agents/`** — **later layers win** on the same relative path inside `agents`, `commands`, `skills`, etc.

**`~/.claude.json`**, **`~/.config/opencode/opencode.json`**, **`~/.codex/config.toml`**, **`~/.codex/auth.json`**, **`~/.grok/config.toml`**, **`~/.grok/auth.json`**, and files under **`~/.cursor`** may contain sensitive settings or session state. These are copied or linked into temp staging directories to preserve user config when harness roots are redirected; Codex keychain bridging can materialize a shared **`$AGENTPACK_HOME/shared/codex/auth.json`** file so staged homes share refresh-token updates.

### Skill shadowing

A full plugin at repo path **`P`** (same **`owner` / `repo` / `commit`**) shadows **skills** whose path is **`P`** or under **`P/`**. Empty **`P`** shadows all skills for that repo at that commit.

### Attribution defaults

**`sync`** force-disables AI attribution (Co-Authored-By trailers, "Generated with X" footers) in every staged harness so projects do not pick up agent credit lines unintentionally. The user's real **`~/.claude`**, **`~/.codex`**, **`~/.cursor`**, and **`~/.config/opencode`** are never modified — only the staged copies under **`$STAGING`** and the Claude overlay file at **`$AGENTPACK_HOME/claude-settings.json`** (loaded via **`--settings`**). Set **`AGENTPACK_KEEP_ATTRIBUTION=1`** to preserve the user's existing values.

| Harness | Staged file | Forced setting |
| --- | --- | --- |
| Claude Code | **`$AGENTPACK_HOME/claude-settings.json`** (passed via **`claude --settings <path>`**) | **`attribution.commit = ""`**, **`attribution.pr = ""`**, **`includeCoAuthoredBy = false`** ([docs](https://code.claude.com/docs/en/settings)). Loads as **`flagSettings`** scope so it overrides user/project/local. **`CLAUDE_CONFIG_DIR`** is intentionally **not** set — that env var hashes into the keychain service name, so any per-project value would forget login on every project switch. |
| Codex | **`$STAGING/codex-home/config.toml`** | **`commit_attribution = ""`** ([docs](https://developers.openai.com/codex/config-reference)) |
| Cursor | **`$STAGING/cursor/cli-config.json`**, **`$STAGING/cursor-home/.cursor/cli-config.json`** | **`attribution.attributeCommitsToAgent = false`**, **`attribution.attributePRsToAgent = false`** ([docs](https://cursor.com/docs/cli/reference/configuration)) |
| OpenCode | **`$STAGING/opencode/opencode.json`** + **`agentpack-no-attribution.md`** | OpenCode has no first-class attribution setting (sst/opencode#919, sst/opencode#1135 — both auto-closed inactive). agentpack writes a system-prompt file and adds it to **`instructions[]`** as a best-effort prompt-level instruction. |
| Grok | **`$STAGING/grok-home/AGENTS.md`** | No verified first-class attribution-off setting in `grok 0.2.14`; agentpack stages prompt-level guidance only and does not modify real **`~/.grok`**. |
| Antigravity | **`$STAGING/agy/agentpack-bundle/rules/agentpack-no-attribution.md`** | No verified first-class attribution-off setting; agentpack stages an always-apply plugin rule as prompt-level guidance only. |

For Cursor specifically, **`$STAGING/cursor-home/.cursor/cli-config.json`** is materialized as a **real file** (not a symlink to **`~/.cursor/cli-config.json`**) so writes from agentpack do not bleed back into the user's real Cursor profile.

### Environment

| Variable | Meaning |
| --- | --- |
| **`AGENTPACK_HOME`** | User agentpack root (`cache/`, `local/`, `projects/`, `db.reddb`). Overrides XDG / OS defaults. |
| **`AGENTPACK_STAGING_ROOT`** | Staging root override (default: `temp_dir()/agentpack-<hash>`). |
| **`AGENTPACK_KEEP_ATTRIBUTION`** | Set to **`1`** / **`true`** / **`yes`** to keep AI attribution settings (Co-Authored-By trailers, "Generated with X" footers) in staged harness configs. Default: drop attribution (see below). |
| **`AGENTPACK_TUI_THEME`** | Force the mode TUI palette: **`light`** or **`dark`**. Unset = auto-detect from the terminal background (OSC 11 luma / `COLORFGBG`), falling back to **`dark`**. |
| **`CLAUDE_CODE_PATH`** | Path to the **`claude`** binary. |
| **`OPENCODE_PATH`** | Path to the **`opencode`** binary. |
| **`CODEX_PATH`** | Path to the **`codex`** binary. |
| **`CURSOR_AGENT_PATH`** | Path to the **`cursor-agent`** binary. |
| **`GROK_PATH`** | Path to the **`grok`** binary. |
| **`AGY_PATH`** | Path to the **`agy`** binary. |

### Global CLI flags

**`--project-root`**, **`-q` / `--quiet`**, **`--no-progress`**.

### Commands (short)

- **`init`** — write stub **`agentpack.toml`**, **v2** **`pack.lock`**, and ensure **`AGENTPACK_HOME`**. Fails if **`agentpack.toml`** already exists.
- **`lock`** — resolve **`agentpack.toml`** and overwrite **`pack.lock`** with all packages (direct + transitive).
- **`add <spec>`** — append module to **`[dependencies]`**, resolve, save **`pack.lock`**, then **`sync`** unless **`--no-sync`** (requires manifest; see golden rules).
- **`remove <spec>`** — remove matching **`[dependencies]`** key, prune any mode selectors that target that module, resolve, save **`pack.lock`**, then **`sync`** unless **`--no-sync`**. Accepts the same shapes as **`add`** where sensible (module id, **`owner/repo/path`**, GitHub **`tree`/`blob`** URL); picks the **`[dependencies]`** entry by walking parent paths for blob file URLs, like **`add`**.
- **`sync`** — ensure cache + rebuild staging; recomputes **`pack.lock`** from the manifest when **`[dependencies]`** is non-empty.
- **`mcp add <name> --command <cmd> [--args ...] [--env K=V ...]`** — add an MCP server to **`[mcp.servers]`** in **`agentpack.toml`**, then **`sync`** unless **`--no-sync`**.
- **`mcp remove <name>`** — remove an MCP server from **`[mcp.servers]`**, then **`sync`** unless **`--no-sync`**.
- **`mcp list`** — show all MCP servers (from manifest, plugins, and **`.agents/mcp.json`**) with provenance.
- **`claude`**, **`opencode`**, **`codex`**, **`grok`**, **`agent`**, **`agy`** — refresh staging via **`sync`** (fast path when nothing changed) then exec with the staged harness roots (see Launchers).

### `agentpack.toml` sketch

```toml
name = "myproj"
version = "0.0.1"

[dependencies]
"github.com/anthropics/skills/skills/canvas-design" = { branch = "main" }
"github.com/anthropics/claude-plugins-official/plugins/hookify" = { branch = "main" }
mcp-retrieval = { path = "../mcp-retrieval" }

[modes.default]
base = "all"
disable = [ "package-path:github.com/someorg/heavy-pack:commands/noise.md" ]

[modes.design]
base = "all"
disable = [ "mcp:filesystem" ]

[mcp.servers.filesystem]
command = "npx"
args = ["-y", "@modelcontextprotocol/server-filesystem"]

[mcp.servers.retrieval]
command = "uvx"
args = ["mcp-retrieval"]
env = { API_KEY = "sk-..." }
```

### `pack.lock` sketch (v2)

```toml
lockfile-version = 2

[meta]
name = "myproj"
version = "0.0.1"

[config]
disabled_plugins = []

[[packages]]
module = "github.com/anthropics/skills/skills/canvas-design"
direct = true
kind = "skill"
url = "https://github.com/anthropics/skills/tree/<40-hex>/skills/canvas-design"
owner = "anthropics"
repo = "skills"
path = "skills/canvas-design"
commit = "<40 hex>"
cache_key = "<64 hex>"
name = ""

[[packages]]
module = "github.com/anthropics/claude-plugins-official/plugins/hookify"
direct = true
kind = "plugin"
url = "https://github.com/anthropics/claude-plugins-official/tree/<40-hex>/plugins/hookify"
owner = "anthropics"
repo = "claude-plugins-official"
path = "plugins/hookify"
commit = "<40 hex>"
cache_key = "<64 hex>"
name = "hookify"
```

### Limits

OpenCode is launched by replacing its config root, not by adding a plugin dir. **`agentpack agent`** rewrites **`HOME`** to **`$STAGING/cursor-home`** so **`$HOME/.cursor`** blends staged **`pack.lock`** symlinks with symlinks to your real Cursor credential/session files, while preserving **`CARGO_HOME`**, **`RUSTUP_HOME`**, and **`DOCKER_CONFIG`** for toolchain commands. Some auth may live in OS keychains or paths outside **`~/.cursor`**; those are not redirected. Codex is launched by replacing **`CODEX_HOME`** and currently only gets the **portable skill** subset of pack content; agentpack does **not** synthesize Codex plugin marketplaces from cached Claude plugins. Grok is launched by replacing **`GROK_HOME`** because the installed CLI does not accept additive **`--plugin-dir`**. Antigravity is launched without config redirection; pack content reaches it through **`./.agents/plugins/agentpack-bundle`**.
