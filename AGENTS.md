# AgentPack

A TUI/CLI tool that manages **skills**, **MCP servers**, and **plugins** for agentic CLI tools. Starting with Claude Code as the MVP target, with planned support for other agentic CLIs.

## What It Does

AgentPack is a package manager for the agentic ecosystem. It handles three types of installable units:

### Skills
Prompt-based capabilities that extend an agent's behavior (e.g., a Rust tutor, a code reviewer, a commit helper).

- **Install from GitHub URL**: AgentPack auto-detects if a URL points to a skill (by looking for `SKILL.md` or `.skill` archive), pulls it, and installs it into the project's `.claude/skills/` directory.
- **`.skill` format**: A zip archive containing `SKILL.md` and optional `references/` directory.
- **Symlinks for other agents**: When installing a skill, AgentPack can create symlinks or equivalent configs in other agent directories (future: Cursor, Windsurf, Aider, etc.).

### MCP Servers
Model Context Protocol servers that give agents access to external tools and data sources.

- **Registry-based**: AgentPack maintains a JSON registry (`registry.json`) of known MCP servers with their configs, installation commands, and metadata.
- **Install/uninstall**: Reads the registry, installs the MCP server, and writes the config into the agent's settings (e.g., `.claude/settings.json` for Claude Code).
- **Config management**: Handles env vars, transport settings, and per-project overrides.

### Plugins
A plugin is a **collection** that bundles multiple installable units together:

- Skills
- Subagent definitions
- MCP server configs
- Any combination of the above

Plugins let you install an entire workflow in one shot (e.g., a "full-stack dev" plugin that includes a code review skill, a database MCP, and a deployment subagent).

## Architecture

```
agentpack/
├── src/                    # Rust source (TUI + CLI)
│   ├── main.rs
│   ├── cli/                # CLI command handlers
│   ├── tui/                # TUI interface (ratatui)
│   ├── registry/           # Registry loading and management
│   ├── installers/         # Install logic per agent target
│   │   ├── claude.rs       # Claude Code installer (MVP)
│   │   └── mod.rs
│   ├── skill.rs            # Skill detection, parsing, packaging
│   ├── mcp.rs              # MCP server config management
│   └── plugin.rs           # Plugin (collection) handling
├── registry.json           # Built-in registry of known MCPs/skills/plugins
├── AGENTS.md               # This file
├── CLAUDE.md               # Claude Code working instructions
└── Cargo.toml
```

## MVP Scope (Claude Code Target)

The MVP focuses exclusively on Claude Code as the target agent:

1. **`agentpack install <github-url>`** — Detect whether the URL is a skill, MCP, or plugin. Download and install to `.claude/`.
2. **`agentpack list`** — Show installed skills, MCPs, and plugins.
3. **`agentpack remove <name>`** — Uninstall a skill/MCP/plugin.
4. **`agentpack search <query>`** — Search the registry.
5. **`agentpack registry update`** — Pull latest registry from remote.
6. **TUI mode** (`agentpack` with no args) — Interactive browsing, installing, and managing.

## Agent Behavior

- **Use the `rust-tutor` skill** when assisting with Rust development on this project. Guide the user through implementation — explain what to do, suggest commands to run, hint at patterns — but do NOT write code for them unless explicitly asked. The user should be typing the code themselves.
- When the user says "let's start coding" or similar, walk them through the setup steps (e.g., `cargo init`, adding dependencies to `Cargo.toml`, creating the module structure) instead of executing commands or writing files directly.

## Key Design Decisions

- **Rust** for the CLI/TUI (performance, single binary distribution).
- **ratatui** for the TUI layer.
- **GitHub-first**: Skills and plugins are hosted as repos or files on GitHub. No custom package server needed for MVP.
- **Auto-detection**: When given a URL, AgentPack inspects the repo/file to determine what it is (skill, MCP config, plugin manifest) without requiring the user to specify.
- **Agent-agnostic core**: The install/registry logic is decoupled from any specific agent. Agent-specific adapters (starting with Claude Code) handle writing configs to the right locations.
- **Registry is a JSON file**: Simple, versionable, forkable. Community can submit PRs to add entries.

## Registry Format

```json
{
  "version": 1,
  "skills": [
    {
      "name": "rust-tutor",
      "description": "Interactive Rust coding tutor",
      "source": "github:user/rust-tutor-skill",
      "tags": ["rust", "education"]
    }
  ],
  "mcps": [
    {
      "name": "notion",
      "description": "Notion MCP server",
      "source": "npm:@anthropic/mcp-notion",
      "config": {
        "command": "npx",
        "args": ["-y", "@anthropic/mcp-notion"],
        "env": ["NOTION_API_KEY"]
      },
      "tags": ["productivity", "notion"]
    }
  ],
  "plugins": [
    {
      "name": "full-stack-dev",
      "description": "Full-stack development toolkit",
      "source": "github:user/fullstack-plugin",
      "includes": {
        "skills": ["code-review", "api-designer"],
        "mcps": ["postgres", "redis"],
        "subagents": ["test-runner"]
      }
    }
  ]
}
```
