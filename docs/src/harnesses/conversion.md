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

## Skill shadowing

A full plugin at repo path `P` (same owner / repo / commit) shadows any **skill** whose path is `P` or sits under `P/`. An empty `P` shadows every skill for that repo at that commit. This keeps a plugin and its bundled skills from staging duplicate copies of the same content.
