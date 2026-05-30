# agentpack: a lockfile for your AI coding agents

Every coding agent — Claude Code, Codex, Cursor, OpenCode, Grok, Antigravity — has invented its own way to load "skills," "plugins," "rules," "commands," and "agents." Each reads a different home directory, in a different format, with a different precedence model. None of them have a notion of *pinning a dependency to a commit* the way `npm`, `cargo`, or `uv` do.

So if your team wants everyone to use the same code-review skill, the same set of slash commands, the same MCP servers — your options today are "copy these files into your `~/.claude`," "paste this into your settings," or a wiki page that's already stale. There's no `package.json` for agent tooling, and definitely no `package-lock.json`.

**agentpack is that missing piece.** It's a Rust CLI that pins GitHub-hosted skills and plugin directories for a project, resolves them to exact commits, and stages them into whichever agent harness you're launching — without ever touching your real `~/.claude`, `~/.codex`, or `~/.cursor`.

Think `uv` or `venv`, but for the configuration layer of AI coding agents.

## The two-file model

There are exactly two files in your repo:

- **`agentpack.toml`** — the source of truth for *what you want*. Direct dependencies, project-local "modes," and MCP server definitions.
- **`pack.lock`** — the resolved, pinned reality. Every package (direct *and* transitive), each with a 40-hex commit SHA and a content-addressed `cache_key`.

```toml
# agentpack.toml
[dependencies]
"github.com/anthropics/skills/skills/canvas-design" = { branch = "main" }
"github.com/anthropics/claude-plugins-official/plugins/hookify" = { version = "^1.0.0" }
mcp-retrieval = { path = "../mcp-retrieval" }

[mcp.servers.filesystem]
command = "npx"
args = ["-y", "@modelcontextprotocol/server-filesystem"]
```

Dependencies are identified by a Go-style module path — `github.com/<owner>/<repo>/<subdir>` — so a single repo can expose many skills and you pin each one independently. You can ask for a branch, a tag, a commit, or a semver range against tags. Identity always resolves down to the commit SHA; branch and tag names never make it into the lockfile's notion of identity.

Everything else — downloaded trees, the metadata index, an optional offline mirror — lives in a **user-wide home** (`$AGENTPACK_HOME`, defaulting to the XDG data dir), *not* in a `.agentpack/` folder polluting your repo. Cache entries are content-addressed, so the same `owner/repo/path/commit` resolves to the same `cache_key` and gets downloaded exactly once across all your projects.

## Storage: a content-addressed cache and an embedded index

There are two storage layers under `$AGENTPACK_HOME`, and they do different jobs.

**The cache is content-addressed on disk.** Every package tree lands in `cache/<cache_key>/`, where

```
cache_key = hex(SHA256(stable_source_identity + resolved_commit_sha))
```

The identity is normalized first — `https://github.com/...` tree URLs, `owner/repo/path` specs, and `local/` paths that point at the same `owner / repo / in-repo path / commit` all collapse to the **same** key, so a given pinned package is fetched exactly once and shared across every project on the machine. Because the key folds in the *resolved commit* (never a branch or tag name), two projects pinning `main` at the same SHA dedupe, and a project that floats `main` to a new SHA simply gets a new directory rather than mutating the old one. Filesystem (`path = "..."`) deps are content-hashed too, so editing a local dependency is detected and re-copied on the next `sync`.

