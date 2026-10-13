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
| agent | agent frontmatter | agent markdown | agent markdown | custom agent (`agents/<name>.toml`) and skill fallback | agent | agent |
| skill | normalized skill | skill | skill | Codex skill | skill | skill |
| rule | skill fallback | `.mdc` (preserved) | skill fallback | skill fallback | skill fallback | rule (preserved) |

Two harnesses have first-class rules — Cursor and Antigravity — so rules are preserved there. Everywhere else, a rule becomes a best-effort skill, with its original rule scope noted so the intent survives. Codex gets commands as skills. A subagent is staged for Codex twice: as a custom agent file, `$CODEX_HOME/agents/<name>.toml` with `name` (the file name), `description` and `developer_instructions` (the agent's body), which is what Codex documents for custom agents, and as a skill of the same name, which Codex versions without custom agents still find.

Where a command, agent or rule becomes a skill, it keeps its name. If the same package already has a skill of that name (or another converted item took it), the converted item is staged as `<name>-command`, `<name>-agent` or `<name>-rule`, then with `-2`, `-3`, and a warning says so. Nothing is overwritten.

## Where artifacts are found

Artifacts are read from the default directories of every plugin format (`skills/<name>/SKILL.md`, `commands/`, `agents/`, `rules/`, also under `.claude/`, `.cursor/`, `.opencode/`, `.agents/`, `.grok/`), and from the component paths a plugin manifest declares: `skills`, `commands`, `agents` and `rules` in `.claude-plugin/plugin.json`, `.cursor-plugin/plugin.json` or `.codex-plugin/plugin.json`, each a path or an array of paths. Claude Code adds declared skills to the default directory and replaces it for commands and agents; Cursor replaces; agentpack stages one tree for all agents, so it reads the declared paths in addition to the defaults.

`README.md`, `CHANGELOG.md`, `LICENSE.md`, `CONTRIBUTING.md` and similar documentation files, written in capitals, are not commands, agents or rules and are not staged.

A declared path, and a symbolic link under a component directory, is followed only while it resolves inside the package (`"skills": "./.codex/skills/"`, where each entry links to `../../engineering/skills/<name>`). A path or link that leaves the package, does not exist, or loops is skipped with a warning that names it. Components written inline in a manifest (Claude Code's `commands` object) are not staged, also with a warning.

## Frontmatter

agentpack writes whatever frontmatter the target format needs (for example, Cursor `.mdc` metadata), using the artifact's name and any metadata carried in the source. The instruction body is preserved; only the wrapper changes.

Frontmatter that is not valid YAML as a whole is read one top-level key at a time, as the agents read it: a value that does not parse, such as the unquoted `argument-hint: [system] [--source <path>]`, is kept as the text written. Frontmatter with no readable key leaves the file with no fields and its description is taken from the body.

OpenCode validates every agent file and refuses its whole configuration when one field has another form, so an agent rendered for OpenCode has its Claude Code fields converted:

| Claude Code | OpenCode |
|---|---|
| `tools: Read, Grep, Bash` or a YAML list (an allow list) | `tools:` object with `read`, `write`, `edit`, `bash`, `grep`, `glob`, `list`, `webfetch`, `websearch`, `task`, `todowrite`, `skill` each `true` or `false` |
| `disallowedTools` | the same tools set to `false` |
| `color: blue` (a name) | the hex value; a value that is neither a known name nor hex is left out |
| `model: sonnet` (an alias) | left out, so the agent uses the session's model; a `provider/model` id is kept |
| `mode` other than `subagent`, `primary`, `all` | left out |

A tool with no OpenCode counterpart (an MCP tool name, for example) is left out of the map. Each value left out is named in a warning.

## Hooks

A package's hooks are read from every place the plugin formats define them: `hooks/hooks.json`, and the `hooks` field of `.claude-plugin/plugin.json`, `.cursor-plugin/plugin.json` and `.codex-plugin/plugin.json` (a path, an inline object, or an array of them). A file is read in Claude Code's format (`{"hooks": {"Stop": [{"matcher": …, "hooks": […]}]}}`, also Codex's) or in Cursor's (`{"version": 1, "hooks": {"stop": [{"command": …}]}}`), whichever its event names belong to.

When a package publishes hooks for more than one agent, each agent gets its own: Claude Code the Claude Code hooks, Cursor the Cursor hooks, Codex the Codex hooks. An agent the package has no hooks for gets the Claude Code hooks, else Cursor's, else Codex's, converted event by event.

The event names are the ones in the Claude Code and Cursor hook references (read 2026-10-13). Claude Code has 33:

`SessionStart`, `Setup`, `UserPromptSubmit`, `UserPromptExpansion`, `PreToolUse`, `PermissionRequest`, `PermissionDenied`, `PostToolUse`, `PostToolUseFailure`, `PostToolBatch`, `Notification`, `MessageDisplay`, `SubagentStart`, `SubagentStop`, `TaskCreated`, `TaskCompleted`, `Stop`, `StopFailure`, `TeammateIdle`, `InstructionsLoaded`, `ConfigChange`, `CwdChanged`, `DirectoryAdded`, `FileChanged`, `WorktreeCreate`, `WorktreeRemove`, `PreCompact`, `PostCompact`, `PreModelSwitch`, `PostModelSwitch`, `Elicitation`, `ElicitationResult`, `SessionEnd`.

Cursor has 21: `sessionStart`, `sessionEnd`, `preToolUse`, `postToolUse`, `postToolUseFailure`, `subagentStart`, `subagentStop`, `beforeShellExecution`, `afterShellExecution`, `beforeMCPExecution`, `afterMCPExecution`, `beforeReadFile`, `afterFileEdit`, `beforeSubmitPrompt`, `preCompact`, `stop`, `afterAgentResponse`, `afterAgentThought`, `beforeTabFileRead`, `afterTabFileEdit`, `workspaceOpen`.

| Hook written for | Claude | Cursor | Codex | OpenCode | Grok, Antigravity |
|---|---|---|---|---|---|
| a Claude Code event | every event, as written | the ten events Cursor shares (`preToolUse`, `postToolUse`, `postToolUseFailure`, `beforeSubmitPrompt`, `stop`, `subagentStart`, `subagentStop`, `sessionStart`, `sessionEnd`, `preCompact`); `PermissionRequest` as `preToolUse` | the eleven events Codex shares: `PreToolUse`, `PermissionRequest`, `PostToolUse`, `PreCompact`, `PostCompact`, `UserPromptSubmit`, `SubagentStart`, `SubagentStop`, `Stop`, `SessionStart`, `SessionEnd` | `PreToolUse`, `PostToolUse`, `UserPromptSubmit`, `PermissionRequest`, `PreCompact` | not staged (see their pages) |
| a Cursor event | the ten shared events | every event, as written, with its `matcher`, `timeout`, `loop_limit` and `failClosed` | as for the shared Claude Code events | as for the shared Claude Code events | not staged |

How each hook was rendered is reported as an information line (`hook for <agent>: …`), which `--quiet` leaves out. A hook whose event the target agent has no equivalent for is skipped for that agent, with a warning that names the event and the hook file; the package's other hooks and its other content are staged. The same holds for an event name no agent documents, a handler `type` agentpack cannot run (`mcp_tool`), and a hook file that does not parse. Command hooks run with `CLAUDE_PLUGIN_ROOT`, `CURSOR_PLUGIN_ROOT` and `PLUGIN_ROOT` set to the staged copy of their package. A command hook in Claude Code's exec form (`"command": "node", "args": ["${CLAUDE_PLUGIN_ROOT}/scripts/hook.mjs"]`) keeps its arguments and runs without a shell, with those variables substituted in the program and its arguments. Every staged hook calls agentpack by the absolute path of the executable that staged it, not by whatever `agentpack` is on the agent's `PATH`.

## Skill shadowing

A full plugin at repo path `P` (same owner / repo / commit) shadows any **skill** whose path is `P` or sits under `P/`. An empty `P` shadows every skill for that repo at that commit. This keeps a plugin and its bundled skills from staging duplicate copies of the same content.
