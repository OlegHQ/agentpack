#!/usr/bin/env -S uv run
# /// script
# requires-python = ">=3.11"
# dependencies = []
# ///
"""What an npm-started MCP server actually runs after the registry changes, under four setups.

A local npm registry (this script) serves a toy MCP server `srv@1.0.0` that depends on `dep@^1.0.0`. The
server answers the MCP `initialize` request with the marker string of the `dep` code it loaded, so each
launch reports exactly which dependency ran. The server is set up three ways, each locked once while the
registry is in its original state:

  none           the definition as plugins publish it: npx -y srv@latest
  agentpack-top  Agentpack 0.6.2: srv pinned to 1.0.0 with its integrity in pack.lock, started by npx;
                 the top-level pin that other pinning tools also record
  agentpack-tree Agentpack 0.7.0: srv and every dependency locked in pack.lock, started by
                 `agentpack mcp-exec`

mcp-lock 0.1.0 is not a row: it reads only https://registry.npmjs.org (hard-coded, dist/mcp-lock.js line
4402), so it cannot be pointed at a test registry. By its README it records and pins the top-level package,
as agentpack-top does.

Then the registry changes, one scenario at a time, and each setup is launched the way an agent would start
it (the command its configuration holds), by a teammate with an empty npm cache and an empty Agentpack
cache:

  baseline       nothing changes
  drift          dep 1.0.1 is published (within ^1.0.0)
  dep-replaced   dep 1.0.0 is published again with other code and a matching new integrity in the
                 registry's metadata (a compromised registry or account)
  srv-replaced   the same for srv 1.0.0 itself
  cache-edited   no registry change; after one launch, the installed dep code is edited in the local
                 cache (npx's _npx directory, Agentpack's npm-trees), and the same user launches again

    uv run supplychain.py --agentpack-top AP062 --agentpack-tree AP070 [--output supplychain-results.json]

Needs node and npm on PATH; no network beyond 127.0.0.1. No model is called and no
login is used; every run has its own HOME.
"""
import argparse, base64, hashlib, http.server, io, json, os, subprocess, tarfile, tempfile, threading, time
from pathlib import Path

HERE = Path(__file__).resolve().parent
# names that do not exist on registry.npmjs.org (checked 2026-10-17), so a request that reached the public
# registry by mistake would fail instead of running someone else's code
SRV, DEP = 'approbe-srv-7f3c', 'approbe-dep-7f3c'
SETUPS = ['none', 'agentpack-top', 'agentpack-tree']
SCENARIOS = ['baseline', 'drift', 'dep-replaced', 'srv-replaced', 'cache-edited']

parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
parser.add_argument('--agentpack-top', type=Path, required=True, help='Agentpack 0.6.2 binary (top-level pin)')
parser.add_argument('--agentpack-tree', type=Path, required=True, help='Agentpack 0.7.0 binary (locked tree)')
parser.add_argument('--work', type=Path, default=None)
parser.add_argument('--output', type=Path, default=HERE / 'supplychain-results.json')
args = parser.parse_args()


# ------------------------------------------------------------------ the registry
def tgz(files):
    buf = io.BytesIO()
    with tarfile.open(fileobj=buf, mode='w:gz') as tar:
        for name, text in files.items():
            data = text.encode()
            info = tarfile.TarInfo('package/' + name)
            info.size, info.mode, info.mtime = len(data), 0o755 if name.endswith('cli.js') else 0o644, 0
            tar.addfile(info, io.BytesIO(data))
    return buf.getvalue()


def sri(data):
    return 'sha512-' + base64.b64encode(hashlib.sha512(data).digest()).decode()


SRV_CLI = r'''#!/usr/bin/env node
const marker = require('%s').marker;
const tag = '%s';
let buf = '';
process.stdin.on('data', d => {
  buf += d;
  let i;
  while ((i = buf.indexOf('\n')) >= 0) {
    const line = buf.slice(0, i); buf = buf.slice(i + 1);
    if (!line.trim()) continue;
    const msg = JSON.parse(line);
    if (msg.method === 'initialize') {
      process.stdout.write(JSON.stringify({jsonrpc: '2.0', id: msg.id, result: {protocolVersion: '2025-06-18', capabilities: {},
        serverInfo: {name: 'srv', version: tag + '+' + marker}}}) + '\n');
    }
  }
});
'''


def srv_package(tag):
    return tgz({'package.json': json.dumps({'name': SRV, 'version': '1.0.0', 'bin': {SRV: 'cli.js'}, 'dependencies': {DEP: '^1.0.0'}}),
                'cli.js': SRV_CLI % (DEP, tag)})


def dep_package(version, marker):
    return tgz({'package.json': json.dumps({'name': DEP, 'version': version, 'main': 'index.js'}),
                'index.js': f'module.exports = {{marker: {json.dumps(marker)}}};\n'})


