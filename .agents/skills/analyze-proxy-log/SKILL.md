---
name: analyze-proxy-log
description: Analyze agentpack Claude proxy JSONL diagnostics for hangs, upstream errors, WebSocket setup failures, auth refresh problems, transport fallback, and stalled requests. Use when debugging `agentpack --proxy claude`, `AGENTPACK_PROXY_LOG_DIR`, `$AGENTPACK_HOME/projects/*/proxy-logs`, proxy hangs, proxy errors, or Codex/OpenAI proxy failures.
---

# Analyze Proxy Log

Use this skill to quickly diagnose `agentpack --proxy claude` failures from the proxy JSONL log.

## Workflow

1. Locate the log:
   - Prefer an explicit path from the user.
   - Else read `AGENTPACK_PROXY_LOG_DIR`.
   - Else find the nearest agentpack project root and use `$AGENTPACK_HOME/projects/<project-hash>/proxy-logs/latest.json`.
2. Run `scripts/analyze_proxy_log.py` with the log file or log directory.
3. Read the summary first, then inspect the request timelines flagged as `LIKELY ISSUE`.
4. Classify the primary failure:
   - `stalled_request`: request accepted but no terminal event.
   - `websocket_timeout`: connect/read/write timeout or closed-before-terminal WebSocket.
   - `websocket_setup`: 401/403/429 or setup frame error before a normal response.
   - `auth_refresh`: refresh attempted and failed or repeated authorization failure.
   - `upstream_http`: non-success HTTP status from Codex.
   - `translation`: request or response conversion failed.
   - `downstream`: Claude disconnected or response write failed.
5. Report the likely root cause, the request ID, transport, model mapping, first failing event, and the exact log path.

## Commands

```bash
python3 .agents/skills/analyze-proxy-log/scripts/analyze_proxy_log.py
python3 .agents/skills/analyze-proxy-log/scripts/analyze_proxy_log.py "$AGENTPACK_PROXY_LOG_DIR"
python3 .agents/skills/analyze-proxy-log/scripts/analyze_proxy_log.py /path/to/proxy-*.jsonl
```

Do not paste full payload snippets into the final answer unless the user explicitly asks. Logs are sanitized by default, but payload logging may be enabled with `AGENTPACK_PROXY_LOG_PAYLOADS=1`.
