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

A server definition is carried over as `type`, `command`, `args`, `env`, `url`, `disabled`, `headers`, `cwd` and its OAuth client settings. The last three are written where the agent's documented format has a place for them:

| | Claude Code | Cursor | Codex | OpenCode | Grok | Antigravity |
|---|---|---|---|---|---|---|
| `headers` (remote) | `headers` | `headers` | `http_headers` | `headers` | `headers` | `headers` |
| `cwd` (stdio) | no setting | no setting | `cwd` | `cwd` | no setting | `cwd` |
| OAuth client id | `oauth.clientId` | `auth.CLIENT_ID` | `oauth.client_id` | `oauth.clientId` | no setting | `oauth.clientId` |
| OAuth client secret | not in the file (`MCP_CLIENT_SECRET`) | `auth.CLIENT_SECRET` | no setting | `oauth.clientSecret` | no setting | `oauth.clientSecret` |
| OAuth scopes | `oauth.scopes` | `auth.scopes` | `scopes` | `oauth.scope` | no setting | no setting |
| OAuth callback port | `oauth.callbackPort` | fixed by Cursor | `oauth.callback_port` | no setting | no setting | fixed by Antigravity |

The OAuth settings are read in any of the published spellings (`oauth` with `clientId` or `client_id`, Cursor's `auth`, a `scopes` list next to them). Where an agent has no setting, the server is staged for that agent without it and a warning names the server, the agent and the setting. A plugin server's relative `cwd` is relative to the plugin and is staged like `${CLAUDE_PLUGIN_ROOT}` (see below). In the manifest the same fields are `headers`, `cwd` and `[mcp.servers.<name>.oauth]` with `client_id`, `client_secret`, `callback_port`, `scopes`, `metadata_url`.

Header values and OAuth settings may hold credentials. Like `env`, they are never written to `pack.lock`, and they are not part of a record's `definition` digest; `cwd` is, when set. Any other field (`headersHelper`, `envFile`, …) is left out with a warning that names it.

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

### The locked dependency tree of an npx server

A version pin fixes the server's own package, not the packages it depends on. For a server started with `npx`, `lock` therefore also asks npm which packages it would install, without running any package script (`npm install --package-lock-only --ignore-scripts` in a temporary directory), and records every one of them in `pack.lock`: its path under `node_modules`, exact version and registry integrity, with npm's platform constraints for optional packages. `tree_digest` is a digest of that list.

```toml
[[mcp_servers]]
name = "playwright"
version = "0.0.83"
bin = "cli.js"
tree_digest = "sha256-npm-tree-v1:a856…"

[[mcp_servers.tree]]
path = "node_modules/playwright-core"
version = "1.64.0-alpha-1790635538000"
integrity = "sha512-pNwa…"
```

Such a server is staged to start through Agentpack instead of `npx`:

```text
agentpack.toml / mcp.json     npx -y @playwright/mcp@latest --headless
staged for every harness      agentpack mcp-exec --spec …/mcp-trees/playwright.json -- --headless
```

The first time the server starts, `mcp-exec` downloads each tarball of the tree from the registry, checks it against its recorded integrity before writing anything, unpacks it into Agentpack's cache (`cache/npm-trees/<tree digest>`) and runs the package's executable with `node` and your arguments. No package script runs. Later starts reuse the unpacked tree after checking that its files are unchanged; a tree edited in the cache is unpacked again from the verified tarballs. A tarball whose integrity differs from the lock stops the server and nothing runs. `npm` is needed only by `lock`; starting the server needs `node` and the registry, once per tree.

A teammate who runs the same `pack.lock` therefore starts the same files. The tree moves only with `agentpack lock --update`. Locks made before trees were recorded get them on the next `agentpack lock`, at the version they already pin. Without `npm` on PATH, or when npm cannot resolve the tree, the server keeps its top-level pin, `lock` says so, and it starts through `npx` as before. Servers started by `bunx`, `pnpm dlx` or `yarn dlx` keep their own runtime and are pinned at the top level only. A lock with a tree is written as `lockfile_version = 4`; earlier releases refuse it with an "unsupported lockfile_version" message rather than start an unlocked tree.

The pin moves only when you ask: `agentpack lock --update` (or `agentpack update` with no arguments) resolves dist-tags again. A plain `lock`, `sync` or launch keeps the recorded version and needs no registry. If a refresh resolves to the version that is already locked but the registry now reports a different integrity for it, the lock fails and writes nothing.

### When the registry is unreachable

`agentpack lock` and `agentpack mcp add` need the registry once per new or changed npm server. If it cannot be reached, they fail and write nothing, rather than record a server that looks locked and is not. To proceed anyway:

```sh
agentpack lock --allow-unpinned-mcp
agentpack mcp add playwright --command npx --args @playwright/mcp@latest --allow-unpinned
```

The server is then recorded with `status = "unpinned"` and `allow_unpinned = true`, runs as written, and is tried again on the next `lock`.

`agentpack add` does not fail on an unreachable registry: the package is added and locked, the server is left without a record, and a warning names it. It then runs as written until `agentpack lock` can pin it.

`sync` and the launchers never contact a registry; a locked tree is downloaded by the server's own start (`mcp-exec`), once. A server with no current record runs as written, with a warning that names it.

### Limits

- **Dependencies are locked for `npx` servers only.** For `bunx`, `pnpm dlx` and `yarn dlx`, and for an `npx` server whose tree npm could not resolve at lock time, only the top-level package is pinned; the runner still resolves its dependencies when it installs it.
- **Integrity is enforced at launch for a locked tree**, by `mcp-exec`. For a server pinned at the top level only, the runner downloads the package and checks it against the registry itself; the recorded value lets `lock` notice a registry that changes a published version.
- **Native addons and install scripts.** No package script runs when a tree is unpacked. A server that needs a compiled addon built by `install` or `postinstall` does not start from a locked tree; record it with `npx` arguments that bypass the tree (for example a version range, which stays `unpinned`).
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
