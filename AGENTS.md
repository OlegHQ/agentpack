# agentpack

`agentpack` is a Rust CLI that pins **GitHub-hosted skills** and **plugin directories** (`.claude-plugin` and/or `.cursor-plugin`) for a project.

**Source of truth for what to install** is **`agentpack.toml`** at the repo root (direct dependencies and optional path overrides). **`pack.lock`** (v2) lists every resolved **package** (direct and transitive from nested `agentpack.toml` files inside dependencies) with pinned commits and `cache_key`s. Both files live in the **project repo**.

All downloaded trees, the RedDB index, and your **`local/`** mirror live under a **user-wide agentpack home** (see below)—not under a repo-local `.agentpack/` directory. Staging for harnesses still uses a **per-project temp** directory (or **`AGENTPACK_STAGING_ROOT`**).

### Pre-release

**No backwards compatibility.** `agentpack` is pre-release: CLI behavior, lockfile shape, staging layout, env vars, and defaults may change without a migration period or deprecation window. Assume **breaking changes** between versions until a stable release is declared.

### User data layout (`AGENTPACK_HOME`)

| Path | Purpose |
| --- | --- |
| **`$AGENTPACK_HOME/cache/<cache_key>/`** | Content-addressed package trees (GitHub tarball, or **copies** from filesystem / `local/`). |
| **`$AGENTPACK_HOME/cache/db.reddb`** | Metadata + alias map for fast repeat **`add`**, plus cached GitHub ref/tag lookups to reduce API calls. |
| **`$AGENTPACK_HOME/local/<owner>/<repo>/…`** | Optional offline mirror; same slash layout as **`owner/repo/…`** specs. |
| **`$AGENTPACK_HOME/projects/<hash>/cursor-overlay.manifest`** | Per-project Cursor overlay bookkeeping (not stored in the repo). |

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
| **`[overrides."github.com/o/r/pkg"]`** | Project-only tweaks. **`disable = ["commands/foo.md", "hooks"]`** — relative paths under that package root **omitted from staging** (converted markdown, raw plugin support dirs, and root files like `mcp.json` when listed). |

Transitive dependencies come **only** from a **`agentpack.toml`** (dependencies table) **inside** an fetched package cache root. There is no implicit scratchpad: **`add`** edits the project manifest; **`lock`** / **`sync`** (when dependencies are non-empty) recompute **`pack.lock`**.

### Golden rules for **`add <spec>`**

Resolution order (network/local):

1. **`https://github.com/…`** — tree or blob URL; the **directory** containing **`SKILL.md`** or a plugin manifest is fetched; the module id is derived from **owner / repo / in-repo path**.
2. **`owner/repo`** — tries **`$AGENTPACK_HOME/local/<owner>/<repo>`** first (copy); else **GitHub** at **repo root**.
3. **`owner/repo/p1/p2/...`** — tries **`local/…/full/slash/spec`** first; else **GitHub** with in-repo path **`p1/p2/...`**.
4. **Single segment** **`name`** — **`local/<name>`** only, or **alias** in RedDB to reuse a **`cache_key`** without network.

Repeat **`owner/repo`** and **`owner/repo/path`** adds also consult the RedDB alias/index after checking **`local/`**, so previously fetched GitHub packages are reused before any new GitHub request is made.

**Not automatic:** a bare **filesystem path** is not passed to **`add`** — put a **`file:`** (or path) dependency in **`agentpack.toml`** yourself if you need a directory pin; **`sync`** will warn on other machines if the path is missing and the cache slot is empty.

Duplicate content for the same **`owner` / `repo` / in-repo `path` / commit** hits the same **`cache_key`**. Plugins may expose **`.claude-plugin`**, **`.cursor-plugin`**, or both; layouts are normalized after fetch.

### Lockfile v2 and **`sync`**

