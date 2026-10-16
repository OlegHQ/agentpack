#!/usr/bin/env -S uv run
# /// script
# requires-python = ">=3.11"
# dependencies = []
# ///
"""How often do the MCP servers that official plugin catalogs start unpinned publish a new version?

Every MCP server definition in the three official catalogs (Claude Code, Cursor, Codex; pinned commits
below) is read: any "mcpServers" object in any JSON file (.mcp.json, mcp.json, plugin manifests and
marketplace entries that declare servers inline). A server started through npx, bunx or pnpm dlx with no version, or with a dist-tag such
as @latest, runs whatever the registry serves at launch. For each such npm package the registry's
publication times are read, and the versions published in the WINDOW days before --as-of are counted.

    uv run drift.py [--as-of 2026-10-16] [--window 90] [--work DIR] [--output drift-results.json]

Network: git (shallow fetch of the pinned commits) and registry.npmjs.org. No model is called.
"""
import argparse, datetime as dt, json, re, subprocess, tempfile, time, urllib.error, urllib.request
from pathlib import Path

CATALOGS = {
    'claude': ('anthropics/claude-plugins-official', 'f359d43b406057f1a8cdbc4ea93b827642c162e6'),
    'cursor': ('cursor/plugins', '1dcc2bacde73ddd284803b7af28164d0436198fc'),
    'codex': ('openai/plugins', '0722921d5542fc593105c27bd52630babd8b8c2a'),
}
RUNNERS = {'npx', 'bunx', 'pnpm', 'npm'}

parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
parser.add_argument('--as-of', default='2026-10-16')
parser.add_argument('--window', type=int, default=90)
parser.add_argument('--work', type=Path)
parser.add_argument('--output', type=Path, default=Path(__file__).resolve().parent / 'drift-results.json')
args = parser.parse_args()
AS_OF = dt.datetime.fromisoformat(args.as_of).replace(tzinfo=dt.timezone.utc)
SINCE = AS_OF - dt.timedelta(days=args.window)


def checkout(work, repo, commit):
    d = work / repo.replace('/', '_')
    if not (d / '.git').exists():
        d.mkdir(parents=True)
        for cmd in (['init', '-q'], ['remote', 'add', 'origin', f'https://github.com/{repo}.git'],
                    ['fetch', '-q', '--depth', '1', 'origin', commit], ['checkout', '-q', 'FETCH_HEAD']):
            subprocess.run(['git', *cmd], cwd=d, check=True)
    return d


def servers(data, top=True):
    """Every server definition in a JSON document: under any "mcpServers" key, or at the top of an .mcp.json."""
    out = []
    if isinstance(data, dict):
        for k, v in data.items():
            if k == 'mcpServers' and isinstance(v, dict):
                out += [(n, e) for n, e in v.items() if isinstance(e, dict)]
            else:
                out += servers(v, False)
        if top and not out:
            out = [(n, e) for n, e in data.items() if isinstance(e, dict) and ('command' in e or 'url' in e)]
    elif isinstance(data, list):
        for v in data:
            out += servers(v, False)
    return out


def npm_spec(entry):
    """(package, requested version or tag) for a server started by an npm runner, else None."""
    cmd, argv = entry.get('command'), [str(a) for a in entry.get('args') or []]
    if not isinstance(cmd, str):
        return None
    parts = cmd.split() + argv
    if not parts or Path(parts[0]).name not in RUNNERS:
        return None
    rest = parts[1:]
    if Path(parts[0]).name in ('pnpm', 'npm'):          # pnpm dlx X, npm exec X
        if not rest or rest[0] not in ('dlx', 'exec', 'x'):
            return None
        rest = rest[1:]
    rest = [a for a in rest if not a.startswith('-')]
    if not rest:
        return None
    spec = rest[0]
    m = re.match(r'^(@[^/@]+/[^@]+|[^@][^@]*)(?:@(.+))?$', spec)
    if not m or m.group(1).startswith(('.', '/', '$')):
        return None
    return m.group(1), m.group(2) or ''


