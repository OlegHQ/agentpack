# MCP Servers

[Model Context Protocol](https://modelcontextprotocol.io) servers give agents tools and data sources. agentpack collects MCP definitions from your manifest, your packages, and your `./.agents/` overlay, merges them, and writes the result into each harness's native MCP config during `sync` — so one definition reaches every agent.

## Declaring servers in the manifest

Add servers under `[mcp.servers]`. Each key is a server name:

```toml
[mcp.servers.filesystem]
command = "npx"
args    = ["-y", "@modelcontextprotocol/server-filesystem"]

[mcp.servers.retrieval]
command = "uvx"
args    = ["mcp-retrieval"]
env     = { API_KEY = "sk-..." }
```

| Field | Type | Description |
|---|---|---|
| `command` | string | Executable to launch the server |
| `args` | string array | Arguments passed to the command |
| `env` | string map | Environment variables for the server process |
| `disabled` | bool | Optional; skip this server when set |

## Managing servers from the CLI

```sh
agentpack mcp add retrieval --command uvx --args mcp-retrieval --env API_KEY=sk-...
agentpack mcp remove retrieval
agentpack mcp list      # every server, with provenance (manifest / plugin / .agents)
```

`add` and `remove` edit `[mcp.servers]` and then sync, unless you pass `--no-sync`.

## The merge pipeline

After packages and the `./.agents/` overlay are staged, `sync` gathers MCP definitions from three sources and merges them. Later sources win when the same server name appears more than once:

1. **Plugin `mcp.json` files** — from each staged plugin, ordered by `cache_key`, filtered through the active [mode](./modes.md)
2. **Manifest `[mcp.servers]`** — your project-level definitions
3. **`./.agents/mcp.json`** — the project overlay

The merged set is then written in each harness's native format:

| Harness | Output |
|---|---|
| Claude, Cursor | JSON `mcpServers` |
| OpenCode | `opencode.json` |
| Codex, Grok | `[mcp_servers]` TOML |
| Antigravity | plugin `mcp_config.json` (remote servers use `serverUrl`) |

For Cursor's fake `HOME`, the merged pack `mcp.json` is additionally merged with your real `~/.cursor/mcp.json` — user-defined entries win on conflict — so agentpack-managed servers coexist with your own.

## Toggling servers per mode

Use the `mcp:<name>` selector in a mode to enable or disable a server without removing it:

```toml
[modes.review]
base    = "all"
disable = ["mcp:filesystem"]
```

See [Modes](./modes.md) for the full selector syntax.
