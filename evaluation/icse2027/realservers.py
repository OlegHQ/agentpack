#!/usr/bin/env -S uv run
# /// script
# requires-python = ">=3.11"
# dependencies = []
# ///
"""Real npx-started MCP servers, locked once and started on more than one platform.

The servers: the four npm packages the official plugin repositories start through npx (as they declare them)
and six popular servers from npm, one of which (mcp-sqlite) needs a native module built by an install script.

  lock   (one host)   Agentpack locks every server: a plugin declaring the ten servers is added to a project,
                      and pack.lock records each server's tree. As the practitioner's baseline, a package.json
                      and package-lock.json per server are written with npm (the lock a team would commit).
                      Writes BUNDLE/ (plugin, project with agentpack.toml and pack.lock, baseline locks).
  start  (any host)   From BUNDLE/ as committed: `agentpack sync` (no lock, no network to GitHub), then each
                      server is started with the command its Claude Code configuration holds (agentpack mcp-exec)
                      and, separately, from the baseline with `npm ci` (install scripts run, as npm does by
                      default). Each start gets the MCP initialize and tools/list requests; a start succeeds when
                      tools/list answers. Also records the tree digest and whether the cache matched it.

    uv run realservers.py lock  --agentpack AP --bundle BUNDLE
    uv run realservers.py start --agentpack AP --bundle BUNDLE --output realservers-<host>.json

Needs node and npm on PATH. No model is called and no login is used; servers that need credentials get
placeholder environment values and are expected to list their tools without calling out.
"""
import argparse, hashlib, json, os, platform, shutil, subprocess, sys, tempfile, time
from pathlib import Path

SERVERS = {
    # the official catalogs' npx servers, as declared (Claude Code, Cursor and Codex plugin repositories)
    'playwright': {'command': 'npx', 'args': ['@playwright/mcp@latest', '--headless']},
    'firebase': {'command': 'npx', 'args': ['-y', 'firebase-tools@latest', 'mcp']},
    'xero': {'command': 'npx', 'args': ['-y', '@xeroapi/xero-mcp-server@latest'],
             'env': {'XERO_CLIENT_ID': 'placeholder', 'XERO_CLIENT_SECRET': 'placeholder'}},
    'xcodebuildmcp': {'command': 'npx', 'args': ['-y', 'xcodebuildmcp@latest', 'mcp']},
    # popular servers from npm
    'context7': {'command': 'npx', 'args': ['-y', '@upstash/context7-mcp@latest']},
    'filesystem': {'command': 'npx', 'args': ['-y', '@modelcontextprotocol/server-filesystem@latest', '.']},
    'memory': {'command': 'npx', 'args': ['-y', '@modelcontextprotocol/server-memory@latest']},
    'everything': {'command': 'npx', 'args': ['-y', '@modelcontextprotocol/server-everything@latest']},
    'chrome-devtools': {'command': 'npx', 'args': ['-y', 'chrome-devtools-mcp@latest']},
    'sqlite': {'command': 'npx', 'args': ['-y', 'mcp-sqlite@latest', 'probe.db']},
}

parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
parser.add_argument('action', choices=['lock', 'start'])
parser.add_argument('--agentpack', type=Path, required=True)
parser.add_argument('--bundle', type=Path, required=True)
parser.add_argument('--output', type=Path)
parser.add_argument('--timeout', type=int, default=120)
args = parser.parse_args()
AP = args.agentpack.resolve()
B = args.bundle.resolve()


def sh(argv, cwd, env=None, timeout=900):
    r = subprocess.run([str(a) for a in argv], cwd=cwd, env=env, capture_output=True, text=True, timeout=timeout)
    return r.returncode, (r.stdout + r.stderr).strip()


def env_for(home):
    e = dict(os.environ)
    e.update({'HOME': str(home), 'USERPROFILE': str(home), 'XDG_DATA_HOME': str(home / '.local/share'),
              'XDG_CONFIG_HOME': str(home / '.config'), 'XDG_CACHE_HOME': str(home / '.cache'),
              'npm_config_cache': str(home / '.npm'), 'npm_config_audit': 'false', 'npm_config_fund': 'false',
              'npm_config_update_notifier': 'false', 'AGENTPACK_STAGING_ROOT': str(home / 'staging'), 'NO_COLOR': '1'})
    return e


def package_of(spec):
    a = next(x for x in spec['args'] if not x.startswith('-'))
    name, _, _ = a.rpartition('@')
    return name or a


def lock():
    if B.exists():
        sys.exit(f'{B} exists')
    plugin, proj, base = B / 'plugin', B / 'project', B / 'baseline'
    (plugin / '.claude-plugin').mkdir(parents=True)
    (plugin / '.claude-plugin/plugin.json').write_text(json.dumps({'name': 'real-servers', 'version': '1.0.0'}))
    (plugin / '.mcp.json').write_text(json.dumps({'mcpServers': SERVERS}, indent=1))
    for cmd in (['init', '-q'], ['add', '-A'], ['-c', 'user.name=t', '-c', 'user.email=t@t', 'commit', '-qm', 'servers']):
        subprocess.run(['git', *cmd], cwd=plugin, check=True)
    proj.mkdir()
    subprocess.run(['git', 'init', '-q'], cwd=proj, check=True)
    home = Path(tempfile.mkdtemp(prefix='rs-lock-'))
    env = env_for(home)
    steps = [sh([AP, '--no-progress', *s], proj, env) for s in (['init'], ['add', '../plugin'], ['lock'])]
    lock = (proj / 'pack.lock').read_text()
    out = {'agentpack': sh([AP, '--version'], proj)[1], 'steps': [(c, o[-400:]) for c, o in steps], 'servers': {}}
    for name, spec in SERVERS.items():
        pkg = package_of(spec)
        d = base / name
        d.mkdir(parents=True)
        version = json.loads(sh(['npm', 'view', pkg, 'version', '--json'], d)[1])
        (d / 'package.json').write_text(json.dumps({'name': 'baseline-' + name, 'private': True, 'dependencies': {pkg: version}}, indent=1))
        code, text = sh(['npm', 'install', '--package-lock-only', '--no-audit', '--no-fund'], d, env)
        out['servers'][name] = {'package': pkg, 'version': version, 'baseline_lock': code == 0}
    shutil.rmtree(home, ignore_errors=True)
    (B / 'lock.json').write_text(json.dumps(out, indent=1))
    print(json.dumps(out, indent=1)[:3000])
    print('pack.lock bytes', len(lock))


