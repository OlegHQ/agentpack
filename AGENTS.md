# agentpack

`agentpack` is a Rust CLI that pins **GitHub-hosted skills** and **plugin directories** (`.claude-plugin` and/or `.cursor-plugin`) for a project.

**Source of truth for what to install** is **`agentpack.toml`** at the repo root (direct dependencies, project-local modes, and MCP settings). **`pack.lock`** (v2) lists every resolved **package** (direct and transitive from nested `agentpack.toml` files inside dependencies) with pinned commits and `cache_key`s. Both files live in the **project repo**.

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
| **`$AGENTPACK_HOME/shared/codex/auth.json`** | Shared Codex auth cache used by staged **`CODEX_HOME`** trees when the real user config stores credentials in the OS keychain instead of **`~/.codex/auth.json`**. |

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

**`agentpack agent`** runs the Cursor CLI with **`HOME=$STAGING/cursor-home`**. **`$HOME/.cursor/commands`** (etc.) symlink into the staged **`pack.lock`** tree. **`agentpack`** also sets **`CURSOR_CONFIG_DIR=$HOME/.cursor`** on the child process. Cursor workspace trust still uses **`CURSOR_DATA_DIR`**; when it is unset, **`agentpack`** points it at real **`~/.cursor`** so trust state survives staging rebuilds. To keep shell tools working inside the staged HOME, agentpack also bridges **`CARGO_HOME=~/.cargo`**, **`RUSTUP_HOME=~/.rustup`**, and **`DOCKER_CONFIG=~/.docker`** unless the user already set them.

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
   - **Packages:** target-specific converted markdown artifacts plus raw Claude support dirs (`hooks`, `matchers`, `core`, `examples`, `utils`), filtered through the selected **mode**.
   - **MCP:** merged `mcp.json` written to bundle root (see MCP merge below).
4a. **Claude config dir** — **`sync`** also rebuilds **`$STAGING/claude-home/`** as the **`CLAUDE_CONFIG_DIR`** target. The env var is read **asymmetrically** by Claude (verified against the v2.1.x bundle): for `settings.json` it IS the dir (`<env>/settings.json`); for `.claude.json` it is the parent (`<env>/.claude.json`). Both live at the root of the staged dir:
   - **`$STAGING/claude-home/settings.json`** is **materialized as a real file** (not a symlink) merged from **`~/.claude/settings.json`** with **attribution forced off**. Other user keys (e.g. **`skipDangerousModePermissionPrompt`**) are preserved so the staged session keeps the user's behavior. Writes from agentpack do not leak into the user's real settings.
   - **`$STAGING/claude-home/.claude.json`** is a **symlink** to **`~/.claude.json`** so per-project trust/auth/MCP state continues to read/write the user's real file.
   - Every other entry from **`~/.claude/`** (auth, `projects/`, `commands/`, `agents/`, `skills/`, `hooks/`, etc.) is **symlinked** at the root of `claude-home/` so it resolves to the user's real on-disk files.
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
   - **Workspace subagents symlink:** **`./.cursor/agents`** → staged pack **`agents/`** (Cursor **`--workspace`** only). **`cursor-overlay.manifest`** tracks agentpack-owned overlay paths for safe cleanup (symlinks/files only — never deletes a real directory).
   - **Migration:** older **`cursor-overlay.manifest`** entries under **`$AGENTPACK_HOME/projects/<hash>/`** are still removed at the start of **`sync`** when present.