class Registry:
    def __init__(self):
        self.packages = {}      # name -> {version: (bytes, deps, published)}
        self.lock = threading.Lock()
        handler = self.handler()
        self.server = http.server.ThreadingHTTPServer(('127.0.0.1', 0), handler)
        self.url = f'http://127.0.0.1:{self.server.server_address[1]}'
        threading.Thread(target=self.server.serve_forever, daemon=True).start()

    def publish(self, name, version, data, deps):
        with self.lock:
            self.packages.setdefault(name, {})[version] = (data, deps, time.strftime('%Y-%m-%dT%H:%M:%S.000Z', time.gmtime()))

    def packument(self, name):
        versions = self.packages[name]
        latest = sorted(versions, key=lambda v: tuple(int(x) for x in v.split('.')))[-1]
        return {'name': name, 'dist-tags': {'latest': latest},
                'versions': {v: {'name': name, 'version': v, 'dependencies': deps,
                                 **({'bin': {SRV: 'cli.js'}} if name == SRV else {}),
                                 'dist': {'tarball': f'{self.url}/{name}/-/{name}-{v}.tgz', 'integrity': sri(data)}}
                             for v, (data, deps, _) in versions.items()},
                'time': {v: t for v, (_, _, t) in versions.items()}}

    def handler(self):
        registry = self

        class H(http.server.BaseHTTPRequestHandler):
            def log_message(self, format, *a):
                pass

            def send(self, code, body, kind='application/json'):
                self.send_response(code)
                self.send_header('Content-Type', kind)
                self.send_header('Content-Length', str(len(body)))
                self.end_headers()
                self.wfile.write(body)

            def do_GET(self):
                parts = [p for p in self.path.split('?')[0].split('/') if p]
                with registry.lock:
                    if len(parts) == 3 and parts[1] == '-' and parts[0] in registry.packages:
                        version = parts[2][len(parts[0]) + 1:-len('.tgz')]
                        if version in registry.packages[parts[0]]:
                            return self.send(200, registry.packages[parts[0]][version][0], 'application/octet-stream')
                    if len(parts) == 1 and parts[0] in registry.packages:
                        return self.send(200, json.dumps(registry.packument(parts[0])).encode())
                    if len(parts) == 2 and parts[0] in registry.packages:
                        doc = registry.packument(parts[0])
                        version = doc['dist-tags'].get(parts[1], parts[1])
                        if version in doc['versions']:
                            return self.send(200, json.dumps(doc['versions'][version]).encode())
                return self.send(404, b'{"error":"not found"}')

            def do_POST(self):          # audit and similar endpoints
                return self.send(404, b'{}')
        return H


def initial_state(reg):
    reg.packages.clear()
    reg.publish(SRV, '1.0.0', srv_package('srv-1.0.0'), {DEP: '^1.0.0'})
    reg.publish(DEP, '1.0.0', dep_package('1.0.0', 'dep-1.0.0'), {})


def change(reg, scenario):
    if scenario == 'drift':
        reg.publish(DEP, '1.0.1', dep_package('1.0.1', 'dep-1.0.1'), {})
    elif scenario == 'dep-replaced':
        reg.publish(DEP, '1.0.0', dep_package('1.0.0', 'dep-1.0.0-REPLACED'), {})
    elif scenario == 'srv-replaced':
        reg.publish(SRV, '1.0.0', srv_package('srv-1.0.0-REPLACED'), {DEP: '^1.0.0'})


# ------------------------------------------------------------------ running
def env_for(home, reg, extra=None):
    e = {'PATH': os.environ['PATH'], 'HOME': str(home), 'USERPROFILE': str(home), 'XDG_DATA_HOME': str(home / '.local/share'),
         'XDG_CONFIG_HOME': str(home / '.config'), 'XDG_CACHE_HOME': str(home / '.cache'), 'npm_config_cache': str(home / '.npm'),
         'npm_config_registry': reg.url, 'npm_config_audit': 'false', 'npm_config_fund': 'false', 'npm_config_update_notifier': 'false',
         'NO_COLOR': '1', 'GIT_TERMINAL_PROMPT': '0', 'LANG': 'C.UTF-8'}
    e.update(extra or {})
    return e


def sh(argv, cwd, env, timeout=300, stdin=None):
    r = subprocess.run([str(a) for a in argv], cwd=cwd, env=env, capture_output=True, text=True, timeout=timeout, input=stdin)
    return r.returncode, (r.stdout + r.stderr).strip()


INIT = json.dumps({'jsonrpc': '2.0', 'id': 1, 'method': 'initialize',
                   'params': {'protocolVersion': '2025-06-18', 'capabilities': {}, 'clientInfo': {'name': 'probe', 'version': '1'}}}) + '\n'