INIT = [{'jsonrpc': '2.0', 'id': 1, 'method': 'initialize',
         'params': {'protocolVersion': '2025-06-18', 'capabilities': {}, 'clientInfo': {'name': 'probe', 'version': '1'}}},
        {'jsonrpc': '2.0', 'method': 'notifications/initialized'},
        {'jsonrpc': '2.0', 'id': 2, 'method': 'tools/list', 'params': {}}]


def handshake(argv, cwd, env):
    t = time.time()
    try:
        p = subprocess.Popen([str(a) for a in argv], cwd=cwd, env=env, stdin=subprocess.PIPE, stdout=subprocess.PIPE,
                             stderr=subprocess.PIPE, text=True)
    except OSError as e:
        return {'ok': False, 'error': str(e)}
    try:
        p.stdin.write(json.dumps(INIT[0]) + '\n')
        p.stdin.flush()
        tools, server, deadline = None, None, time.time() + args.timeout
        sent = False
        while time.time() < deadline:
            line = p.stdout.readline()
            if not line:
                break
            try:
                msg = json.loads(line)
            except ValueError:
                continue
            if msg.get('id') == 1 and not sent:
                server = (msg.get('result') or {}).get('serverInfo')
                p.stdin.write(json.dumps(INIT[1]) + '\n' + json.dumps(INIT[2]) + '\n')
                p.stdin.flush()
                sent = True
            elif msg.get('id') == 2:
                tools = len((msg.get('result') or {}).get('tools') or [])
                break
    finally:
        p.kill()
        try:
            _, err = p.communicate(timeout=10)
        except Exception:
            err = ''
    return {'ok': tools is not None, 'tools': tools, 'server': server, 'seconds': round(time.time() - t, 1),
            'stderr_tail': (err or '')[-300:]}


def start():
    home = Path(tempfile.mkdtemp(prefix='rs-start-'))
    env = env_for(home)
    work = home / 'bundle'
    shutil.copytree(B, work)
    proj = work / 'project'
    # the plugin is a local package: a teammate's clone has it at the same relative path, with its git history
    sync = sh([AP, '--no-progress', 'sync'], proj, env)
    staged = next((home / 'staging').rglob('plugins/agentpack-bundle/.mcp.json'))
    config = json.loads(staged.read_text())
    config = config.get('mcpServers', config)
    lock = (proj / 'pack.lock').read_text()
    res = {'host': platform.platform(), 'machine': platform.machine(), 'node': sh(['node', '--version'], proj)[1],
           'npm': sh(['npm', '--version'], proj)[1], 'agentpack': sh([AP, '--version'], proj)[1],
           'agentpack_sha256': hashlib.sha256(AP.read_bytes()).hexdigest(), 'pack_lock_sha256': hashlib.sha256(lock.encode()).hexdigest(),
           'sync': [sync[0], sync[1][-400:]], 'servers': {}}
    for name, spec in SERVERS.items():
        entry = config.get(name) or {}
        row = {'staged_command': [entry.get('command', ''), *entry.get('args', [])][:4]}
        senv = dict(env)
        senv.update(entry.get('env') or spec.get('env') or {})
        row['locked_tree'] = handshake([entry['command'], *entry['args']], proj, senv) if entry else {'ok': False, 'error': 'not staged'}
        # baseline: the committed package-lock, installed with npm ci, started with the package's executable
        bdir = work / 'baseline' / name
        code, text = sh(['npm', 'ci', '--no-audit', '--no-fund'], bdir, env)
        pkg = package_of(spec)
        pj = json.loads((bdir / 'node_modules' / pkg / 'package.json').read_text()) if code == 0 else {}
        bins = pj.get('bin') if isinstance(pj.get('bin'), dict) else ({pkg.split('/')[-1]: pj['bin']} if pj.get('bin') else {})
        exe = bins.get(pkg.split('/')[-1]) or (next(iter(bins.values())) if len(bins) == 1 else None)
        rest = spec['args'][spec['args'].index(next(a for a in spec['args'] if not a.startswith('-'))) + 1:]
        row['npm_ci'] = {'install_exit': code, 'install_tail': text[-200:]} if code != 0 or not exe else \
            handshake(['node', bdir / 'node_modules' / pkg / exe, *rest], proj, {**env, **(spec.get('env') or {})})
        res['servers'][name] = row
        print(name, 'locked tree:', 'ok' if row['locked_tree']['ok'] else 'FAIL', row['locked_tree'].get('tools'),
              '| npm ci:', 'ok' if row['npm_ci'].get('ok') else 'FAIL', row['npm_ci'].get('tools'), flush=True)
    trees = home / '.local/share/agentpack/cache/npm-trees'
    res['trees_installed'] = sorted(p.name for p in trees.iterdir()) if trees.exists() else []
    shutil.rmtree(home, ignore_errors=True)
    args.output.write_text(json.dumps(res, indent=1) + '\n')


lock() if args.action == 'lock' else start()
