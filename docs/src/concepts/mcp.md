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
agentpack mcp list      # every server, with provenance (manifest / plugin / .agents) and lock status
```

`add` and `remove` edit `[mcp.servers]`, update the server records in `pack.lock`, and then sync, unless you pass `--no-sync`.

## The merge pipeline

After packages and the `./.agents/` overlay are staged, `sync` gathers MCP definitions from three sources and merges them. Later sources win when the same server name appears more than once:

1. **Plugin definitions** — from each staged plugin, ordered by `cache_key`, filtered through the active [mode](./modes.md)
2. **Manifest `[mcp.servers]`** — your project-level definitions
3. **`./.agents/mcp.json`** — the project overlay

A plugin's servers are read from every place the vendors' plugin formats allow, in this order (a later definition of the same server name wins):

| Location | Format it comes from |
|---|---|
| `mcp.json` at the plugin root | Cursor |
| `.mcp.json` at the plugin root | Claude Code, Codex |
| `mcpServers` in `.codex-plugin/plugin.json`: a path such as `./.codex-plugin/mcp.json`, or an inline map | Codex |
| `mcpServers` in `.cursor-plugin/plugin.json`: a path, an inline map, or an array of them | Cursor |
| `mcpServers` in `.claude-plugin/plugin.json`: a path, an inline map, or an array of them | Claude Code |

A definition file may wrap its servers in `mcpServers` or be the bare `name → server` map; both are published and both are read. A declared path must stay inside the package. A server or a location that cannot be read (a `.mcpb` bundle, a URL, an entry with neither `command` nor `url`) is skipped with a warning that names it; the package's other servers still load.

The merged set is then written in each harness's native format:

| Harness | Output |
|---|---|
| Claude, Cursor | JSON `mcpServers` |
| OpenCode | `opencode.json` |
| Codex, Grok | `[mcp_servers]` TOML |
| Antigravity | plugin `mcp_config.json` (remote servers use `serverUrl`) |

For Cursor's fake `HOME`, the merged pack `mcp.json` is additionally merged with your real `~/.cursor/mcp.json` — user-defined entries win on conflict — so agentpack-managed servers coexist with your own.

## Servers that run from the plugin's own directory

A plugin's server definition may name the plugin's directory: `bun run --cwd ${CLAUDE_PLUGIN_ROOT} start`, `node ${CLAUDE_PLUGIN_ROOT}/start.mjs`. Claude Code expands that variable for a plugin it installed itself; the other agents do not know it, and in agentpack's merged bundle it would not name the plugin's files for Claude Code either.

For each package with such a server, `sync` therefore stages one whole copy of the package under `<staging>/modes/<mode>/plugin-roots/<cache_key prefix>/` and writes that path into every harness's MCP config in place of `${CLAUDE_PLUGIN_ROOT}`, `${CURSOR_PLUGIN_ROOT}` or `${PLUGIN_ROOT}`, in `command`, `args` and `env`. The copy is filtered through the active mode like the rest of the package. The server runs in the copy, not in the verified cache entry, so what it writes next to its files (a `node_modules`, a database) cannot make the cache fail verification; the copy is rebuilt by each `sync`.

`${CLAUDE_PLUGIN_DATA}` and `${PLUGIN_DATA}` become `$AGENTPACK_HOME/projects/<hash>/plugin-data/<module>/`, which survives rebuilds and package updates.

`pack.lock` records the definition as the package published it, with the variable, so a lock is the same on every machine.

## What pack.lock records

MCP definitions live in the manifest, in plugins and in `./.agents/mcp.json`, but what a server *runs* is decided when it starts: `npx @playwright/mcp@latest` downloads whatever `latest` means that day. `agentpack lock` therefore records every merged server in `pack.lock` under `[[mcp_servers]]`, with an explicit `status`. `agentpack add` does the same for the servers the added package brings, and `agentpack mcp add` for the one it defines:

| Launcher | Recognized from | What is recorded | Status |
|---|---|---|---|
| `npm` | `npx`, `bunx`, `pnpm dlx`, `yarn dlx`, `bun x` with a registry package | package, the exact version behind the version or dist-tag you wrote, the registry's tarball integrity, the registry URL | `pinned` |
| `pypi` | `uvx`, `uv tool run`, `pipx run` | the requirement as written, and its version if you wrote `==` | `unpinned`: version and hash resolution is not implemented |
| `docker` | `docker run`, `podman run` | the image as written | `pinned` if you wrote an `@sha256:` digest, otherwise `unpinned`: digest resolution is not implemented |
| `remote` | a `url` server | the host | `unpinnable`: the host decides what runs |
| `command` | anything else | the executable | `unpinnable`: agentpack does not manage what it runs |

`pinned` means agentpack knows the exact artifact and stages it. `unpinned` means it could be pinned and is not. `unpinnable` means nothing agentpack records can fix what runs.

Environment values and arguments are never written to the lock. Each record carries a `definition` digest of the server's command, arguments and URL, and applies only while that digest matches; edit a server and its record is stale until the next `agentpack lock`.

### Staging a pinned npm server

For a `pinned` npm server, `sync` writes the exact version into every harness's MCP config instead of the spec you wrote:

```text
agentpack.toml / mcp.json     npx @playwright/mcp@latest
staged for every harness      npx @playwright/mcp@0.0.83
```

Your definition is not edited. `agentpack mcp list` shows both the definition and the pin.

The pin moves only when you ask: `agentpack lock --update` (or `agentpack update` with no arguments) resolves dist-tags again. A plain `lock`, `sync` or launch keeps the recorded version and needs no registry. If a refresh resolves to the version that is already locked but the registry now reports a different integrity for it, the lock fails and writes nothing.

### When the registry is unreachable

`agentpack lock` and `agentpack mcp add` need the registry once per new or changed npm server. If it cannot be reached, they fail and write nothing, rather than record a server that looks locked and is not. To proceed anyway:

```sh
agentpack lock --allow-unpinned-mcp
agentpack mcp add playwright --command npx --args @playwright/mcp@latest --allow-unpinned
```

The server is then recorded with `status = "unpinned"` and `allow_unpinned = true`, runs as written, and is tried again on the next `lock`.

`agentpack add` does not fail on an unreachable registry: the package is added and locked, the server is left without a record, and a warning names it. It then runs as written until `agentpack lock` can pin it.

`sync` and the launchers never contact a registry. A server with no current record runs as written, with a warning that names it.

### Limits

- **Only the top-level package is pinned.** `npx` still resolves that package's own dependencies when it installs it, and they can change. agentpack does not vendor or lock them.
- **The integrity is recorded, not enforced at launch.** agentpack does not download or run the package; `npx` does, and checks the tarball against the registry itself. The recorded value lets `lock` notice a registry that changes a published version.
- **The registry is the one agentpack can see**: `npm_config_registry` in the server's `env`, then in your environment, then `https://registry.npmjs.org`. Per-user `.npmrc` files are not read; if yours points elsewhere, set the variable.
- **Version ranges** (`pkg@^1.2`), Git and tarball specs, and `npx --package` are recorded as `unpinned`. Use an exact version or a dist-tag.
- **PyPI and docker** servers are recorded but not resolved. Write `pkg==1.2.3` or `image@sha256:…` yourself to fix what runs.

## Toggling servers per mode

Use the `mcp:<name>` selector in a mode to enable or disable a server without removing it:

```toml
[modes.review]
base    = "all"
disable = ["mcp:filesystem"]
```

See [Modes](./modes.md) for the full selector syntax.