8. **Launchers**
   - **`agentpack claude`** runs **`claude`** with **`--plugin-dir`** pointing at **`agentpack-bundle`** and **`CLAUDE_CONFIG_DIR=$STAGING/claude-home`** so attribution-forced settings are honored without modifying real **`~/.claude`**.
   - **`agentpack opencode`** runs **`opencode`** with **`OPENCODE_CONFIG_DIR=$STAGING/opencode`**.
   - **`agentpack codex`** runs **`codex`** with **`CODEX_HOME=$STAGING/codex-home`**.
   - **`agentpack agent`** runs Cursor Agent with **`HOME=$STAGING/cursor-home`**. **`--workspace`** defaults to the **canonical project root** (same place you **`add` / `sync`**). **`CURSOR_CONFIG_DIR`** is **`$HOME/.cursor`** under the fake home. **Workspace trust** uses **`$CURSOR_DATA_DIR/projects/<slug>/.workspace-trusted`**; **`agentpack`** sets **`CURSOR_DATA_DIR`** to **real `~/.cursor`** when unset so trust state is not lost when **`$STAGING`** is recreated. It also preserves **`CARGO_HOME`**, **`RUSTUP_HOME`**, and **`DOCKER_CONFIG`** from the real home unless those env vars are already set. For a **stable** staging path when your OS rotates temp dirs, set **`AGENTPACK_STAGING_ROOT`**. Cursor’s **`agent`** only accepts **`--trust`** with **`--print`** / headless; **`agentpack`** prepends **`--trust`** automatically in that case.

**MCP merge pipeline** — after pack content and **`.agents/`** overlay are staged, **`sync`** collects MCP server definitions from three sources (merge order; later wins on same server name): **(1)** plugin root **`mcp.json`** files (sorted by `cache_key`, filtered through the selected **mode**), **(2)** manifest **`[mcp.servers]`**, **(3)** **`.agents/mcp.json`**. The merged result is written as `{"mcpServers":{…}}` JSON to all four harness staging roots. For the **Cursor fake HOME**, the merged pack `mcp.json` is further merged with the user’s real **`~/.cursor/mcp.json`** (user entries win on conflict) so agentpack-managed servers coexist with user-defined ones.

After staging, **`sync`** verifies that **skill directory names** under **`bundle/skills/`** and **`.md` file stems** under **`bundle/commands/`** and **`bundle/agents/`** do not **also** appear under **`~/.claude/skills`**, **`commands`**, or **`agents`**. If they do, the staged pack copy is removed so the user install wins (Claude would otherwise list both **`/foo`** and **`/agentpack-bundle:foo`**).

Overlay order for staged roots: user config copies first, then **plugins** (by `cache_key`), then **bare skills**, then **project `./.agents/`** — **later layers win** on the same relative path inside `agents`, `commands`, `skills`, etc.

**`~/.claude.json`**, **`~/.config/opencode/opencode.json`**, **`~/.codex/config.toml`**, **`~/.codex/auth.json`**, and files under **`~/.cursor`** may contain sensitive settings or session state. These are copied into a temp staging directory to preserve user config when harness roots are redirected; Codex keychain bridging can materialize a shared **`$AGENTPACK_HOME/shared/codex/auth.json`** file so staged homes share refresh-token updates.

### Skill shadowing

A full plugin at repo path **`P`** (same **`owner` / `repo` / `commit`**) shadows **skills** whose path is **`P`** or under **`P/`**. Empty **`P`** shadows all skills for that repo at that commit.

### Attribution defaults

**`sync`** force-disables AI attribution (Co-Authored-By trailers, "Generated with X" footers) in every staged harness so projects do not pick up agent credit lines unintentionally. The user's real **`~/.claude`**, **`~/.codex`**, **`~/.cursor`**, and **`~/.config/opencode`** are never modified — only the staged copies under **`$STAGING`**. Set **`AGENTPACK_KEEP_ATTRIBUTION=1`** to preserve the user's existing values.