- **`pack.lock`** with **`lockfile-version = 2`** stores **`[[packages]]`** only. Legacy **`[[skills]]`** / **`[[plugins]]`** sections are rejected. In-memory **`skills`** / **`plugins`** are derived views rebuilt from canonical packages after load.
- **`sync`** refreshes **`pack.lock`** from **`agentpack.toml`** only when **`[dependencies]`** is **non-empty**. With an **empty** dependency table, **`sync`** treats the existing lock as authoritative (manual edits, tests, or hybrid workflows).
- Run **`agentpack lock`** to force a full resolve from the manifest (requires **`agentpack.toml`**).
- Harness launchers (**`agentpack claude`**, **`opencode`**, **`codex`**, **`agent`**) run a **fast pre-sync** when **`agentpack.toml`**, **`pack.lock`**, **`./.agents/`**, and the env vars that affect staging (see **`AGENTPACK_LAUNCH_FULL_SYNC`**) are unchanged since the last successful launch sync: they verify cache + staging integrity and **skip** full lock resolve, re-download, and staging rebuild. Floating pins (branch / floating semver) therefore **do not advance** on launch alone — run **`agentpack sync`** or **`agentpack lock`** when you need **`pack.lock`** refreshed from the manifest.
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

**`agentpack agent`** runs the Cursor CLI with **`HOME=$STAGING/cursor-home`**. **`$HOME/.cursor/commands`** (etc.) symlink into the staged **`pack.lock`** tree. **`agentpack`** also sets **`CURSOR_CONFIG_DIR=$HOME/.cursor`** on the child process (unless **`AGENTPACK_CURSOR_CONFIG_DIR`** overrides): the bundled `cursor-config` resolves the config root as **`CURSOR_CONFIG_DIR`**, else **`$XDG_CONFIG_HOME/cursor`**, else **`$HOME/.cursor`**, so without this, **Linux** (fake **`XDG_CONFIG_HOME`**) or a user-global **`CURSOR_CONFIG_DIR` / `XDG_CONFIG_HOME`** can point **`agent`** at a directory that **lacks** the staged **`agents/`** tree (custom **subagents** from **`pack.lock`** would appear missing). Login/session data is **not** only under **`~/.cursor`**: on **macOS** the Electron app uses **`~/Library/Application Support/Cursor`** (state DB, cookies, **`machineid`**). **`sync`** symlinks that whole directory into the fake HOME, and also symlinks **`agent-cli-state.json`** and other CLI files from real **`~/.cursor`**. On **Linux**, **`~/.config/Cursor`** (and **`~/.local/share/Cursor`** when present) are symlinked and **`XDG_CONFIG_HOME` / `XDG_DATA_HOME`** are pointed at the fake HOME layout for **`agent`**. On **Windows**, **`%USERPROFILE%\\AppData\\Roaming\\Cursor`** is symlinked and **`APPDATA` / `LOCALAPPDATA`** are set under the fake profile.

The Cursor CLI also reads workspace **`.cursor/`** for some features; behavior may combine configured **`CURSOR_CONFIG_DIR`**, **`--workspace`**, and **`CURSOR_DATA_DIR`** (workspace trust / projects).

## What agentpack does

**Isolation (like **`venv` / `uv`**) —** Pin resolution from **`agentpack.toml`** stays under **`$AGENTPACK_HOME`** and ephemeral **`$STAGING`**. **`agentpack` does not copy pack trees into your git workspace** (no materialized commands/skills/rules under **`./.cursor/`** for pack content) **and does not symlink pack agents into your real `~/.cursor` or `~/.claude`** — that would leak project-specific pins into global userspace.

