# Cross-Harness Conversion

Every harness names and formats the same ideas differently. agentpack does not copy artifacts verbatim — during `sync` it **parses** each markdown artifact from the cached package and **re-renders** it into every target harness's native format.

## The four artifact types

| Type | What it is |
|---|---|
| **command** | A named slash command (e.g. `/review`) |
| **agent** | A named sub-agent with its own system prompt |
| **skill** | A reusable instruction set with frontmatter |
| **rule** | Context applied across a session (Cursor `.mdc`, Antigravity rules) |

## How it works

1. agentpack reads the artifact from the cached package, detecting its source format — Cursor plain markdown, OpenCode markdown frontmatter, Claude command/agent/skill frontmatter, or skill frontmatter.
2. For each target harness, it re-renders the artifact in that harness's native shape: the right directory, filename, and frontmatter.
3. When a harness lacks a first-class equivalent, agentpack uses a documented fallback rather than dropping the artifact.

## Where each artifact lands

| Source | Claude | Cursor | OpenCode | Codex | Grok | Antigravity |
|---|---|---|---|---|---|---|
| command | command frontmatter | plain markdown | command markdown | skill fallback | command | command |
| agent | agent frontmatter | agent markdown | agent markdown | skill fallback | agent | agent |
| skill | normalized skill | skill | skill | Codex skill | skill | skill |
| rule | skill fallback | `.mdc` (preserved) | skill fallback | skill fallback | skill fallback | rule (preserved) |

Two harnesses have first-class rules — Cursor and Antigravity — so rules are preserved there. Everywhere else, a rule becomes a best-effort skill, with its original rule scope noted so the intent survives. Codex gets the portable skill subset: commands and agents fall back to skills.

## Frontmatter

agentpack writes whatever frontmatter the target format needs (for example, Cursor `.mdc` metadata), using the artifact's name and any metadata carried in the source. The instruction body is preserved; only the wrapper changes.

## Hooks

A package's hooks are read from every place the plugin formats define them: `hooks/hooks.json`, and the `hooks` field of `.claude-plugin/plugin.json`, `.cursor-plugin/plugin.json` and `.codex-plugin/plugin.json` (a path, an inline object, or an array of them). A file is read in Claude Code's format (`{"hooks": {"Stop": [{"matcher": …, "hooks": […]}]}}`, also Codex's) or in Cursor's (`{"version": 1, "hooks": {"stop": [{"command": …}]}}`), whichever its event names belong to.

When a package publishes hooks for more than one agent, each agent gets its own: Claude Code the Claude Code hooks, Cursor the Cursor hooks, Codex the Codex hooks. An agent the package has no hooks for gets the Claude Code hooks, else Cursor's, else Codex's, converted event by event.

The event names are the ones in the Claude Code and Cursor hook references (read 2026-10-13). Claude Code has 33:

`SessionStart`, `Setup`, `UserPromptSubmit`, `UserPromptExpansion`, `PreToolUse`, `PermissionRequest`, `PermissionDenied`, `PostToolUse`, `PostToolUseFailure`, `PostToolBatch`, `Notification`, `MessageDisplay`, `SubagentStart`, `SubagentStop`, `TaskCreated`, `TaskCompleted`, `Stop`, `StopFailure`, `TeammateIdle`, `InstructionsLoaded`, `ConfigChange`, `CwdChanged`, `DirectoryAdded`, `FileChanged`, `WorktreeCreate`, `WorktreeRemove`, `PreCompact`, `PostCompact`, `PreModelSwitch`, `PostModelSwitch`, `Elicitation`, `ElicitationResult`, `SessionEnd`.

Cursor has 21: `sessionStart`, `sessionEnd`, `preToolUse`, `postToolUse`, `postToolUseFailure`, `subagentStart`, `subagentStop`, `beforeShellExecution`, `afterShellExecution`, `beforeMCPExecution`, `afterMCPExecution`, `beforeReadFile`, `afterFileEdit`, `beforeSubmitPrompt`, `preCompact`, `stop`, `afterAgentResponse`, `afterAgentThought`, `beforeTabFileRead`, `afterTabFileEdit`, `workspaceOpen`.

| Hook written for | Claude | Cursor | Codex | OpenCode | Grok, Antigravity |
|---|---|---|---|---|---|
| a Claude Code event | every event, as written | the ten events Cursor shares (`preToolUse`, `postToolUse`, `postToolUseFailure`, `beforeSubmitPrompt`, `stop`, `subagentStart`, `subagentStop`, `sessionStart`, `sessionEnd`, `preCompact`); `PermissionRequest` as `preToolUse` | `PreToolUse`, `PostToolUse`, `UserPromptSubmit`, `SessionStart`, `Stop`; `PermissionRequest` as `PreToolUse` | `PreToolUse`, `PostToolUse`, `UserPromptSubmit`, `PermissionRequest`, `PreCompact` | not staged (see their pages) |
| a Cursor event | the ten shared events | every event, as written, with its `matcher`, `timeout`, `loop_limit` and `failClosed` | as for the shared Claude Code events | as for the shared Claude Code events | not staged |

A hook whose event the target agent has no equivalent for is skipped for that agent, with a warning that names the event and the hook file; the package's other hooks and its other content are staged. The same holds for an event name no agent documents, a handler `type` agentpack cannot run (`mcp_tool`), and a hook file that does not parse. Command hooks run with `CLAUDE_PLUGIN_ROOT`, `CURSOR_PLUGIN_ROOT` and `PLUGIN_ROOT` set to the staged copy of their package.

## Skill shadowing

A full plugin at repo path `P` (same owner / repo / commit) shadows any **skill** whose path is `P` or sits under `P/`. An empty `P` shadows every skill for that repo at that commit. This keeps a plugin and its bundled skills from staging duplicate copies of the same content.
