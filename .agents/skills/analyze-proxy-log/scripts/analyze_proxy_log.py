#!/usr/bin/env python3
import argparse
import hashlib
import json
import os
import sys
from collections import defaultdict
from pathlib import Path


ISSUE_KINDS = {
    "upstream_error": "upstream",
    "http_error": "upstream_http",
    "websocket_error": "websocket_timeout",
    "websocket_setup_error": "websocket_setup",
    "stream_bridge_error": "translation",
    "response_translate_error": "translation",
    "request_error": "translation",
    "downstream_response_error": "downstream",
}

TERMINAL_KINDS = {
    "http_complete",
    "websocket_complete",
    "websocket_terminal_frame",
    "websocket_error",
    "websocket_setup_error",
    "http_error",
    "upstream_error",
    "stream_bridge_error",
    "response_translate_error",
    "request_error",
    "downstream_response_error",
}


def main() -> int:
    parser = argparse.ArgumentParser(description="Analyze agentpack proxy JSONL logs")
    parser.add_argument("path", nargs="?", help="proxy log file or directory")
    args = parser.parse_args()

    log_path = resolve_log_path(args.path)
    events = load_events(log_path)
    if not events:
        print(f"No events found in {log_path}")
        return 1

    print(f"Log: {log_path}")
    print(f"Events: {len(events)}")
    print()

    summarize_session(events)
    print()
    summarize_requests(events)
    return 0


def resolve_log_path(raw):
    if raw:
        path = Path(raw).expanduser()
    elif os.environ.get("AGENTPACK_PROXY_LOG_DIR"):
        path = Path(os.environ["AGENTPACK_PROXY_LOG_DIR"]).expanduser()
    else:
        path = default_log_dir()

    if path.is_dir():
        latest = path / "latest.json"
        if latest.is_file():
            data = json.loads(latest.read_text())
            return Path(data["path"]).expanduser()
        candidates = sorted(path.glob("proxy-*.jsonl"), key=lambda p: p.stat().st_mtime)
        if candidates:
            return candidates[-1]
        raise SystemExit(f"No proxy-*.jsonl files found in {path}")
    if not path.is_file():
        raise SystemExit(f"Proxy log not found: {path}")
    return path


def default_log_dir():
    root = find_project_root(Path.cwd())
    agentpack_home = os.environ.get("AGENTPACK_HOME")
    if not agentpack_home:
        xdg = os.environ.get("XDG_DATA_HOME")
        agentpack_home = str(Path(xdg) / "agentpack") if xdg else str(Path.home() / ".local/share/agentpack")
    digest = hashlib.sha256(str(root.resolve()).encode()).digest()[:8].hex()
    return Path(agentpack_home) / "projects" / digest / "proxy-logs"


def find_project_root(start):
    for path in [start, *start.parents]:
        if (path / "agentpack.toml").is_file() or (path / "pack.lock").is_file():
            return path
    raise SystemExit("Could not find agentpack.toml or pack.lock from current directory")


def load_events(path):
    events = []
    with path.open() as handle:
        for line_no, line in enumerate(handle, 1):
            line = line.strip()
            if not line:
                continue
            try:
                event = json.loads(line)
            except json.JSONDecodeError as err:
                print(f"Skipping invalid JSON line {line_no}: {err}", file=sys.stderr)
                continue
            event["_line"] = line_no
            events.append(event)
    return events


def summarize_session(events):
    first = events[0]
    last = events[-1]
    print("Session")
    print(f"  first: {first.get('ts')} {first.get('kind')}")
    print(f"  last:  {last.get('ts')} {last.get('kind')}")
    starts = [e for e in events if e.get("kind") == "proxy_start"]
    if starts:
        start = starts[-1]
        print(
            "  transport: "
            f"{start.get('transport')} "
            f"(request_timeout={start.get('request_timeout_ms')}ms, "
            f"connect_timeout={start.get('connect_timeout_ms')}ms, "
            f"ws_idle={start.get('websocket_idle_timeout_ms')}ms)"
        )


def summarize_requests(events):
    grouped = defaultdict(list)
    global_events = []
    for event in events:
        request_id = event.get("request_id")
        if request_id is None:
            global_events.append(event)
        else:
            grouped[request_id].append(event)

    if not grouped:
        print("No request-scoped events found.")
        return

    print("Requests")
    for request_id in sorted(grouped, key=lambda x: int(x)):
        timeline = grouped[request_id]
        first = timeline[0]
        last = timeline[-1]
        issues = classify_issues(timeline)
        status = "LIKELY ISSUE" if issues else "ok"
        print(f"  request {request_id}: {status}")
        print(f"    span: {first.get('elapsed_ms')}ms -> {last.get('elapsed_ms')}ms")
        model = last_value(timeline, "upstream_request")
        if model:
            print(
                "    model: "
                f"{model.get('requested_model')} -> {model.get('upstream_model')} "
                f"via {model.get('transport')}"
            )
        if issues:
            print(f"    issue: {', '.join(issues)}")
            failing = first_failing_event(timeline)
            if failing:
                print(f"    first failing event: line {failing.get('_line')} {failing.get('kind')}")
                message = failing.get("error") or failing.get("message") or failing.get("reason")
                if message:
                    print(f"    message: {message}")
        else:
            print(f"    last event: line {last.get('_line')} {last.get('kind')}")


def classify_issues(timeline):
    issues = []
    kinds = [event.get("kind") for event in timeline]
    for kind in kinds:
        issue = ISSUE_KINDS.get(kind)
        if issue and issue not in issues:
            issues.append(issue)

    if "auth_refresh" in kinds and any(kind in kinds for kind in ("websocket_setup_error", "http_error")):
        if "auth_refresh" not in issues:
            issues.append("auth_refresh")

    if not any(kind in TERMINAL_KINDS for kind in kinds):
        issues.append("stalled_request")

    return issues


def first_failing_event(timeline):
    for event in timeline:
        if event.get("kind") in ISSUE_KINDS:
            return event
    return None


def last_value(timeline, kind):
    found = None
    for event in timeline:
        if event.get("kind") == kind:
            found = event
    return found


if __name__ == "__main__":
    raise SystemExit(main())
