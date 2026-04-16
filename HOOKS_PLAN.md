# Hooks Compatibility Layer Plan

## Objective

Build an ultra-compatible hooks compiler for agentpack where the pack-facing source format is exactly Claude Code hooks JSON, and agentpack compiles that 1:1 Claude-compatible input into the correct harness-specific hook/runtime configuration for Claude, Cursor, Codex, and OpenCode.

Claude remains the source-of-truth authoring model. agentpack does not invent a second hook language. The work is to preserve that contract while providing the strongest possible native mapping and emulation for the other harnesses.

## Hard Constraints

- Pack-authored hooks must stay 100% Claude Code compatible.
- No custom canonical hook schema.
- No agentpack-only hook fields such as custom `version`, `id`, `priority`, `native`, or other authoring-time extensions inside `hooks/hooks.json`.
- Do not cut Claude handler types. `command`, `http`, `prompt`, and `agent` all remain in scope.
- Do not silently drop enforcement behavior. If a Claude hook cannot be represented safely for a target and the behavior is strict, sync must fail for that target.
- Keep hook logic in a dedicated Rust domain module and let staging orchestrate it.

## Correction To The Previous Draft

The previous rewrite went wrong in one key place: it introduced a custom canonical schema and treated Claude as just one renderer. That is incorrect for this project.

The correct model is:

1. Claude hooks JSON is the canonical pack input.
2. agentpack normalizes that Claude structure into an internal IR.
3. agentpack renders target-specific native configs and bridge shims from that IR.
4. Other harnesses are compatibility targets, not alternate authoring schemas.

## Source Of Truth

Pack-owned hook files are authored exactly as Claude Code expects them today.

Expected location:

- `hooks/hooks.json`

Expected authoring shape:

```json
{
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "Bash",
        "hooks": [
          {
            "type": "command",
            "command": "./scripts/validate.sh"
          }
        ]
      }
    ],
    "Stop": [
      {
        "hooks": [
          {
            "type": "prompt",
            "prompt": "Did the agent complete the task correctly? Reply with JSON."
          }
        ]
      }
    ]
  }
}
```

Rule:

- If a Claude user could point Claude at the same `hooks/hooks.json` and have it parse, the file is valid for agentpack.
- Any metadata agentpack needs must be derived from origin, declaration order, manifest overrides, or external staging context, not by extending the source schema.

## What agentpack Is Actually Building

agentpack is a Claude-hooks compiler plus runtime bridge:

```text
Claude-compatible hooks/hooks.json
          |
     parse + normalize
          |
    capability evaluation
          |
   +------+------+------+------+
   |Claude|Cursor|Codex |OpenCode
   |native|compile|compile|compile
   |render|+ shim |+ shim |+ JS bridge
   +------+------+------+------+
```

Claude:

- mostly native render
- minimal transformation

Cursor, Codex, OpenCode:

- compile from Claude event/handler semantics into target-native config
- use `agentpack hook-exec` where a Claude handler must be emulated

## Verified Harness Reality

This plan should be treated as verified against upstream docs/source as of 2026-04 and rechecked during implementation.

### Claude

- Plugin-local config at `hooks/hooks.json`
- Native handler types: `command`, `http`, `prompt`, `agent`
- Richest lifecycle model
- Some events are command-only, so support must be evaluated per event and handler type

### Codex

- Singleton config at `CODEX_HOME/hooks.json`
- Limited native lifecycle surface compared to Claude
- Command-oriented hook schema
- Upstream support is experimental, so capability checks and diagnostics matter

### OpenCode

- Plugin-based JS hook model
- Useful lifecycle surface plus event-bus hooks
- Not a credible parse target for arbitrary existing JS plugins
- Best treated as a render target from Claude IR

### Cursor

- Native lifecycle is **documented** for the Cursor IDE; the **Cursor Agent CLI** (`agent`) ships the same hook engine in practice (bundled `hooks` / `hooks-exec` modules).
- Behavior is **capability-driven** for agentpack because Claude and Cursor differ on events, handler types, merge order, and a few semantic details (see below).
- Reverse-engineered specifics below come from Cursor’s published docs plus the **`agent-cli@2026.03.30-a5d3e17`** bundle on disk (representative of the 2026-03 Cursor Agent line); re-verify on newer CLI drops.

#### Config discovery and precedence (Agent CLI)

The CLI loads hook configuration from multiple files, then **runs every matching hook script** for a step and **merges** results (see merge semantics).

**Load order when collecting hooks for a step** (each tier is filtered by matchers independently):