def launch(command, cwd, env):
    """Start the server as an agent would, send initialize, return what ran (the marker) or the error."""
    p = subprocess.Popen([str(a) for a in command], cwd=cwd, env=env, stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
    try:
        out, err = p.communicate(INIT, timeout=120)
    except subprocess.TimeoutExpired:
        p.kill()
        out, err = p.communicate()
    for line in out.splitlines():
        try:
            msg = json.loads(line)
        except ValueError:
            continue
        version = msg.get('result', {}).get('serverInfo', {}).get('version')
        if version:
            return {'ran': version}
    return {'ran': None, 'stopped': (err or out).strip()[-400:]}


def mcp_entry(path):
    data = json.loads(Path(path).read_text())
    entry = (data.get('mcpServers') or data)['srv']
    return [entry['command'], *entry.get('args', [])]


def plugin_repo(root, reg):
    plugin = root / 'plugin'
    (plugin / '.claude-plugin').mkdir(parents=True)
    (plugin / '.claude-plugin/plugin.json').write_text(json.dumps({'name': 'srv-plugin', 'version': '1.0.0'}))
    (plugin / '.mcp.json').write_text(json.dumps({'mcpServers': {'srv': {'command': 'npx', 'args': ['-y', SRV + '@latest'],
                                                                         'env': {'npm_config_registry': reg.url}}}}))
    for cmd in (['init', '-q'], ['add', '-A'], ['-c', 'user.name=t', '-c', 'user.email=t@t', 'commit', '-qm', 'p']):
        subprocess.run(['git', *cmd], cwd=plugin, check=True)
    return plugin


def prepare(setup, root, reg):
    """Lock the setup once against the original registry; return a function that gives the launch command for a
    given HOME, and the lock steps."""
    proj, home = root / 'project', root / 'author-home'
    proj.mkdir(parents=True)
    home.mkdir()
    env = env_for(home, reg)
    steps = []
    if setup == 'none':
        cfg = proj / '.mcp.json'
        cfg.write_text(json.dumps({'mcpServers': {'srv': {'command': 'npx', 'args': ['-y', SRV + '@latest']}}}, indent=1))
        return proj, (lambda _: mcp_entry(cfg)), steps
    ap = args.agentpack_top if setup == 'agentpack-top' else args.agentpack_tree
    plugin = plugin_repo(root, reg)
    env['AGENTPACK_STAGING_ROOT'] = str(root / 'staging')
    subprocess.run(['git', 'init', '-q'], cwd=proj, check=True)
    for s in (['init'], ['add', plugin], ['lock'], ['sync']):
        steps.append(sh([ap, '--no-progress', *s], proj, env))
    staged = next((root / 'staging').rglob('plugins/agentpack-bundle/.mcp.json'))
    return proj, (lambda _: mcp_entry(staged)), steps


def edit_installed_dep(home):
    """Change the installed dep code in whichever local cache the launch used; return the files edited."""
    edited = []
    for f in home.rglob(f'node_modules/{DEP}/index.js'):
        f.write_text('module.exports = {marker: "dep-EDITED-IN-CACHE"};\n')
        edited.append(str(f.relative_to(home)))
    return edited


def main():
    base = (args.work or Path(tempfile.mkdtemp(prefix='supplychain-'))).resolve()
    base.mkdir(parents=True, exist_ok=True)
    reg = Registry()
    results = {'date': time.strftime('%Y-%m-%d'), 'node': sh(['node', '--version'], base, os.environ)[1],
               'npm': sh(['npm', '--version'], base, os.environ)[1],
               'agentpack_top': sh([args.agentpack_top, '--version'], base, os.environ)[1],
               'agentpack_tree': sh([args.agentpack_tree, '--version'], base, os.environ)[1],
               'agentpack_top_sha256': hashlib.sha256(args.agentpack_top.read_bytes()).hexdigest(),
               'agentpack_tree_sha256': hashlib.sha256(args.agentpack_tree.read_bytes()).hexdigest(),
               'runs': []}
    for scenario in SCENARIOS:
        for setup in SETUPS:
            initial_state(reg)
            root = base / scenario / setup
            proj, command_for, steps = prepare(setup, root, reg)
            row = {'scenario': scenario, 'setup': setup, 'lock_steps': [{'exit': c, 'tail': o[-300:]} for c, o in steps]}
            if scenario == 'cache-edited':
                home = root / 'user-home'
                home.mkdir()
                env = env_for(home, reg, {'AGENTPACK_STAGING_ROOT': str(root / 'staging')})
                row['first_launch'] = launch(command_for(home), proj, env)
                row['edited'] = edit_installed_dep(home)
                row['launch'] = launch(command_for(home), proj, env)
            else:
                change(reg, scenario)
                home = root / 'teammate-home'
                home.mkdir()
                env = env_for(home, reg, {'AGENTPACK_STAGING_ROOT': str(root / 'staging')})
                row['launch'] = launch(command_for(home), proj, env)
            row['command'] = [str(a).replace(str(base), '<work>') for a in command_for(home)]
            results['runs'].append(row)
            print(scenario, setup, row['launch'].get('ran') or 'STOPPED', flush=True)
    args.output.write_text(json.dumps(results, indent=1) + '\n')
    print('\n' + '\n'.join(f"{r['scenario']:13} {r['setup']:15} {r['launch'].get('ran') or 'stopped'}" for r in results['runs']))


main()