def registry(package):
    url = 'https://registry.npmjs.org/' + package.replace('/', '%2F')
    for attempt in range(4):
        try:
            with urllib.request.urlopen(urllib.request.Request(url, headers={'Accept': 'application/json'}), timeout=60) as r:
                return json.load(r)
        except urllib.error.HTTPError as e:
            if e.code == 404:
                return None
            time.sleep(2 ** attempt)
        except OSError:
            time.sleep(2 ** attempt)
    return None


work = args.work or Path(tempfile.mkdtemp(prefix='drift-'))
found, remote, local = [], [], []
for catalog, (repo, commit) in CATALOGS.items():
    root = checkout(work, repo, commit)
    for f in sorted(root.rglob('*')):
        if f.suffix != '.json' or '.git' in f.parts or 'node_modules' in f.parts or not f.is_file():
            continue
        try:
            data = json.loads(f.read_text())
        except (ValueError, UnicodeDecodeError):
            continue
        for name, entry in servers(data, f.name in ('.mcp.json', 'mcp.json')):
            if isinstance(entry.get('url'), str) or entry.get('type') in ('http', 'sse'):
                remote.append({'catalog': catalog, 'file': str(f.relative_to(root)), 'server': name})
                continue
            if isinstance(entry.get('command'), str):
                local.append({'catalog': catalog, 'file': str(f.relative_to(root)), 'server': name,
                              'command': Path(entry['command'].split()[0]).name if entry['command'].split() else ''})
            spec = npm_spec(entry)
            if spec:
                found.append({'catalog': catalog, 'file': str(f.relative_to(root)), 'server': name,
                              'package': spec[0], 'requested': spec[1]})

floating = [s for s in found if not re.match(r'^\d+\.\d+\.\d+', s['requested'])]
rows, cache = [], {}
for s in floating:
    pkg = s['package']
    if pkg not in cache:
        cache[pkg] = registry(pkg)
    meta = cache[pkg]
    if not meta:
        rows.append({**s, 'registry': 'not found'})
        continue
    times = {v: dt.datetime.fromisoformat(t.replace('Z', '+00:00')) for v, t in (meta.get('time') or {}).items()
             if v not in ('created', 'modified')}
    recent = sorted((t, v) for v, t in times.items() if SINCE < t <= AS_OF)
    rows.append({**s, 'versions_total': len(times), 'versions_in_window': len(recent),
                 'first_in_window': recent[0][1] if recent else None, 'last_in_window': recent[-1][1] if recent else None,
                 'latest_tag': (meta.get('dist-tags') or {}).get('latest')})

packages = {r['package']: r for r in rows if 'versions_in_window' in r}
changed = [p for p, r in packages.items() if r['versions_in_window'] > 0]
counts = sorted(r['versions_in_window'] for r in packages.values())
summary = {
    'as_of': args.as_of, 'window_days': args.window, 'catalogs': {k: {'repo': r, 'commit': c} for k, (r, c) in CATALOGS.items()},
    'server_definitions': len(remote) + len(local), 'remote_servers': len(remote), 'local_servers': len(local),
    'local_commands': {c: sum(1 for x in local if x['command'] == c) for c in sorted({x['command'] for x in local})},
    'npm_runner_servers': len(found), 'floating_servers': len(floating),
    'pinned_servers': len(found) - len(floating),
    'distinct_floating_packages': len(packages), 'packages_with_new_version_in_window': len(changed),
    'upper_middle_versions_in_window': counts[len(counts) // 2] if counts else None,
    'not_in_registry': sorted({r['package'] for r in rows if r.get('registry') == 'not found'}),
}
args.output.write_text(json.dumps({'summary': summary, 'servers': found, 'floating': rows, 'remote': remote, 'local': local}, indent=1) + '\n')
print(json.dumps(summary, indent=1))