1. Enterprise — `hooks.json` at OS-specific path (macOS: `/Library/Application Support/Cursor/hooks.json`; Windows: `C:\ProgramData\Cursor\hooks.json`; Linux/WSL: `/etc/cursor/hooks.json`).
2. Team — dashboard-managed path (`teamConfigPath` in loader; content synced for Enterprise).
3. Project — `<workspace>/.cursor/hooks.json` (optional; can skip project hooks with `loadProjectHooks: false`).
4. User — `~/.cursor/hooks.json`.
5. Claude project-local — `<workspace>/.claude/settings.local.json` (hooks extracted and normalized).
6. Claude project — `<workspace>/.claude/settings.json`.
7. Claude user — `~/.claude/settings.json`.

**Documentation vs implementation:** Cursor’s [third-party hooks](https://cursor.com/docs/reference/third-party-hooks) doc states priority as Enterprise → Team → **Project (`.cursor`)** → **User (`~/.cursor`)** → Claude local → Claude project → Claude user. The Agent CLI **collects hooks in the order above** when building the per-step list; **merge** (not collection order) determines how conflicting `permission` / `continue` values combine.

**Parsing:**

- **Native** `.cursor`/`Cursor` `hooks.json`: **JSONC** — `//` line comments and `/* */` block comments are stripped before `JSON.parse`.
- **Claude** `settings.json`: plain `JSON.parse` (no JSONC in that path).

**Deduping Claude against Cursor:** After load, any **Claude-sourced** hook entry whose normalized key (`command:<string>` or `prompt:<string>`) already appears under the same step in **Enterprise, Team, User, or Project** native hooks is **removed** (logged as duplicate). Claude hooks never dedupe against each other across the three Claude files beyond normal per-file structure.

#### Claude → Cursor mapping (CLI transform)

The bundle embeds an explicit event and tool map used when ingesting Claude-format `settings.json`:

| Claude event | Cursor step | Notes |
|--------------|-------------|--------|
| `PreToolUse` | `preToolUse` | |
| `PostToolUse` | `postToolUse` | |
| `UserPromptSubmit` | `beforeSubmitPrompt` | |
| `Stop` | `stop` | |
| `SubagentStop` | `subagentStop` | |
| `SessionStart` | `sessionStart` | |
| `SessionEnd` | `sessionEnd` | |
| `PreCompact` | `preCompact` | |
| `Notification` | — | **Dropped** (warn) |
| `PermissionRequest` | — | **Dropped** (warn) |

**Tool matchers (`PreToolUse` / `PostToolUse`):** Claude matcher strings are split on `|`; segments are mapped where present (e.g. `Bash` → `Shell`, `Edit` → `Write`). **`mcp__server__tool`**-style segments become `MCP:…` matcher tokens. **Glob** is called out as unsupported (warning, can skip entire group if no tools left). **`SessionStart` / `PreCompact`:** Claude trigger-specific matchers (`startup`, `manual`, etc.) are **not** modeled; the CLI **warns** and runs hooks for all triggers.

**Shape gate:** Claude settings are only treated as carrying hooks if the JSON matches an expected grouped shape (`hooks` object with known event keys and `{ matcher?, hooks: [...] }` entries). `Stop` hooks may allow matcherless groups when `allowMatcherlessStop` is set during detection.

#### Native Cursor hook schema (validated in CLI)

- Top-level **`version`**: positive integer (typically `1`).
- **`hooks`**: object whose keys are **known step names** only; unknown keys are validation errors.
- Per-script: **`command`** (string) and optional **`type`** (`"command"` default, `"prompt"`), **`prompt`** + optional **`model`** for prompt hooks, **`timeout`** (seconds, positive, max 3600 warns), **`matcher`** (string, must be valid **RegExp** syntax or empty / `*`), **`loop_limit`** (positive integer or `null`; top-level **`stop_hook_loop_limit`** is deprecated and ignored), **`failClosed`** (boolean).

**Handler coverage vs Claude packs:** Native validation is **command + prompt only**. Claude’s **`http`** and **`agent`** handlers must be **compiled to Cursor command** (e.g. `agentpack hook-exec …`) or flagged unsupported — the CLI’s Claude importer does not preserve those as first-class Cursor types.

#### stdin / subprocess / environment (command hooks)

**Important implementation detail:** Command hooks are **not** spawned as `your-script` with raw stdin piped from the parent. The shell runs:

```text
<command from hooks.json> <<'CURSOR_HOOK_EOF'
<single JSON object: full hook request payload>
CURSOR_HOOK_EOF
```

So the child process receives JSON on **stdin** via a heredoc attached by the shell wrapper. Working directory depends on hook **source** (see below). Sandbox policy for that execution is explicitly **`insecure_none`** (hooks run outside the user’s tool sandbox).

**Environment variables** injected for command hooks (plus process env): `CURSOR_PROJECT_DIR` (workspace), `CURSOR_VERSION`, optional `CURSOR_USER_EMAIL`, `CURSOR_TRANSCRIPT_PATH` when a transcript file exists for the conversation, `CLAUDE_PROJECT_DIR` (same as workspace), and any **session** env merged via `setSessionEnvironment`.

**Working directory by source:**

| Source | CWD for `command` |
|--------|---------------------|
| enterprise | Enterprise config directory, else workspace |
| team | Team hooks directory, else workspace |
| project | Workspace |
| user | `~/.cursor` (user config dir) |
| claude-project / claude-project-local / claude-user | Workspace (for project/local) or `~/.claude` (user), per `configDirs` |

#### Payload envelope (all steps)

Every hook request is a JSON object. The CLI **merges** tool-specific fields with a common envelope including:

- `conversation_id`, `generation_id`, `model` (when provided by caller)
- `session_id` — alias of `conversation_id` if absent
- `hook_event_name` — Cursor step string (e.g. `preToolUse`)
- `cursor_version`
- `workspace_roots` — `[workspacePath]` (single-root today in CLI)
- `user_email` — nullable
- `transcript_path` — resolved path to main transcript when available; else `null`
- `agent_transcript_path` — for `subagentStop`, subagent transcript when available

Official docs mirror this “common schema”; the CLI always injects the above before spawning.

#### Exit codes and `failClosed` (command hooks)

Aligned with docs and Claude parity:

| Exit | Behavior |
|------|----------|
| `0` | Success: **stdout** must contain JSON (may be empty only if `failClosed` handles empty output as block). |
| `2` | Treated as **block** even if JSON is missing — mapped to step-appropriate `permission: deny` / `continue: false` via the same path as a denied JSON response. |
| Other non-zero | **Fail-open** unless `failClosed: true` on that script, in which case the action is blocked with a generic “hook failed / fail-closed” message. |
| Timeout / throw | Same as non-zero: fail-open unless `failClosed`. |

**`failClosed` scope caveat (important for agentpack):** `hasFailClosedHooksForStep` — used to decide whether a **thrown** error during `preToolUse` should hard-block the tool — only inspects **Enterprise, Team, Project, and User** native Cursor hooks. **`failClosed` on Claude-imported hooks is not consulted** in that predicate. So “fail closed on preToolUse error” behaves differently if hooks live only in `.claude/settings.json`.

#### Prompt hooks

- Require a **`promptHookClient`** on the executor; if missing, **prompt hooks are skipped** (no prompt, no block).
- Timeout defaults to the same **60s** default as command hooks unless overridden.
- Evaluation returns `{ ok, reason? }`; `ok: false` maps to the same blocking helpers as command deny.

#### Parallelism and merge semantics

- All hook scripts scheduled for a step after matcher filtering run **concurrently** (`Promise.all`).
- Results are merged with step-specific rules:
  - For “blocking” steps (`beforeShellExecution`, `beforeMCPExecution`, `beforeReadFile`, `beforeTabFileRead`, `subagentStart`, `preToolUse`): **`permission`** combines as **deny > ask > allow**.
  - **`user_message` / `agent_message`**: concatenated with `\n\n---\n\n`.
  - **`sessionStart`**: `env` objects shallow-merged; `additional_context` strings concatenated; `continue` ANDed (both must be true to continue).

#### Tool integration semantics (Agent CLI — not always1:1 with event names)

- **`preToolUse` / `postToolUse` / `postToolUseFailure`:** Generic tool wrapper supplies `tool_name`, `tool_input`, `tool_output` (stringified JSON for post), `tool_use_id`, `duration`, `failure_type`, etc. **`preToolUse` `updated_input`** is applied when valid (shell/MCP paths apply patches to live tool args).
- **Shell:** Also fires **`beforeShellExecution`** / **`afterShellExecution`** with `command`, `cwd`, **`sandbox`** (boolean derived from whether the tool sandbox policy is non-none), and aggregated `output` / `duration` for after.
- **Read:** **`beforeReadFile`** is invoked from the **post-read** path after a **successful** read, with `content`, `file_path`, `attachments` — it gates **exposure of content**, not OS `open()` itself. Fail-closed wrapper can throw if the hook errors.
- **Write:** **`afterFileEdit`** runs after a successful write with an **`edits`** array (`old_string` / `new_string` hunks); may return structured result to refresh file content in the tool result when requested.
- **MCP:** Runs **`preToolUse`** with `tool_name` like `MCP:toolName`, then **`beforeMCPExecution`** / **`afterMCPExecution`** with serialized args/results; supports **`updated_mcp_tool_output`** rewriting model-visible MCP content on **`postToolUse`**.

#### Plugin bundles (`hooks/hooks.json`)

Separate discovery in the main bundle walks **plugin manifests**: if a manifest declares `hooks`, those paths are loaded; else if the plugin root contains `hooks/hooks.json`, that file is used. Staged Cursor plugins (e.g. agentpack’s `hooks/` tree) should assume this layout for IDE/CLI parity.

#### Cursor tool hook coverage (verified from `agent-cli@2026.03.30-a5d3e17`)

Only tools with a **HooksExecutorAccessor wrapper** fire `preToolUse` / `postToolUse` / `postToolUseFailure`. Unwrapped tools fall through to the raw executor — **no hook fires**.

**Hooked tools** (wrapper exists, fires `preToolUse`/`postToolUse`):

| Cursor `tool_name` | Claude equivalent | Notes |
|---------------------|-------------------|-------|
| `Shell` | `Bash` | Two wrappers: streaming + background |
| `Write` | `Write` / `Edit` | Also fires `afterFileEdit` with edit hunks |
| `Read` | `Read` | Also fires `beforeReadFile` (post-read content gate) |
| `Grep` | `Grep` | |
| `List` | — | No Claude equivalent (ls tool) |
| `Delete` | — | |
| `Fetch` | `WebFetch` | **Name mismatch**: CLI mapper outputs `WebFetch` but wrapper fires as `Fetch` |
| `ComputerUse` | — | |
| `RecordScreen` | — | |
| `ReadLints` | — | |
| `BackgroundShell` | — | |
| `WriteShellStdin` | — | |
| `ListMcpResources` | — | |
| `FetchMcpResource` | — | |
| `MCP:<toolName>` | `mcp__server__tool` | Custom wrapper; also fires `beforeMCPExecution` / `afterMCPExecution` |

**Unhooked tools** (no wrapper — hooks **never fire**):

| Cursor tool | Claude equivalent | Impact |
|-------------|-------------------|--------|
| `Glob` | `Glob` | Claude `PreToolUse` matcher `Glob` is dropped by CLI with warning AND no event would fire even if passed through. |
| `WebSearch` | `WebSearch` | Mapper passes `WebSearch` through but no wrapper exists, so no event fires. |
| `SemanticSearch` | — | No Claude equivalent, no wrapper. |
| `UpdateTodos` / `ReadTodos` | — | No wrapper. |

**Matcher bugs / mismatches in the CLI's Claude transform:**

1. **`WebFetch` → wrong name**: Claude mapper outputs `WebFetch` but the hook wrapper fires `tool_name: "Fetch"`. Matchers silently never match.
2. **`Task`**: Mapper passes `Task` through. Docs list `Task` as valid matcher. However Task invocations go through the **subagent** path (`subagentStart`/`subagentStop`), not a preToolUse wrapper in the CLI bundle. Whether `preToolUse` fires for Task may depend on IDE-side vs CLI-side execution.

#### Emulation analysis: what agentpack can recover

**Constraint:** agentpack controls only `hooks.json` content and ships `agentpack hook-exec` as a bridge command. It cannot inject code into the Cursor CLI tool execution path. If a tool has no hook wrapper, no `hooks.json` entry can intercept it.

| Gap | Emulatable? | Mechanism | Level |
|-----|-------------|-----------|-------|
| **`Glob` tool matcher** | **No** | Tool has no hook wrapper; `preToolUse` never fires. | **Unsupported** |
| **`WebSearch` tool matcher** | **No** | No hook wrapper, no event fires. | **Unsupported** |
| **`WebFetch` tool matcher** | **Yes** | Rewrite matcher token from `WebFetch` to `Fetch`. | **Native (with fix)** |
| **`http` handler type** | **Yes** | Emit `agentpack hook-exec http <url> <method>` as `command`. | **Emulated** |
| **`agent` handler type** | **Yes** | Emit `agentpack hook-exec agent <config>` as `command`. | **Emulated** |
| **`prompt` handler type** | **Native** | Cursor supports `type: "prompt"` natively. Emit directly. | **Native** |
| **`PermissionRequest` event** | **Partial** | No direct Cursor event. Decompose into per-tool `preToolUse` hooks that deny/ask. Semantics differ: Claude fires for any permission prompt, Cursor `preToolUse` fires unconditionally before use. | **Degraded** |
| **`Notification` event** | **No** | Observational fire-and-forget. No Cursor event bus. | **Unsupported** |
| **`SessionStart` trigger matchers** | **No** | Cursor fires for all triggers; payload lacks type. | **Degraded (fires for all)** |
| **`PreCompact` trigger matchers** | **No** | Same — fires for all compactions. | **Degraded (fires for all)** |
| **`Glob` in multi-tool matcher** (e.g. `Bash\|Glob\|Read`) | **Partial** | Strip unsupported segment, emit remainder (`Shell\|Read`). Glob portion lost. Warn. | **Degraded** |
| **`WebSearch` in multi-tool matcher** | **Partial** | Same — strip and warn. | **Degraded** |

#### What the capability registry must encode

For each `(ClaudeEvent, matcher_tool, handler_type)` → `CursorStep`:

```text
PreToolUse + Bash      + command → preToolUse + Shell   : Native
PreToolUse + Glob      + command → —                    : Unsupported (no hook fires)
PreToolUse + WebFetch  + command → preToolUse + Fetch   : Native (matcher rewrite)
PreToolUse + WebSearch + command → —                    : Unsupported
PreToolUse + *         + http    → preToolUse + *       : Emulated (hook-exec http)
PreToolUse + *         + agent   → preToolUse + *       : Emulated (hook-exec agent)
PreToolUse + *         + prompt  → preToolUse + *       : Native (Cursor prompt hooks)
PermissionRequest + *            → preToolUse decomposed: Degraded
Notification + *                 → —                    : Unsupported
Stop + *               + command → stop                 : Native
Stop + *               + http    → stop                 : Emulated (hook-exec http)
SessionStart(startup)  + command → sessionStart (all)   : Degraded (trigger lost)
```

`SupportLevel` in `capabilities.rs` must distinguish:
- **Native**: direct Cursor mapping, no bridge needed
- **Emulated**: agentpack wraps it via `hook-exec`, semantics preserved
- **Degraded**: something fires but semantics differ (trigger matchers lost, event decomposed, etc.)
- **Unsupported**: tool never fires hooks on Cursor, cannot recover with `hooks.json` alone

#### Implications for agentpack Cursor renderer

1. **Emit Cursor-native `hooks.json`** (version + flat `hooks` map) into the staged plugin/config root; path and CWD rules above determine how `command` strings must be rewritten.
2. **Matcher rewriting is mandatory**: Claude `WebFetch` → Cursor `Fetch`. Claude `Glob` / `WebSearch` → **strip from matcher, warn** (or fail sync if the hook is strict/enforcement-capable and the stripped tool was the only segment).
3. **Multi-tool matchers**: When a Claude matcher like `Bash|Glob|Read` targets a mix of hookable and unhookable tools, strip unsupported segments, produce valid remainder (`Shell|Read`), emit diagnostic. If all segments are unsupported, omit the hook entry entirely with warning.
4. **`http` / `agent`** Claude handlers → `agentpack hook-exec http ...` / `agentpack hook-exec agent ...` as the `command` field. **`prompt`** → Cursor-native `type: "prompt"` directly.
5. **`PermissionRequest`**: When a Claude `PermissionRequest` hook targets specific tools with an enforcement handler, decompose into `preToolUse` entries for each supported tool. Emit diagnostic noting semantic difference. Tool-agnostic hooks → matcherless `preToolUse`.
6. **`Notification`**: Omit with clear diagnostic. No Cursor event to attach to.
7. **Sync failure for strict gaps**: If a Claude hook is enforcement-capable (blocking `PreToolUse` on `Glob`) and the tool is **Unsupported** on Cursor, sync **must fail** for the Cursor target with a clear message rather than silently dropping enforcement.
8. **Document** for users: parallel hook merge (deny > ask > allow) may differ from Claude ordering; `failClosed` on Claude-only configs may not match Cursor IDE behavior for `preToolUse` errors.
9. **Read hook timing**: Claude `PreToolUse` on `Read` is pre-execution gating; Cursor `beforeReadFile` is a **post-read content gate**. Capability matrix should call this out as **degraded / different timing**.
## Supported Surface Definition

When this plan says “supported events for a harness,” it means:

- Claude lifecycle events that can be represented natively or emulated safely on that harness

This does not mean agentpack will invent pack-authoring support for harness-native-only events with no Claude analog. The authoring contract remains Claude.

That still leaves a large scope:

- all Claude lifecycle events stay in the IR
- every target gets the maximum safe subset
- every non-native handler type gets explicit bridge/emulation treatment instead of being ignored

## Internal IR

The internal IR should normalize Claude’s grouped structure into a flat, origin-aware event stream while preserving declaration order.

Suggested core types:

```rust
pub struct HookBundle {
    pub hooks: Vec<NormalizedHook>,
}

pub struct NormalizedHook {
    pub event: ClaudeEvent,
    pub matcher: Option<String>,
    pub handler: ClaudeHandler,
    pub origin: HookOrigin,
    pub raw_extra: BTreeMap<String, serde_json::Value>,
}

pub struct HookOrigin {
    pub layer: HookLayer,
    pub module: String,
    pub cache_key: Option<String>,
    pub source_rel: String,
    pub event_index: usize,
    pub matcher_group_index: usize,
    pub hook_index: usize,
}
```

This IR stays Claude-shaped:

- event names are Claude event names
- handlers are Claude handler variants
- grouping is flattened only for processing convenience
- unrecognized Claude-native fields are preserved in `raw_extra` instead of invented as agentpack schema

## Merge Semantics

Because the source schema must remain 1:1 Claude-compatible, merge behavior cannot depend on extra authoring fields like `id` or `priority`.

### Source Layers

Hook sources should be collected in this conceptual order:

1. Seeded user-native singleton hook files for targets that require them
2. Pack plugins
3. Bare skills
4. Project `.agents/hooks/hooks.json`

### Ordering

Effective render order must be deterministic:

1. layer order
2. module id
3. source file path
4. event declaration order
5. matcher group order
6. hook order within matcher group

### Override Model

Because we are not extending Claude’s schema:

- pack-owned hooks are append-only within their source files
- later layers do not replace earlier hooks by per-hook ID
- if the user needs to suppress a package’s hooks, they do that through `agentpack.toml` overrides such as disabling `hooks/` or the containing package path

This keeps the authoring contract pure Claude while still making the final merged ordering deterministic.

## Strictness And Failure Rules

Support evaluation must be explicit and target-specific.

Suggested internal result type:

```rust
pub enum SupportLevel {
    Native,
    Emulated,
    Degraded { reason: &'static str },
    Unsupported { reason: &'static str },
}
```

Rule:

- enforcement-capable mappings that cannot be represented safely must fail sync for that target
- observational mappings may warn and omit when no analog exists

The strictness decision cannot rely on custom schema fields, so it must be inferred from the Claude hook semantics plus event/handler behavior:

- blocking `PreToolUse` / permission / stop-interrupt style flows are strict
- purely observational post-event hooks can degrade more safely

## Capability Registry

`src/hooks/capabilities.rs` should own the mapping truth table.

It must answer:

- is this Claude event supported on this target
- is this Claude handler supported natively for this target and event
- can this handler be emulated safely
- is the mapping degraded
- is the mapping unsupported

This must be evaluated per `(target, ClaudeEvent, handler_type)` tuple.

## Asset Staging And Path Rewriting

The previous generic raw-copy ownership of `hooks/` is not enough for a robust compiler.

Hook support files must be staged in namespaced per-origin locations to prevent cross-package collisions.

Suggested staged asset roots:

- Claude: `hooks/_packages/<cache_key-or-local-key>/...`
- Cursor: `hooks/_packages/<cache_key-or-local-key>/...`
- Codex: `hooks/_packages/<cache_key-or-local-key>/...`
- OpenCode: `plugins/agentpack-hooks/assets/<cache_key-or-local-key>/...`

Important constraint:

- do not require new placeholders in source hook files
- path rewriting happens during render based on origin metadata

That means:

- if a Claude command references a package-relative script or asset, the renderer resolves it to the correct target-specific staged path
- authors keep writing normal Claude hook commands

## Rust Structure

This work should live in a dedicated `src/hooks/` domain module. Staging calls into it; renderers and runtime executors stay isolated.

### Module Graph

```text
src/staging/*             -> orchestration only
src/hooks/stage.rs        -> collection + evaluation + render orchestration
src/hooks/collect.rs      -> source discovery
src/hooks/merge.rs        -> deterministic ordering
src/hooks/paths.rs        -> namespaced staged paths + command/path rewriting
src/hooks/capabilities.rs -> support matrix
src/hooks/ir.rs           -> normalized Claude-shaped types
src/hooks/render/*        -> per-target renderers
src/hooks/runtime/*       -> hook-exec bridge
```

### Concrete Layout

```text
src/hooks/
  mod.rs
  ir.rs
  capabilities.rs
  collect.rs
  merge.rs
  paths.rs
  stage.rs
  parse/
    mod.rs
    claude.rs
    cursor.rs
    codex.rs
  render/
    mod.rs
    claude.rs
    cursor.rs
    codex.rs
    opencode.rs
  runtime/
    mod.rs
    bridge.rs
    command.rs
    http.rs
    prompt.rs
    agent.rs
    translate.rs
```

### Pattern Choice

Use Strategy-style traits rather than a giant target switch:

```rust
trait HookRenderer {
    fn target(&self) -> HarnessTarget;
    fn render(&self, bundle: &HookBundle, ctx: &RenderContext) -> Result<RenderedHookOutput>;
}

trait HookExecutor {
    fn execute(&self, req: HookExecutionRequest) -> Result<NormalizedHookResult>;
}
```

This aligns with the existing strategy pattern already used in `src/staging/harnesses.rs`.

## Renderers

Each target gets a dedicated renderer module. The source semantics stay Claude-shaped; only the output differs.

### Claude Renderer

Responsibilities:

- render back to Claude-native grouped JSON
- preserve handler types directly
- keep output plugin-local at `hooks/hooks.json`
- apply only the minimal transformation needed for namespaced staged paths and merged ordering

### Cursor Renderer

Responsibilities:

- compile Claude events into the supported Cursor hook surface
- use native Cursor hook fields where they exist
- derive fine-grained Cursor hooks from Claude semantics when safe
- wrap non-native Claude handler types with `agentpack hook-exec`

Important rule:

- Cursor is a render target, not the source schema

### Codex Renderer

Responsibilities:

- compile the supported Claude subset into singleton `${codex_home}/hooks.json`
- enforce any Codex-specific limitations during capability evaluation
- wrap non-command Claude handlers with `agentpack hook-exec`
- merge seeded user Codex hooks if user settings seeding is enabled

Important rule:

- singleton ownership means this renderer is responsible for the full final file

### OpenCode Renderer

Responsibilities:

- generate an agentpack-owned plugin under `${opencode_root}/plugins/agentpack-hooks/`
- compile Claude semantics into JS hook registrations and helper config
- use event-bus subscriptions internally where needed to emulate Claude lifecycle points
- wrap non-native Claude handlers with `agentpack hook-exec`

Important rule:

- OpenCode remains a target compiler output, not a source parse format for pack hooks

## Runtime Bridge

`agentpack hook-exec` remains the bridge for handler-type emulation.

### CLI Shape

```text
agentpack hook-exec command ...
agentpack hook-exec http ...
agentpack hook-exec prompt ...
agentpack hook-exec agent ...
```

### Responsibilities

- `bridge.rs`: stdin/stdout contract
- `command.rs`: subprocess execution
- `http.rs`: HTTP execution
- `prompt.rs`: model-backed prompt execution
- `agent.rs`: real agent execution, not a stub
- `translate.rs`: normalized result -> target wire format

### Normalized Result Type

All bridge executors should return a target-neutral result first:

```rust
pub struct NormalizedHookResult {
    pub decision: HookDecision,
    pub message: Option<String>,
    pub additional_context: Option<String>,
    pub updated_input: Option<serde_json::Value>,
    pub updated_tool_output: Option<serde_json::Value>,
    pub metadata: BTreeMap<String, serde_json::Value>,
}
```

### Agent Handler Rule

There is no stub fallback to `prompt`.

If an `agent` hook must be emulated:

- implement a real executor backend
- if no backend is configured, fail or omit according to target safety evaluation

## Ingestion Model

Pack-facing inputs:

1. `hooks/hooks.json` in Claude format from pack plugins
2. Claude-format `.agents/hooks/hooks.json`

Supplemental preservation inputs:

1. seeded user singleton `hooks.json` for Codex if user settings seeding is enabled
2. optionally other target-native singleton files if we explicitly decide to preserve them later

Important rule:

- foreign target-native files may be normalized for preservation/merge
- they are not the pack authoring contract

## Staging Pipeline Changes

The hooks compiler becomes a first-class staging phase.

### New Order

In `src/staging/harnesses.rs`:

1. prepare harness roots
2. stage pack plugins
3. stage pack skills
4. stage hooks across all harnesses
5. stage remaining `.agents/` overlay, but route `.agents/hooks` through the hooks compiler instead of raw copy
6. finalize harness roots

### Why This Order

By step 4 we already know:

- which pack sources exist
- which staged roots exist
- which user seed files have been copied

That is exactly when hook collection, capability evaluation, asset namespacing, and target rendering should happen.

## Required Changes To Existing Files

### `src/lib.rs`

- add `pub mod hooks;`

### `src/staging/harnesses.rs`

- call `hooks::stage::stage_hooks_all_harnesses(...)`

### `src/staging/dot_agents.rs`

- stop raw-copying `.agents/hooks`
- route `.agents/hooks/hooks.json` through hook collection and render instead

### `src/staging/constants.rs`

- add `hooks.json` to `CODEX_HOME_ENTRIES`
- keep Cursor user hook seeding conservative until verified

### `src/staging/seed.rs`

- seed Codex singleton `hooks.json` when user settings seeding is enabled
- treat it as compiler input, not authoritative final output

### `src/artifacts/harness.rs`

End-state intent:

- remove generic ownership of hook entrypoint config from `raw_plugin_subdirs()`
- move hook config generation into `src/hooks/stage.rs`

Safe incremental path:

- temporary raw copy can remain during bring-up
- final rendered hook outputs must overwrite the target entrypoints deterministically

## Diagnostics

Sync should produce a hook summary per target:

- Claude hooks rendered natively
- hooks rendered through bridge emulation
- degraded mappings
- omitted observational mappings
- failed strict mappings

Diagnostics must include source file and module identity so the user can act on them.

## Implementation Phases

### Phase 0: IR And Capability Registry

Files:

- `src/hooks/ir.rs`
- `src/hooks/capabilities.rs`
- `src/hooks/mod.rs`

Deliverables:

- Claude-shaped normalized IR
- target capability registry

### Phase 1: Parsing And Collection

Files:

- `src/hooks/collect.rs`
- `src/hooks/parse/mod.rs`
- `src/hooks/parse/claude.rs`
- `src/hooks/parse/cursor.rs`
- `src/hooks/parse/codex.rs`

Deliverables:

- pack-owned Claude parser
- supplemental foreign-format parsers where needed for seeded singleton preservation
- origin-aware collection across pack plugins, bare skills, `.agents`, and seeded singleton files

### Phase 2: Merge And Namespaced Assets

Files:

- `src/hooks/merge.rs`
- `src/hooks/paths.rs`

Deliverables:

- deterministic merged ordering
- target-safe namespaced asset staging
- command/path rewriting without changing authoring schema

### Phase 3: Claude And Codex Renderers

Files:

- `src/hooks/render/mod.rs`
- `src/hooks/render/claude.rs`
- `src/hooks/render/codex.rs`
- `src/hooks/stage.rs`

Deliverables:

- first full end-to-end target outputs
- Codex singleton ownership solved early

### Phase 4: OpenCode Renderer

Files:

- `src/hooks/render/opencode.rs`

Deliverables:

- generated OpenCode plugin
- Claude-to-OpenCode event compilation

### Phase 5: Cursor Renderer

Files:

- `src/hooks/render/cursor.rs`

Deliverables:

- conservative verified Cursor mapping
- derived fine-grained hook emission where safe

### Phase 6: Runtime Bridge

Files:

- `src/cli/hook_exec.rs`
- `src/cli/mod.rs`
- `src/cli/dispatch.rs`
- `src/hooks/runtime/*`

Deliverables:

- shared hook bridge
- target wire-format translation
- real `command`, `http`, `prompt`, and `agent` executors

### Phase 7: Pipeline Integration And Cleanup

Files:

- `src/staging/harnesses.rs`
- `src/staging/dot_agents.rs`
- `src/staging/constants.rs`
- `src/staging/seed.rs`
- `src/artifacts/harness.rs`

Deliverables:

- first-class hooks compiler phase in staging
- removal of stale raw hook ownership

## Test Plan

### Unit Tests

- parse Claude-native hook files
- normalize event/matcher/hook ordering correctly
- capability checks per `(target, event, handler)`
- path rewriting and namespaced staged asset roots
- renderer snapshots for Claude, Cursor, Codex, and OpenCode
- runtime bridge output translation per target

### Integration Tests

- multiple packages each shipping Claude-format hooks
- `.agents/hooks/hooks.json` layered on top of pack content
- Codex singleton merge with seeded user `hooks.json`
- cross-package asset collision proof
- target failure when strict Claude behavior cannot be represented safely

### Manual Verification

- `agentpack sync`
- launch Claude, Cursor, Codex, and OpenCode against staged outputs
- verify `http`, `prompt`, and `agent` bridge paths
- verify strict failures are explicit, not silent

## Completion Criteria

This work is complete only when all of the following are true:

- pack-authored hooks remain valid Claude Code hooks JSON
- the same hook source can be compiled to all four harnesses
- non-native handler types are emulated explicitly instead of ignored
- strict Claude behavior never disappears silently on non-Claude harnesses
- Codex singleton ownership is solved
- `.agents/hooks` participates in the same compiler pipeline as pack hooks
- hook support assets are namespaced and collision-safe

## Immediate Next Step

Start with Phase 0 and Phase 1. The first implementation milestone should be:

1. a Claude-native parser
2. a normalized IR with origin metadata
3. a capability registry
4. a collector over pack plugins, bare skills, `.agents`, and seeded singleton hook files

That gives us the foundation for the renderers and `hook-exec` bridge without compromising the Claude-first authoring contract.
