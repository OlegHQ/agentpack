# Cross-Harness Artifact Conversion

Each AI harness uses a different file format and directory structure for the same conceptual artifact types. When agentpack stages a package, it automatically converts every artifact to every supported harness's native format.

## Artifact types

| Type | Description |
|---|---|
| **command** | A named slash-command the user can invoke (e.g. `/review`) |
| **agent** | A named sub-agent or mode with a dedicated system prompt |
| **skill** | A reusable instruction block, similar to an agent |
| **rule** | A persistent context fragment injected into every session |

## Conversion table

| Source artifact | Claude Code | Cursor | OpenCode | Codex |
|---|---|---|---|---|
| command | `.claude/commands/<n>.md` | Converted to rule | Agent instruction | Agent instruction |
| agent | `.claude/agents/<n>.md` | `.cursor/rules/<n>.mdc` | `agents/<n>.md` | `agents/<n>.md` |
| skill | `.claude/agents/<n>.md` | `.cursor/rules/<n>.mdc` | `agents/<n>.md` | `agents/<n>.md` |
| rule | System prompt prepend | `.cursor/rules/<n>.mdc` | `instructions/<n>.md` | `instructions/<n>.md` |

## How conversion works

1. A package declares its artifacts in a format-agnostic manifest inside the package itself.
2. At stage time, agentpack reads the artifact declarations and the content files.
3. For each target harness, agentpack writes the artifact in the native format.

The content itself is not transformed — only the file location, name, and any wrapping frontmatter are adjusted.

## Example

A package ships a skill called `code-review` with content in `skills/code-review.md`. After staging:

```
claude/   .claude/agents/code-review.md
cursor/   home/.cursor/rules/code-review.mdc
opencode/ agents/code-review.md
codex/    agents/code-review.md
```

Each harness sees the same instructions in its own native location.

## Frontmatter handling

Some harnesses require frontmatter metadata (e.g. Cursor `.mdc` files). agentpack adds the required frontmatter when converting, using the artifact name and any metadata declared in the package.

## Limitations

- **Commands** are not fully first-class in all harnesses. For Cursor, OpenCode, and Codex, commands are folded into agent/instruction files with a note indicating their original command name.
- Binary or non-text artifacts are not converted and are only staged for harnesses that declare support for them in the package manifest.