| Harness | Staged file | Forced setting |
| --- | --- | --- |
| Claude Code | **`$STAGING/claude-home/settings.json`** (`CLAUDE_CONFIG_DIR=$STAGING/claude-home`) | **`attribution.commit = ""`**, **`attribution.pr = ""`**, **`includeCoAuthoredBy = false`** ([docs](https://code.claude.com/docs/en/settings)). The plugin dir is not a settings source — the redirect points Claude at our staged `settings.json` and the sibling `.claude.json` symlink. |
| Codex | **`$STAGING/codex-home/config.toml`** | **`commit_attribution = ""`** ([docs](https://developers.openai.com/codex/config-reference)) |
| Cursor | **`$STAGING/cursor/cli-config.json`**, **`$STAGING/cursor-home/.cursor/cli-config.json`** | **`attribution.attributeCommitsToAgent = false`**, **`attribution.attributePRsToAgent = false`** ([docs](https://cursor.com/docs/cli/reference/configuration)) |
| OpenCode | **`$STAGING/opencode/opencode.json`** + **`agentpack-no-attribution.md`** | OpenCode has no first-class attribution setting (sst/opencode#919, sst/opencode#1135 — both auto-closed inactive). agentpack writes a system-prompt file and adds it to **`instructions[]`** as a best-effort prompt-level instruction. |

For Cursor specifically, **`$STAGING/cursor-home/.cursor/cli-config.json`** is materialized as a **real file** (not a symlink to **`~/.cursor/cli-config.json`**) so writes from agentpack do not bleed back into the user's real Cursor profile.

### Environment

| Variable | Meaning |
| --- | --- |
| **`AGENTPACK_HOME`** | User agentpack root (`cache/`, `local/`, `projects/`, `db.reddb`). Overrides XDG / OS defaults. |
| **`AGENTPACK_STAGING_ROOT`** | Staging root override (default: `temp_dir()/agentpack-<hash>`). |
| **`AGENTPACK_KEEP_ATTRIBUTION`** | Set to **`1`** / **`true`** / **`yes`** to keep AI attribution settings (Co-Authored-By trailers, "Generated with X" footers) in staged harness configs. Default: drop attribution (see below). |
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
- **`remove <spec>`** — remove matching **`[dependencies]`** key, prune any mode selectors that target that module, resolve, save **`pack.lock`**, then **`sync`** unless **`--no-sync`**. Accepts the same shapes as **`add`** where sensible (module id, **`owner/repo/path`**, GitHub **`tree`/`blob`** URL); picks the **`[dependencies]`** entry by walking parent paths for blob file URLs, like **`add`**.
- **`sync`** — ensure cache + rebuild staging; recomputes **`pack.lock`** from the manifest when **`[dependencies]`** is non-empty.
- **`mcp add <name> --command <cmd> [--args ...] [--env K=V ...]`** — add an MCP server to **`[mcp.servers]`** in **`agentpack.toml`**, then **`sync`** unless **`--no-sync`**.
- **`mcp remove <name>`** — remove an MCP server from **`[mcp.servers]`**, then **`sync`** unless **`--no-sync`**.
- **`mcp list`** — show all MCP servers (from manifest, plugins, and **`.agents/mcp.json`**) with provenance.
- **`claude`**, **`opencode`**, **`codex`**, **`agent`** — refresh staging via **`sync`** (fast path when nothing changed) then exec with the staged harness roots (see Launchers).

### `agentpack.toml` sketch

```toml
name = "myproj"
version = "0.0.1"

[dependencies]
"github.com/anthropics/skills/skills/canvas-design" = { branch = "main" }
"github.com/anthropics/claude-plugins-official/plugins/hookify" = { version = "^1.0.0" }
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

OpenCode is launched by replacing its config root, not by adding a plugin dir. **`agentpack agent`** rewrites **`HOME`** to **`$STAGING/cursor-home`** so **`$HOME/.cursor`** blends staged **`pack.lock`** symlinks with symlinks to your real Cursor credential/session files, while preserving **`CARGO_HOME`**, **`RUSTUP_HOME`**, and **`DOCKER_CONFIG`** for toolchain commands. Some auth may live in OS keychains or paths outside **`~/.cursor`**; those are not redirected. Codex is launched by replacing **`CODEX_HOME`** and currently only gets the **portable skill** subset of pack content; agentpack does **not** synthesize Codex plugin marketplaces from cached Claude plugins.