**Cursor `agent` subagents —** The bundled **`agent`** CLI resolves [subagents](https://cursor.com/docs/subagents) from **`resolve(--workspace)/.cursor/agents`** only (not from fake **`$HOME/.cursor/agents`** for that list). **`sync`** therefore creates **`./.cursor/agents`** as a **symlink** into **`$STAGING/cursor/agentpack-bundle/agents`** when the pack exposes agent markdown, and records the path in **`cursor-overlay.manifest`** under **`$AGENTPACK_HOME/projects/<hash>/`** so the next **`sync`** can replace the symlink safely. If **`./.cursor/agents`** already exists as a **directory** or **file**, **`sync`** leaves it alone and logs a warning. Add **`./.cursor/agents`** (or **`.cursor/agents`**) to **`.gitignore`** if you do not want the symlink in version control.

**Project `./.agents/` ([dot-agents](https://github.com/dot-agents/dot-agents)-style)** — Optional. After pack content is staged, **`sync`** merges **`./.agents/`** into harness trees under **`$STAGING`** that do **not** natively read the directory: **Claude bundle** and **Codex home**. Shared **`rules/**/*.mdc`** (hard-linked into staged **`rules/`** when possible), **`skills/`**, **`agents/`**, **`commands/`**, **`hooks/`**, top-level **`AGENTS.md`** (Codex), **`CLAUDE.md`** (Claude bundle), **`mcp.json`**, and optional subtrees **`claude/`**, **`codex/`** that mirror each harness layout. **Cursor and OpenCode are excluded** — both natively discover **`.agents/`** from the workspace (skills, commands, agents, rules all appear as project-scoped), so merging into their staging would duplicate content. The only workspace pointer **`agentpack`** may still add is **`./.cursor/agents`** → merged staged **`agents/`** (for pack content subagent discovery, not dot-agents). Set **`AGENTPACK_DOT_AGENTS=0`** to skip.

1. **Cache** — **`add`**, **`lock`**, and **`sync`** populate **`$AGENTPACK_HOME/cache/<cache_key>/`**.  
2. **Index** — **`$AGENTPACK_HOME/cache/db.reddb`** stores metadata (`kind`: `skill` | `plugin`) and shorthand **aliases** → `cache_key`.  
3. **Artifact conversion** — **`sync`** parses supported markdown artifacts from cached pack content and re-renders them per target harness instead of copying frontmatter blindly:
   - **Commands**: Cursor plain markdown, OpenCode markdown frontmatter, Claude command/skill frontmatter, Codex skill fallback.
   - **Agents**: Claude / OpenCode / Cursor agent markdown frontmatter, Codex skill fallback.
   - **Skills**: skill frontmatter normalized and rendered as target skills.
   - **Rules**: Cursor `.mdc` rules preserved for Cursor; other harnesses get a best-effort skill fallback with original rule scope noted when the target lacks first-class rule files.
4. **Claude bundle** — **`sync`** rebuilds **`$STAGING/plugins/agentpack-bundle/`** with **`.claude-plugin/plugin.json`** and:
   - **Optional:** copies **`~/.claude/settings.json`** → **`bundle/.claude/settings.json`** and **`~/.claude.json`** → **`bundle/.claude.json`** (parsed and rewritten as pretty JSON). No **`commands/`** / **`agents/`** / **`skills/`** from `~/.claude`.
   - **Packages:** target-specific converted markdown artifacts plus raw Claude support dirs (`hooks`, `matchers`, `core`, `examples`, `utils`), respecting **`[overrides]`** **`disable`** paths when **`agentpack.toml`** is present.
5. **OpenCode root** — **`sync`** rebuilds **`$STAGING/opencode/`**:
   - **Optional:** seeds from **`~/.config/opencode/`** (`opencode.json`, `agents`, `commands`, `modes`, `plugins`, `skills`) so provider/auth config still works when **`OPENCODE_CONFIG_DIR`** is redirected.
   - **Overlay:** converted pack commands / agents / skills / rules written into OpenCode’s supported markdown locations.
6. **Codex home** — **`sync`** rebuilds **`$STAGING/codex-home/`**:
   - **Optional:** seeds from **`~/.codex/`** (`config.toml`, **`auth.json`**, `skills`, `themes`) so user config still works when **`CODEX_HOME`** is redirected. The Codex CLI stores OAuth/API material in **`auth.json`** or in the OS keychain keyed by the **canonical `CODEX_HOME` path**; a staged path would otherwise miss keychain entries. When **`auth.json`** is missing in the staged tree, agentpack may **read the same keychain entry** used for **`~/.codex`** (service **`Codex Auth`**) and write **`$STAGING/codex-home/auth.json`**. If the **staged** copy of **`config.toml`** sets **`cli_auth_credentials_store = "keyring"`**, agentpack rewrites that setting to **`file`** in the staging copy only so the CLI loads the bridged **`auth.json`**.
   - **Overlay:** portable pack content is rendered into Codex **skills** under **`$STAGING/codex-home/skills/`**. Full Claude plugins are **not** translated into Codex plugin marketplaces.
7. **Cursor staging** — **`sync`** rebuilds **`$STAGING/cursor/`** as a [Cursor plugins](https://cursor.com/docs/reference/plugins) layout, then builds **`$STAGING/cursor-home/`** as a **fake `HOME`** for the CLI:
   - **Plugin / pack tree:** **`$STAGING/cursor/.cursor-plugin/marketplace.json`**, **`$STAGING/cursor/agentpack-bundle/.cursor-plugin/plugin.json`**, plus **`commands/`**, **`agents/`**, **`skills/`**, **`rules/`**, **`hooks/`**, **`assets/`**, **`scripts/`**, **`mcp.json`** when present.
   - **Fake `HOME`:** **`$STAGING/cursor-home/.cursor/`** symlinks pack dirs and (when present on disk) **`cli-config.json`**, **`machineid`**, **`agent-cli-state.json`**, **`argv.json`**, **`ide_state.json`**, **`mcp.json`**, **`User/globalStorage`**. **macOS:** symlink **`~/Library/Application Support/Cursor`** → **`$HOME/Library/.../Cursor`**. **Linux:** **`~/.config/Cursor`** and **`~/.local/share/Cursor`**. **Windows:** **`%USERPROFILE%\\AppData\\Roaming\\Cursor`**.
   - **Optional:** copies **`cli-config.json`** and **`mcp.json`** from **`~/.cursor/`** into **`$STAGING/cursor/`** when user-settings seeding is enabled (separate from the fake `HOME` symlink pass). User **`agents/`**, **`commands/`**, and similar are not merged from **`~/.cursor`** (pack content lives under **`agentpack-bundle/`**; the profile tree can contain broken symlinks).
   - **Workspace subagents symlink:** **`./.cursor/agents`** → staged pack **`agents/`** (Cursor **`--workspace`** only). **`cursor-overlay.manifest`** tracks agentpack-owned overlay paths for safe cleanup (symlinks/files only — never deletes a real directory).
   - **Migration:** older **`cursor-overlay.manifest`** entries under **`$AGENTPACK_HOME/projects/<hash>/`** are still removed at the start of **`sync`** when present.
8. **Launchers**
   - **`agentpack claude`** runs **`claude`** with **`--plugin-dir`** pointing at **`agentpack-bundle`** (unless **`AGENTPACK_PLUGIN_DIRS`** overrides).
   - **`agentpack opencode`** runs **`opencode`** with **`OPENCODE_CONFIG_DIR=$STAGING/opencode`** (unless **`AGENTPACK_OPENCODE_CONFIG_DIR`** overrides).
   - **`agentpack codex`** runs **`codex`** with **`CODEX_HOME=$STAGING/codex-home`** (unless **`AGENTPACK_CODEX_HOME`** overrides).
   - **`agentpack agent`** runs Cursor Agent with **`HOME=$STAGING/cursor-home`** (and on Windows **`USERPROFILE`** to match). **`--workspace`** defaults to the **canonical project root** (same place you **`add` / `sync`**). **`CURSOR_CONFIG_DIR`** is **`$HOME/.cursor`** under the fake home unless **`AGENTPACK_CURSOR_CONFIG_DIR`** overrides. **Workspace trust** uses **`$CURSOR_DATA_DIR/projects/<slug>/.workspace-trusted`** (see `cursor-config`); **`agentpack`** sets **`CURSOR_DATA_DIR`** to **real `~/.cursor`** when unset so trust state is not lost when **`$STAGING`** is recreated. For a **stable** staging path when your OS rotates temp dirs, set **`AGENTPACK_STAGING_ROOT`**. Electron **`globalStorage` / `workspaceStorage`** are symlinked into the fake HOME where needed. Cursor’s **`agent`** only accepts **`--trust`** with **`--print`** / headless; **`agentpack`** prepends **`--trust`** in that case unless **`AGENTPACK_CURSOR_AGENT_TRUST=0`**.

After staging, **`sync`** verifies that **skill directory names** under **`bundle/skills/`** and **`.md` file stems** under **`bundle/commands/`** and **`bundle/agents/`** do not **also** appear under **`~/.claude/skills`**, **`commands`**, or **`agents`**. If they do, sync **fails** with a clear message (Claude would list both **`/foo`** and **`/agentpack-bundle:foo`**). Override with **`AGENTPACK_IGNORE_USER_BUNDLE_COLLISION=1`** if you accept the duplication.

Overlay order for staged roots: optional **user config copies** first, then **plugins** (by `cache_key`), then **bare skills**, then **project `./.agents/`** when present — **later layers win** on the same relative path inside `agents`, `commands`, `skills`, etc.

**`~/.claude.json`**, **`~/.config/opencode/opencode.json`**, **`~/.codex/config.toml`**, **`~/.codex/auth.json`**, and files under **`~/.cursor`** may contain sensitive settings or session state. Copying them into a temp staging directory may widen exposure (including a **materialized** Codex **`auth.json`** when bridging from the keychain). Turn off these seed copies with **`AGENTPACK_BUNDLE_USER_SETTINGS=0`** if you only want pack content in staged roots.

### Skill shadowing

A full plugin at repo path **`P`** (same **`owner` / `repo` / `commit`**) shadows **skills** whose path is **`P`** or under **`P/`**. Empty **`P`** shadows all skills for that repo at that commit.

### Environment

| Variable | Meaning |
| --- | --- |
| **`AGENTPACK_HOME`** | User agentpack root (`cache/`, `local/`, `projects/`, `db.reddb`). Overrides XDG / OS defaults. |
| **`AGENTPACK_STAGING_ROOT`** | Staging root override (default: `temp_dir()/agentpack-<hash>`). |
| **`AGENTPACK_LAUNCH_FULL_SYNC`** | **`1`**, **`true`**, or **`yes`** — on **`claude` / `opencode` / `codex` / `agent`**, always run a full **`sync`** before exec (disables the launch fast path). Default: use the fast path when inputs match the last successful launcher sync. |
| **`AGENTPACK_BUNDLE_USER_SETTINGS`** | **`0`** — do not seed staged harness roots from **`~/.claude`**, **`~/.config/opencode`**, **`~/.codex`**, or **`~/.cursor`**. Default: copy compatible user config files when they exist. |
| **`AGENTPACK_BUNDLE_USER_CLAUDE`** | Legacy alias for **`AGENTPACK_BUNDLE_USER_SETTINGS`**. |
| **`AGENTPACK_PLUGIN_DIRS`** | Colon-separated plugin roots; **`claude`** uses these instead of staging. |
| **`AGENTPACK_OPENCODE_CONFIG_DIR`** | Override the staged OpenCode config root used by **`agentpack opencode`**. |
| **`AGENTPACK_CODEX_HOME`** | Override the staged Codex home used by **`agentpack codex`**. |
| **`AGENTPACK_CURSOR_HOME`** | Fake home directory used by **`agentpack agent`**. Default: **`$STAGING/cursor-home`**. |
| **`AGENTPACK_CURSOR_CONFIG_DIR`** | Optional: use this path as **`CURSOR_CONFIG_DIR`** for **`agentpack agent`** instead of the default **`$AGENTPACK_CURSOR_HOME/.cursor`**. |
| **`CURSOR_DATA_DIR`** | If **unset** when **`agentpack agent`** runs, set to **real `~/.cursor`** so workspace trust files under **`projects/`** are not stored under ephemeral staging. Override explicitly if you use a non-default Cursor data root. |
| **`AGENTPACK_CURSOR_AGENT_TRUST`** | **`0`**: never prepend **`--trust`**. Unset: prepend **`--trust`** only when args include **`--print`**, **`-p`**, or **`--output-format`** (Cursor requires that combo). |
| **`AGENTPACK_IGNORE_USER_BUNDLE_COLLISION`** | **`1`** — skip the **`sync`** check that errors when a skill slug or **`commands`/`agents` `.md`** stem exists under **both** **`~/.claude/`** and **`agentpack-bundle`** (duplicated slash UX). Default: enforce. |
| **`AGENTPACK_DOT_AGENTS`** | **`0`** — do not merge **`./.agents/`** into staged harness trees. Default: merge into Claude and Codex staging when the directory exists. Cursor and OpenCode are always excluded (they read **`.agents/`** natively from the workspace). |
| **`CLAUDE_CODE_PATH`** | Path to the **`claude`** binary. |
| **`OPENCODE_PATH`** | Path to the **`opencode`** binary. |
| **`CODEX_PATH`** | Path to the **`codex`** binary. |
| **`CURSOR_AGENT_PATH`** | Path to the **`agent`** binary. |

### Global CLI flags

**`--project-root`**, **`-q` / `--quiet`**, **`--no-progress`**.

### Commands (short)

- **`init`** — write stub **`agentpack.toml`**, **v2** **`pack.lock`**, and ensure **`AGENTPACK_HOME`**. Fails if **`agentpack.toml`** already exists.
- **`lock`** — resolve **`agentpack.toml`** and overwrite **`pack.lock`** with all packages (direct + transitive).
- **`add <spec>`** — append module to **`[dependencies]`**, resolve, save **`pack.lock`**, then **`sync`** unless **`--no-sync`** (requires manifest; see golden rules).
- **`remove <spec>`** — remove matching **`[dependencies]`** key (and **`[overrides]`** for that module), resolve, save **`pack.lock`**, then **`sync`** unless **`--no-sync`**. Accepts the same shapes as **`add`** where sensible (module id, **`owner/repo/path`**, GitHub **`tree`/`blob`** URL); picks the **`[dependencies]`** entry by walking parent paths for blob file URLs, like **`add`**.
- **`sync`** — ensure cache + rebuild staging; recomputes **`pack.lock`** from the manifest when **`[dependencies]`** is non-empty.
- **`claude`**, **`opencode`**, **`codex`**, **`agent`** — refresh staging via **`sync`** (fast path when nothing changed; see **`AGENTPACK_LAUNCH_FULL_SYNC`**) then exec with the staged harness roots (see Launchers).

### `agentpack.toml` sketch

```toml
name = "myproj"
version = "0.0.1"

[dependencies]
"github.com/anthropics/skills/skills/canvas-design" = { branch = "main" }
"github.com/anthropics/claude-plugins-official/plugins/hookify" = { version = "^1.0.0" }

[overrides."github.com/someorg/heavy-pack"]
disable = [ "commands/noise.md", "hooks" ]
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

OpenCode is launched by replacing its config root, not by adding a plugin dir. **`agentpack agent`** rewrites **`HOME`** (and **`USERPROFILE`** on Windows) to **`$STAGING/cursor-home`** so **`$HOME/.cursor`** blends staged **`pack.lock`** symlinks with symlinks to your real Cursor credential/session files — **`pack.lock`** content still never lands in the git workspace. Some auth may live in OS keychains or paths outside **`~/.cursor`**; those are not redirected. Codex is launched by replacing **`CODEX_HOME`** and currently only gets the **portable skill** subset of pack content; agentpack does **not** synthesize Codex plugin marketplaces from cached Claude plugins.
