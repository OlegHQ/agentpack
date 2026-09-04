# Environment Variables

Set these in your shell profile (`.bashrc`, `.zshrc`, `config.fish`) or in CI.

## State and staging

### `AGENTPACK_HOME`

The user-wide root for cache, the metadata index, the `local/` mirror, and per-project bookkeeping.

**Default:** `$XDG_DATA_HOME/agentpack` (or `$HOME/.local/share/agentpack`) on Unix; `%LOCALAPPDATA%\agentpack` on Windows.

```sh
export AGENTPACK_HOME=/data/agentpack
```

Point it at a network mount for a shared cache, or somewhere project-specific for isolation.

### `AGENTPACK_STAGING_ROOT`

Root for per-harness, per-mode staging trees.

**Default:** `<temp_dir>/agentpack-<project-hash>`.

```sh
export AGENTPACK_STAGING_ROOT=/tmp/agentpack-staging
```

Set this for a stable path when your OS rotates temp directories, or to put staging on a fast local disk in CI.

## Behavior toggles

### `AGENTPACK_KEEP_ATTRIBUTION`

**Default:** unset. Set to `1` / `true` / `yes` to keep your existing AI-attribution settings (Co-Authored-By trailers, "Generated with X" footers) in staged harness configs. By default agentpack forces attribution off in staging. See [Overrides and Attribution](../guides/overrides.md).

### `AGENTPACK_DOT_AGENTS`

**Default:** enabled. Set to `0` to skip merging the project's `./.agents/` overlay into harness staging.

## Claude proxy diagnostics

### `AGENTPACK_PROXY_LOG_DIR`

Directory for `agentpack --proxy claude` JSONL diagnostics.

**Default:** `$AGENTPACK_HOME/projects/<project-hash>/proxy-logs`.

### `AGENTPACK_PROXY_LOG_PAYLOADS`

**Default:** unset. Set to `1` / `true` / `yes` to include truncated upstream error bodies and payload snippets in proxy logs. Leave unset for sanitized metadata-only logs.

### `AGENTPACK_PROXY_LOG_MAX_BODY_BYTES`

Maximum bytes retained for any payload snippet when payload logging is enabled.

**Default:** `4096`.

### `AGENTPACK_PROXY_WS_CONNECT_TIMEOUT_SECS`

WebSocket TCP connect timeout for proxy upstream connections.

**Default:** `15`.

### `AGENTPACK_PROXY_WS_IDLE_TIMEOUT_SECS`

WebSocket read/write idle timeout for proxy upstream connections.

**Default:** `300`.

### Proxy transport, endpoint, and model overrides

| Variable | Default | Purpose |
|---|---|---|
| `AGENTPACK_PROXY_TRANSPORT` | inferred from auth | `http`, `websocket`, or `auto` |
| `AGENTPACK_PROXY_PORT` | `0` | Loopback listen port; `0` selects a free port |
| `AGENTPACK_PROXY_REQUEST_TIMEOUT_SECS` | `300` | Complete upstream request timeout |
| `AGENTPACK_PROXY_UPSTREAM_URL` | Codex auth endpoint | Override the OpenAI Responses endpoint |
| `AGENTPACK_PROXY_AUTH_JSON` | Codex auth discovery | Explicit Codex-compatible auth JSON path |
| `AGENTPACK_PROXY_BIG_MODEL` | `gpt-5.5` | Upstream model for Opus-class requests |
| `AGENTPACK_PROXY_MIDDLE_MODEL` | `gpt-5.4` | Upstream model for Sonnet-class requests |
| `AGENTPACK_PROXY_SMALL_MODEL` | `gpt-5.4-mini` | Upstream model for Haiku-class requests |

## GitHub access

### `GITHUB_TOKEN` / `GH_TOKEN`

**Default:** unset. When set, agentpack sends it as a bearer token for GitHub ref/tag lookups and authenticated downloads. Set one to resolve private repositories or to avoid anonymous API rate limits during heavy resolution. `GITHUB_TOKEN` takes precedence over `GH_TOKEN`.

## Binary paths

Override the path to each harness's executable when it isn't on `PATH` or you need a specific build:

| Variable | Binary |
|---|---|
| `CLAUDE_CODE_PATH` | `claude` |
| `OPENCODE_PATH` | `opencode` |
| `CODEX_PATH` | `codex` |
| `CURSOR_AGENT_PATH` | `cursor-agent` |
| `GROK_PATH` | `grok` |
| `AGY_PATH` | `agy` |

## Summary

| Variable | Default | Purpose |
|---|---|---|
| `AGENTPACK_HOME` | XDG / `%LOCALAPPDATA%` | Cache and state root |
| `AGENTPACK_STAGING_ROOT` | `<temp>/agentpack-<hash>` | Staging root |
| `AGENTPACK_KEEP_ATTRIBUTION` | unset | Keep AI attribution in staging |
| `AGENTPACK_DOT_AGENTS` | enabled | Merge `./.agents/` overlay |
| `AGENTPACK_PROXY_LOG_DIR` | project state dir | Claude proxy JSONL diagnostics |
| `AGENTPACK_PROXY_LOG_PAYLOADS` | unset | Include truncated payload snippets in proxy logs |
| `AGENTPACK_PROXY_LOG_MAX_BODY_BYTES` | `4096` | Payload snippet byte cap |
| `AGENTPACK_PROXY_TRANSPORT` | inferred | Proxy upstream transport |
| `AGENTPACK_PROXY_PORT` | `0` | Proxy loopback port |
| `AGENTPACK_PROXY_REQUEST_TIMEOUT_SECS` | `300` | Proxy request timeout |
| `AGENTPACK_PROXY_UPSTREAM_URL` | auth endpoint | Override proxy upstream URL |
| `AGENTPACK_PROXY_AUTH_JSON` | auto | Override proxy auth JSON |
| `AGENTPACK_PROXY_BIG_MODEL` / `MIDDLE_MODEL` / `SMALL_MODEL` | built in | Override proxy model mapping |
| `GITHUB_TOKEN` / `GH_TOKEN` | unset | GitHub auth for private repos and rate limits |
| `CLAUDE_CODE_PATH` … `AGY_PATH` | — | Override harness binary paths |