**The index is a single embedded `redb` database** — `cache/db.reddb`, a pure-Rust, single-file, ACID key-value store with no server process and no daemon. (The docs call it "RedDB"; it's the [`redb`](https://github.com/cberner/redb) crate.) Writes go through real transactions (`begin_write` → `insert` → `commit`); lookups use read transactions. It holds four tables, each value a small `serde_json` blob:

| Table | Key | Value | Purpose |
| --- | --- | --- | --- |
| `cache_entries` | `cache_key` | `{kind, source_url, owner, repo, path, commit, fetched_at}` | what's in the cache, by content key |
| `aliases` | lowercased shorthand (`anthropics/skills/canvas-design`) | `cache_key` | resolve a repeat `add` **offline**, before any network call |
| `github_ref_cache` | `owner\0repo\0ref` | `{sha, checked_at}` | ref→commit lookups, **15-min TTL** |
| `github_tag_cache` | `owner\0repo` | `{tags, checked_at}` | tag listings for semver matching, **60-min TTL** |

The split is the point. The **cache directory** is the heavy, immutable content — keyed so it's never fetched twice. The **redb index** is the small, mutable metadata that makes the cache fast and offline-friendly: the alias table means re-adding a package you've seen before never touches GitHub, and the two TTL-bounded ref/tag tables collapse the GitHub REST traffic during `add` / `lock` / `sync`. When those tables are stale *and* the REST API is throttling, resolution falls back to the Git protocol (`ls-refs` via embedded `gix`) rather than trusting an expired row — so a rate-limit on github.com degrades to "slightly slower," not "can't resolve."

Nothing in here is a service you run or a schema you migrate. It's one file you can delete; the next command rebuilds it from the cache and the network.

## The interesting part: staging, not symlinking

Here's the design decision that makes agentpack actually usable: **it never copies pack content into your git workspace, and never symlinks project-specific pins into your global `~/.claude` or `~/.cursor`.** Doing either would leak one project's pins into your whole userspace — the exact problem that makes "just copy the files" unmaintainable.

Instead, `agentpack sync` builds an ephemeral **staging directory** per project and rewrites it into each harness's native layout. Then the launcher points the harness at it:

| Harness | How it's redirected |
| --- | --- |
| **Claude Code** | `claude --plugin-dir <staged-bundle>` (additive — your user skills still load) |
| **OpenCode** | `OPENCODE_CONFIG_DIR=<staged-root>` |
| **Codex** | `CODEX_HOME=<staged-home>` |
| **Grok** | `GROK_HOME=<staged-home>` (the CLI rejects `--plugin-dir`, so we override home and write plugin paths into `config.toml`) |
| **Cursor** | `HOME=<staged-cursor-home>` with symlinks back to your real credential/session files |
| **Antigravity** | workspace plugin symlink `./.agents/plugins/agentpack-bundle` (no config-root override exists) |

Each harness has a wildly different extension model — Claude's `--plugin-dir` is purely additive, OpenCode and Codex want you to *replace* their config root, Grok and Antigravity have neither — and a big chunk of agentpack is the per-harness research to make "the same pack" show up correctly in all six. The full gory details live in the project's `AGENTS.md`; the short version is that we reverse-engineered each one's discovery rules so your pinned content lands where the tool actually looks.

## Artifact conversion

You don't get six copies of every skill written six ways. agentpack parses supported markdown artifacts from cached pack content and **re-renders them per target harness**:

- **Commands** become Cursor plain-markdown, OpenCode frontmatter-markdown, Claude command frontmatter, or Codex skill fallback.
- **Agents** become Claude/OpenCode/Cursor agent markdown, or a Codex skill fallback.
- **Skills** are normalized and rendered as target skills.
- **Rules** stay rules on Cursor and Antigravity (which have first-class rule files); everyone else gets a best-effort skill fallback that notes the original rule scope.

So one upstream skill definition fans out into whatever the launched agent can actually consume.

## MCP, merged

MCP servers come from three places — plugin `mcp.json` files, your `[mcp.servers]` manifest section, and a project `./.agents/mcp.json` — and agentpack merges them (later wins on name collision) and writes the result in each harness's native dialect: JSON `mcpServers` for Claude and Cursor, `opencode.json` for OpenCode, `[mcp_servers]` TOML for Codex and Grok, and `mcp_config.json` for Antigravity. For Cursor's faked HOME, the merged set is further merged with your real `~/.cursor/mcp.json` so your personal servers coexist with the project's.

## Modes

A `[modes.<name>]` block is a staging preset: `base = "all" | "none"` plus `enable`/`disable` selectors like `package:...`, `package-path:...`, `mcp:...`, and `.agents:...`. Want a "design" mode that drops the filesystem MCP server, or a lean mode that strips a noisy command out of an otherwise-vendored pack? That's a few lines, and it applies at staging time without re-resolving anything.

## A couple of things we sweat that you'd never think about

- **Login survival.** Claude Code namespaces its macOS keychain entry by a hash of `CLAUDE_CONFIG_DIR`. If agentpack redirected that per-project (the "obvious" move), you'd be logged out on every project switch and every reboot. So we *deliberately don't* set it — instead we force attribution off via a stable `--settings` overlay file that loads at `flagSettings` precedence. Codex stores OAuth in the keychain keyed by the canonical `CODEX_HOME` path, so we link staged homes to a shared `auth.json` and force file-based credential storage so refresh-token updates propagate across projects.
- **Attribution off by default.** Every staged harness gets "Co-Authored-By" trailers and "Generated with X" footers disabled, so vendoring an agent config doesn't silently start tagging your commits. `AGENTPACK_KEEP_ATTRIBUTION=1` opts back in. Your real configs are never modified — only the staging copies.
- **Fast path.** Launchers skip the full resolve/re-download/rebuild when `agentpack.toml`, `pack.lock`, and `./.agents/` are all unchanged since the last successful launch — they just verify cache + staging integrity. (Floating pins therefore don't advance on launch alone; run `sync` or `lock` when you want them refreshed. That's intentional — launching shouldn't silently move your dependencies.)
- **No REST dependency for resolution.** GitHub ref→commit and tag lookups are cached in a local embedded DB, and when the REST API throttles, agentpack falls back to the Git protocol (`ls-refs` via embedded `gix`) before ever trusting stale metadata.

## Hooks emulation: one event model, six runtimes

Hooks are where the harnesses diverge the most, and where agentpack does the most work. Claude Code has the richest hook system — lifecycle **events** (`PreToolUse`, `PostToolUse`, `UserPromptSubmit`, `Stop`, `SubagentStop`, `SessionStart/End`, `PreCompact`, `PermissionRequest`, `Notification`), tool **matchers** (`Edit|Write`, globs), and four **handler** types (run a command, call an HTTP endpoint, inject a prompt, spawn an agent). Most other CLIs support some subset, with different event names, coarser matchers, and fewer handler types.

agentpack treats **Claude's model as the canonical IR**. Every hook is normalized to a `(event, matcher, handler, working_dir)` spec, and each harness gets a renderer that maps that IR onto its native lifecycle — emulating whatever the target can't do natively via a small shim binary, `agentpack hook-exec`:

- **Claude** — rendered nearly natively. The one twist: `command` handlers are wrapped in `agentpack hook-exec command` so they run with the **package-relative working directory** the hook was authored against, not wherever you launched from.
- **Cursor** — Cursor's matcher model is coarser (no globs, no `Edit|Write` alternations). Rather than lossily down-translating each matcher, agentpack installs **one blanket dispatcher per Cursor lifecycle event** (`preToolUse`, `postToolUse`, `beforeSubmitPrompt`, …) that calls `agentpack hook-exec dispatch`. At runtime the shim reads Cursor's stdin, normalizes the tool name back to Claude's vocabulary, and fires only the specs whose **original Claude matcher** matches. Event names are remapped (`UserPromptSubmit → beforeSubmitPrompt`, `PermissionRequest → preToolUse` fail-closed); `Notification` has no Cursor equivalent and is dropped.
- **OpenCode** — has no command-hook config at all; hooks are JS plugins. So agentpack **generates a Node.js plugin** (`plugins/agentpack-hooks/`) whose `index.js` shells out to `agentpack hook-exec` and maps events to OpenCode's (`tool.execute.before/after`, `permission.ask`, `chat.message`, `experimental.session.compacting`).
- **Codex** — native-ish JSON hooks at the home root; `prompt` handlers render natively, the rest are wrapped in the shim. Hooks that were *seeded from the user's real Codex config* pass through unwrapped.
- **Grok & Antigravity** — **not staged yet, on purpose.** In smoke tests, Grok only loaded hooks from HOME/project-trusted roots and Antigravity's plugin-local hook runtime isn't verified — so agentpack refuses to synthesize hook configs it can't stand behind, rather than emit something that silently no-ops. The capability is marked `Unsupported` with the reason in-code.

The payoff: you author a hook once in Claude's format, and it runs — with matcher semantics preserved — on four different runtimes, two of which have no comparable hook system of their own.

## Compatibility matrix

What actually reaches each harness, and how it's wired in. "Skill fallback" means the harness has no first-class concept for that content type, so it's rendered as a skill (with the original scope noted).

| | **Claude** | **Cursor** | **Codex** | **OpenCode** | **Grok** | **Antigravity** |
|---|---|---|---|---|---|---|
| **Redirect** | `--plugin-dir` (additive) | `HOME` rewrite + symlinks | `CODEX_HOME` | `OPENCODE_CONFIG_DIR` | `GROK_HOME` + `[plugins].paths` | workspace symlink + `--add-dir` |
| **Skills** | ✅ native | ✅ native | ✅ native | ✅ native | ✅ native | ✅ native |
| **Commands** | ✅ native | ✅ native | ⚠️ skill fallback | ✅ native | ✅ native | ✅ native |
| **Agents / subagents** | ✅ native | ✅ native (workspace symlink) | ⚠️ skill fallback | ✅ native | ✅ native | ✅ native |
| **Rules** | ⚠️ skill fallback | ✅ native | ⚠️ skill fallback | ⚠️ skill fallback | ✅ native | ✅ native |
| **MCP** | JSON `mcpServers` | JSON (+ merge real `~/.cursor`) | `[mcp_servers]` TOML | `opencode.json` | `[mcp_servers]` TOML | `mcp_config.json` (`serverUrl`) |
| **Hooks** | ✅ native (+shim wrap) | ✅ emulated (dispatcher) | ✅ native (+shim) | ✅ emulated (JS plugin) | ❌ not staged | ❌ not staged |
| **Attribution-off** | `--settings` overlay (keeps keychain) | `cli-config.json` (real file) | `commit_attribution=""` | config + prompt file | prompt-level (`AGENTS.md`) | prompt-level (rule) |
| **Auth survival** | reuse user keychain (no redirect) | symlink real session/trust | shared `auth.json` link | seed `~/.config/opencode` | link real `auth.json` | untouched real profile |

Reading the matrix: **Claude** is the richest target (it's the IR's home). **Cursor** matches it on content but pays for it with the HOME-rewrite + symlink gymnastics. **Codex** is the most reduced — commands, agents, and rules all collapse into skills, since that's the only portable artifact Codex exposes. **Grok** and **Antigravity** are the newest integrations and the most conservative — full content, but hooks deliberately held back until the runtime behavior is verified.

## The honest part

This is **pre-release and explicitly has no backwards-compatibility guarantee.** CLI behavior, the lockfile shape, the staging layout, env vars, and defaults can all change without a migration window. The harness integrations are reverse-engineered against specific CLI versions (e.g. `grok 0.1.219`), so a harness shipping a breaking change can break a launcher. Codex currently only receives the *portable skill* subset of pack content — we don't synthesize Codex plugin marketplaces from Claude plugins. Some auth lives in OS keychains or paths we don't redirect.

In other words: it works, it's useful today, and it's moving fast.

## Try it

```bash
agentpack init                      # stub agentpack.toml + v2 pack.lock
agentpack add github.com/anthropics/skills/skills/canvas-design
agentpack claude                    # or: opencode / codex / grok / agent / agy
```

`add` edits the manifest, resolves, writes the lock, and stages. The launcher syncs (fast path when nothing changed) and execs the real agent with the staged roots wired up. Your pinned skills, commands, agents, rules, and MCP servers show up — in whichever agent you launched — and nothing leaked into your global config.

A lockfile for your agents. That's the whole pitch.
