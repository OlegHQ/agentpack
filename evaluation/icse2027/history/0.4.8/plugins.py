#!/usr/bin/env -S uv run
# /// script
# requires-python = ">=3.11"
# dependencies = []
# ///
"""Breadth test: published agent packages, as they are, on six coding agents through Agentpack.

Part 1  A corpus of public packages is selected by a fixed rule from repositories pinned by commit
        (see SOURCES). Each package is added to a fresh project with a fresh AGENTPACK_HOME, locked
        and synced. The package's own files say what it contains (skills, slash commands, subagents,
        rules, hooks, MCP servers); the staged tree of each agent is compared with that, item by item.
Part 2  For every package that locked, each agent's own CLI is started through the Agentpack launcher
        (or `agentpack probe`) with commands that send no prompt to a model, and what the agent lists
        is recorded: MCP servers, and skills, commands and subagents where the CLI can list them.
Part 3  Every MCP definition in the source repositories that is started with npx (or bunx, pnpm dlx, yarn dlx, bun x)
        is locked through the package that ships it and, separately, as a manifest entry. The lock
        record and each agent's MCP configuration are read, then sync is repeated with the network
        blocked, and `lock --update` is run under the same block as a control.

Nothing here calls a model or needs an account. HOME, the XDG directories, AGENTPACK_HOME and the
staging root are temporary directories, so no user configuration, credential or hook is read. During
the listings, PATH starts with stubs for node, npx, bun, uvx, docker and similar launchers, so no MCP
server shipped by a package is executed: an agent lists the server and reports that it did not start.
Pass --allow-server-start to remove the stubs. Agentpack keeps Codex homes under /var/tmp/ap-c/<hash>
whatever the staging root is; the script removes the ones it created.

    uv run plugins.py --agentpack bin/agentpack
"""
import argparse, concurrent.futures, datetime, hashlib, json, os, platform, re, shutil, subprocess, sys, tempfile, time, tomllib
from pathlib import Path

HERE = Path(__file__).resolve().parent
AGENTS = ['claude', 'codex', 'opencode', 'cursor', 'grok', 'agy']
KINDS = ['skill', 'command', 'agent', 'rule', 'hooks', 'mcp']
LAUNCHER = {'claude': 'claude', 'codex': 'codex', 'opencode': 'opencode', 'cursor': 'agent', 'grok': 'grok', 'agy': 'agy'}
BIN_ENV = {'claude': 'CLAUDE_CODE_PATH', 'codex': 'CODEX_PATH', 'opencode': 'OPENCODE_PATH', 'cursor': 'CURSOR_AGENT_PATH',
           'grok': 'GROK_PATH', 'agy': 'AGY_PATH'}
STUBS = ['node', 'npx', 'npm', 'bun', 'bunx', 'pnpm', 'yarn', 'deno', 'uv', 'uvx', 'pipx', 'docker', 'podman', 'php', 'tessl']

# Selection rule. Every repository is pinned to the commit that was its default-branch head on
# 2026-10-13. Within a directory the package directories are sorted by name (Python sorted()) and
# positions 1, 1+k, 1+2k, ... are taken; k is fixed per source so that each gives four to eight.
SOURCES = [
    dict(id='claude-official', format='Claude Code plugin', repo='anthropics/claude-plugins-official',
         commit='f359d43b406057f1a8cdbc4ea93b827642c162e6', dir='plugins', stride=5,
         rule='every 5th directory of plugins/ in the official Claude Code marketplace repository'),
    dict(id='claude-official-external', format='Claude Code plugin', repo='anthropics/claude-plugins-official',
         commit='f359d43b406057f1a8cdbc4ea93b827642c162e6', dir='external_plugins', stride=2,
         rule='every 2nd directory of external_plugins/ in the same repository'),
    dict(id='anthropic-skills', format='bare SKILL.md skill', repo='anthropics/skills',
         commit='dbd4588f9e1033efb41dad4bef2f7947c8993d44', dir='skills', stride=5,
         rule='every 5th directory of skills/ in anthropics/skills'),
    dict(id='cursor-official', format='Cursor plugin', repo='cursor/plugins',
         commit='1dcc2bacde73ddd284803b7af28164d0436198fc', dir='', stride=4, need='.cursor-plugin/plugin.json',
         rule='every 4th top-level directory with .cursor-plugin/plugin.json in the official Cursor marketplace repository'),
    dict(id='cursor-third-party', format='Cursor plugin', repo='cursor/plugins',
         commit='1dcc2bacde73ddd284803b7af28164d0436198fc', dir='third_party', stride=17,
         rule='every 17th directory of third_party/ in the same repository'),
    dict(id='codex-official', format='Codex plugin', repo='openai/plugins',
         commit='0722921d5542fc593105c27bd52630babd8b8c2a', dir='plugins', stride=12,
         rule='every 12th directory of plugins/ in the official Codex plugin repository'),
]
# Community marketplaces: the three most-starred repositories with the GitHub topic
# `claude-code-plugins` on 2026-10-13 (GET /search/repositories?q=topic:claude-code-plugins&sort=stars).
# Each is added at its root; where the root catalog lists several plugins, the first one with a
# local source is added as well.
COMMUNITY = [
    dict(repo='alirezarezvani/claude-skills', commit='19392f7a08264ed00486a251f5b2098321771f94', stars=28107),
    dict(repo='mksglu/context-mode', commit='1a1bed139906ecc048233583bb2fd29290603b7a', stars=27874),
    dict(repo='phuryn/pm-skills', commit='c68487d721e419ec87af1ae6412a842978eab785', stars=26955),
]
COMMUNITY_RULE = ('the three most-starred repositories with the GitHub topic claude-code-plugins on 2026-10-13: the '
                  'repository root, and for a root catalog with several plugins also its first plugin with a local source')

parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
parser.add_argument('--agentpack', type=Path, required=True, help='released agentpack binary')
for name, exe in (('claude', 'claude'), ('codex', 'codex'), ('opencode', 'opencode'), ('cursor', 'cursor-agent'),
                  ('grok', 'grok'), ('agy', 'agy')):
    parser.add_argument(f'--{name}', type=Path, default=shutil.which(exe), help=f'{exe} executable (default: from PATH)')
parser.add_argument('--work', type=Path, help='working directory (default: a temporary directory, removed at the end)')
parser.add_argument('--output', type=Path, default=HERE / 'plugins-results.json')
parser.add_argument('--corpus-output', type=Path, default=HERE / 'plugins-corpus.json')
parser.add_argument('--jobs', type=int, default=4)
parser.add_argument('--only', help='comma-separated package ids (debugging)')
parser.add_argument('--skip-listing', action='store_true', help='skip part 2')
parser.add_argument('--skip-pinning', action='store_true', help='skip part 3')
parser.add_argument('--keep', action='store_true', help='keep each project directory (about 20 MB per package) instead of removing it when done')
parser.add_argument('--allow-server-start', action='store_true', help='do not stub MCP server launchers during listings')
args = parser.parse_args()
AP = args.agentpack.resolve()
BINS = {a: (Path(getattr(args, a)).resolve() if getattr(args, a) else None) for a in AGENTS}


TIMES = []


def sh(cmd, cwd=None, env=None, timeout=300):
    """Run a command with output to files: some agents lose output beyond 64 KiB when it goes to a pipe."""
    started = time.time()
    with tempfile.TemporaryFile() as fo, tempfile.TemporaryFile() as fe:
        try:
            rc = subprocess.run([str(c) for c in cmd], cwd=cwd, env=env, stdout=fo, stderr=fe, timeout=timeout,
                                stdin=subprocess.DEVNULL).returncode
        except subprocess.TimeoutExpired:
            rc = 'timeout'
        fo.seek(0)
        fe.seek(0)
        out, err = fo.read().decode(errors='replace'), fe.read().decode(errors='replace')
    TIMES.append((round(time.time() - started, 1), ' '.join(str(c) for c in cmd[1:6])))
    return rc, out, err


ANSI = re.compile(r'\x1b\[[0-9;?]*[A-Za-z]')


def clean(text, base):
    return ANSI.sub('', unwrap(text)).replace(str(base), '<work>')


def unwrap(text):
    """Agentpack prints an error in a box wrapped at about 118 columns; put it back on one line."""
    lines = text.split('\n')
    for i, line in enumerate(lines):
        if line.strip() == 'ERROR':
            body, joined = [l for l in lines[i + 1:]], ''
            for l in body:
                if l.strip():
                    joined += l.strip() + ('' if l.rstrip().endswith('-') else ' ')
            return '\n'.join(lines[:i] + ['ERROR: ' + joined.strip()])
    return text


def messages(out, err, base):
    """Distinct non-empty output lines with how often each occurred."""
    counts = {}
    for line in clean(out + '\n' + err, base).splitlines():
        line = line.strip()
        if line:
            counts[line] = counts.get(line, 0) + 1
    return [{'text': k, 'count': v} for k, v in counts.items()]


def read_json(path):
    try:
        return json.loads(Path(path).read_text())
    except Exception:
        return None


# ---------------------------------------------------------------- sources and selection

def checkout(src_root, repo, commit):
    d = src_root / (repo.replace('/', '_') + '@' + commit[:12])
    if not (d / '.git').exists():
        d.mkdir(parents=True)
        for cmd in (['git', 'init', '-q'], ['git', 'remote', 'add', 'origin', f'https://github.com/{repo}.git'],
                    ['git', 'fetch', '-q', '--depth', '1', 'origin', commit], ['git', 'checkout', '-q', 'FETCH_HEAD']):
            subprocess.run(cmd, cwd=d, check=True)
    assert subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=d, text=True).strip() == commit
    return d


def select(src_root):
    corpus = []
    for s in SOURCES:
        root = checkout(src_root, s['repo'], s['commit'])
        parent = root / s['dir'] if s['dir'] else root
        dirs = sorted(d.name for d in parent.iterdir() if d.is_dir() and not d.name.startswith('.')
                      and (not s.get('need') or (d / s['need']).is_file()))
        for name in dirs[::s['stride']]:
            path = f"{s['dir']}/{name}" if s['dir'] else name
            corpus.append(dict(id=f"{s['id']}/{name}", source=s['id'], format=s['format'], repo=s['repo'], path=path,
                               commit=s['commit'], rule=s['rule'], universe=len(dirs)))
    for c in COMMUNITY:
        root = checkout(src_root, c['repo'], c['commit'])
        short = c['repo'].split('/')[1]
        corpus.append(dict(id=f'community/{short}', source='community', format='marketplace repository root',
                           repo=c['repo'], path='', commit=c['commit'], rule=COMMUNITY_RULE, stars=c['stars']))
        plugins = (read_json(root / '.claude-plugin/marketplace.json') or {}).get('plugins', [])
        local = [p for p in plugins if isinstance(p.get('source'), str) and p['source'].strip('./')]
        if len(plugins) > 1 and local:
            path = local[0]['source'].strip('./')
            corpus.append(dict(id=f'community/{short}/{path}', source='community', format='Claude Code plugin',
                               repo=c['repo'], path=path, commit=c['commit'], rule=COMMUNITY_RULE, stars=c['stars']))
    for p in corpus:
        p['url'] = f"https://github.com/{p['repo']}/tree/{p['commit']}/{p['path']}".rstrip('/')
        p['spec'] = (f"{p['repo']}/{p['path']}" if p['path'] else p['repo']) + '@' + p['commit']
        p['module'] = 'github.com/' + (f"{p['repo']}/{p['path']}" if p['path'] else p['repo'])
    return corpus


# ---------------------------------------------------------------- what a package contains

def frontmatter(text):
    if not text.startswith('---'):
        return {}, text
    end = text.find('\n---', 3)
    if end < 0:
        return {}, text
    meta = {}
    for line in text[3:end].splitlines():
        m = re.match(r'^([A-Za-z_-]+):\s*(.*)$', line)
        if m:
            meta[m.group(1)] = m.group(2).strip().strip('"\'')
    return meta, text[end + 4:]


def probe_line(text):
    """A distinctive line of the body, used to tell whose content a staged file holds."""
    _, body = frontmatter(text)
    for line in body.splitlines():
        line = ' '.join(line.split())
        if len(line) >= 16 and not line.startswith(('---', '```', '<!--', '|')):
            return line[:160]
    return None


def norm(text):
    return ' '.join(text.split())


def md_item(path, name):
    text = path.read_text(errors='replace')
    meta, _ = frontmatter(text)
    return {'name': name, 'declared_name': meta.get('name') or name, 'file': None, 'line': probe_line(text)}


def servers_of(data):
    """MCP servers of a JSON document: wrapped in mcpServers, or the bare name-to-server map."""
    if not isinstance(data, dict):
        return None, {}
    if isinstance(data.get('mcpServers'), dict):
        return 'mcpServers', data['mcpServers']
    flat = {k: v for k, v in data.items() if isinstance(v, dict) and ('command' in v or 'url' in v)}
    return ('bare map', flat) if flat else (None, {})


def hook_handlers(data):
    hooks = data.get('hooks', data) if isinstance(data, dict) else {}
    n = 0
    events = []
    if isinstance(hooks, dict):
        for event, entries in hooks.items():
            if isinstance(entries, list):
                events.append(event)
                for e in entries:
                    n += len(e['hooks']) if isinstance(e, dict) and isinstance(e.get('hooks'), list) else 1
    return n, sorted(events)


def inventory(root, pkg, catalog_entry=None):
    """What the package says it contains, from its own files."""
    inv = {'markers': [], 'skill': [], 'command': [], 'agent': [], 'rule': [], 'hooks': [], 'mcp': [], 'other': [],
           'skill_md_outside_standard_paths': 0}
    rel = lambda p: str(p.relative_to(pkg))
    manifests = {}
    for m in ('.claude-plugin/plugin.json', '.cursor-plugin/plugin.json', '.codex-plugin/plugin.json',
              '.claude-plugin/marketplace.json', '.cursor-plugin/marketplace.json', '.agents/plugins/marketplace.json',
              'agentpack.toml', 'SKILL.md'):
        if (pkg / m).is_file():
            inv['markers'].append(m)
            if m.endswith('plugin.json'):
                manifests[m] = read_json(pkg / m) or {}

    def declared(key):
        out = []
        for m in manifests.values():
            v = m.get(key)
            for item in (v if isinstance(v, list) else [v]):
                if isinstance(item, str):
                    p = (pkg / item).resolve()
                    if p.exists() and pkg.resolve() in (p, *p.parents):
                        out.append(p)
        return out

    seen = set()

    def add_skill(d):
        d = d.resolve()
        if d in seen or not (d / 'SKILL.md').is_file():
            return
        seen.add(d)
        item = md_item(d / 'SKILL.md', d.name if d != pkg.resolve() else (pkg.name or root.name.split('@')[0].split('_', 1)[-1]))
        meta, _ = frontmatter((d / 'SKILL.md').read_text(errors='replace'))
        hidden = []
        policy = d / 'agents/openai.yaml'
        if policy.is_file() and re.search(r'allow_implicit_invocation:\s*false', policy.read_text(errors='replace')):
            hidden.append('codex')      # the package tells Codex not to offer this skill to the model
        if meta.get('user-invocable') == 'false':
            hidden.append('claude')     # Claude Code leaves such a skill out of the session's command list
        if hidden:
            item['hidden_by_own_metadata_for'] = hidden
        item['file'] = rel(d / 'SKILL.md') if d != pkg.resolve() else 'SKILL.md'
        inv['skill'].append(item)

    add_skill(pkg)
    for base in [pkg / 'skills', *declared('skills')]:
        if base.is_dir():
            add_skill(base)
            for d in sorted(base.iterdir()):
                if d.is_dir():
                    add_skill(d)
    skip = ('.git', 'node_modules')
    every = [p for p in pkg.rglob('SKILL.md') if not any(part in skip for part in p.relative_to(pkg).parts)]
    inv['skill_md_outside_standard_paths'] = len([p for p in every if p.parent.resolve() not in seen])

    def add_md(kind, base, suffixes=('.md',)):
        if base.is_file():
            files = [base]
            top = base.parent
        elif base.is_dir():
            files = sorted(p for p in base.rglob('*') if p.is_file() and p.suffix in suffixes)
            top = base
        else:
            return
        for f in files:
            if f.name in ('README.md', 'CLAUDE.md', 'AGENTS.md'):
                continue
            name = str(f.relative_to(top).with_suffix(''))
            if any(i['file'] == rel(f) for i in inv[kind]):
                continue
            item = md_item(f, name)
            item['file'] = rel(f)
            inv[kind].append(item)

    for base in [pkg / 'commands', *declared('commands')]:
        add_md('command', base)
    for base in [pkg / 'agents', *declared('agents')]:
        add_md('agent', base)
    add_md('rule', pkg / 'rules', ('.md', '.mdc'))

    hook_files = [pkg / 'hooks/hooks.json', pkg / 'hooks.json', pkg / '.codex-plugin/hooks.json', *declared('hooks')]
    done = set()
    for f in hook_files:
        if f.is_file() and f.resolve() not in done:
            done.add(f.resolve())
            n, events = hook_handlers(read_json(f) or {})
            inv['hooks'].append({'name': rel(f), 'file': rel(f), 'handlers': n, 'events': events})
    for m, data in manifests.items():
        if isinstance(data.get('hooks'), dict):
            n, events = hook_handlers(data['hooks'])
            inv['hooks'].append({'name': m + '#hooks', 'file': m, 'handlers': n, 'events': events})

    def add_mcp(file, data):
        form, servers = servers_of(data)
        for name, server in servers.items():
            entry = {'name': name, 'file': file, 'form': form, 'command': server.get('command'), 'args': server.get('args'),
                     'url': server.get('url'), 'type': server.get('type')}
            same = [e for e in inv['mcp'] if e['name'] == name]
            if same:
                same[0].setdefault('also_in', []).append({k: entry[k] for k in ('file', 'form', 'command', 'args', 'url')})
            else:
                inv['mcp'].append(entry)

    for f in ('.mcp.json', 'mcp.json', '.codex-plugin/mcp.json', *[rel(p) for p in declared('mcpServers')]):
        if (pkg / f).is_file():
            add_mcp(f, read_json(pkg / f))
    for m, data in manifests.items():
        if isinstance(data.get('mcpServers'), dict):
            add_mcp(m + '#mcpServers', {'mcpServers': data['mcpServers']})
    if catalog_entry:
        if isinstance(catalog_entry.get('mcpServers'), dict):
            add_mcp('marketplace.json entry#mcpServers', {'mcpServers': catalog_entry['mcpServers']})
        if catalog_entry.get('lspServers'):
            inv['other'].append('LSP servers declared in the marketplace catalog entry: ' + ', '.join(catalog_entry['lspServers']))
    for f, label in (('.app.json', 'Codex app connector (.app.json)'), ('.lsp.json', 'LSP servers (.lsp.json)'),
                     ('output-styles', 'output styles'), ('AGENTS.md', 'AGENTS.md'), ('CLAUDE.md', 'CLAUDE.md'),
                     ('settings.json', 'settings.json'), ('monitors', 'monitors'), ('bin', 'bin/')):
        if (pkg / f).exists():
            inv['other'].append(label)
    market = read_json(pkg / '.claude-plugin/marketplace.json')
    if market:
        inv['marketplace_plugins'] = len(market.get('plugins', []))
    return inv


# ---------------------------------------------------------------- the staged trees

def discard(base, project_hash):
    """Remove a finished project, and the Codex home Agentpack keeps for it under /var/tmp/ap-c."""
    if args.keep:
        return
    shutil.rmtree(base, ignore_errors=True)
    if project_hash:
        shutil.rmtree(Path('/var/tmp/ap-c') / project_hash, ignore_errors=True)


def staged_roots(base):
    modes = next(iter((base / 'staging/projects').glob('*/modes/default')), None)
    project = next(iter((base / 'ap/projects').glob('*')), None)
    if not modes or not project:
        return None
    codex = None
    pointer = project / 'codex-generations/default.current'
    if pointer.is_file():
        codex = Path(pointer.read_text().strip())
    return {'claude': modes / 'plugins/agentpack-bundle', 'cursor': modes / 'cursor/agentpack-bundle',
            'opencode': modes / 'opencode', 'codex': codex, 'grok': modes / 'grok/agentpack-bundle',
            'agy': modes / 'agy/agentpack-bundle', 'grok_home': project / 'grok-homes/default',
            'cursor_home': modes / 'cursor-home/.cursor', 'project_hash': project.name}


def staged_mcp(roots):
    def toml_servers(path):
        try:
            return tomllib.loads(Path(path).read_text()).get('mcp_servers', {})
        except Exception:
            return {}
    out = {
        'claude': (read_json(roots['claude'] / '.mcp.json') or {}).get('mcpServers', {}),
        'cursor': (read_json(roots['cursor'] / 'mcp.json') or {}).get('mcpServers', {}),
        'opencode': (read_json(roots['opencode'] / 'opencode.json') or {}).get('mcp', {}),
        'codex': toml_servers(roots['codex'] / 'config.toml') if roots['codex'] else {},
        'grok': toml_servers(roots['grok_home'] / 'config.toml'),
        'agy': (read_json(roots['agy'] / 'mcp_config.json') or {}).get('mcpServers', {}),
    }
    return {a: (v if isinstance(v, dict) else {}) for a, v in out.items()}


# where each kind is expected for each agent: (class, path template); first hit wins
def candidates(agent, kind, item):
    name, declared = item['name'], item.get('declared_name') or item['name']
    leaf = name.split('/')[-1]
    skill = [f'skills/{n}/SKILL.md' for n in dict.fromkeys([leaf, declared, leaf.lstrip('_')])]
    if kind == 'skill':
        return [('native', p) for p in skill]
    if kind in ('command', 'agent'):
        folder = 'commands' if kind == 'command' else 'agents'
        if agent == 'codex':
            return [('substitute', p) for p in skill]
        return [('native', f'{folder}/{name}.md'), ('native', f'{folder}/{leaf}.md')]
    if kind == 'rule':
        files = [f'rules/{name}{ext}' for ext in ('.mdc', '.md')] + [f'rules/{leaf}{ext}' for ext in ('.mdc', '.md')]
        if agent in ('cursor', 'agy'):
            return [('native', p) for p in files]
        return [('substitute', p) for p in skill] + [('copied', p) for p in files]
    return []


def compare(inv, roots):
    """For each agent and kind, where each source item ended up."""
    cells, detail = {}, {}
    mcp = staged_mcp(roots)
    for agent in AGENTS:
        root = roots[agent]
        cells[agent], detail[agent] = {}, {}
        for kind in ('skill', 'command', 'agent', 'rule'):
            count = dict(source=len(inv[kind]), native=0, substitute=0, copied=0, replaced=0, dropped=0)
            rows = []
            for item in inv[kind]:
                found = None
                for cls, relpath in candidates(agent, kind, item):
                    f = root / relpath if root else None
                    if f and f.is_file():
                        found = (cls, relpath, f)
                        break
                if not found:
                    count['dropped'] += 1
                    rows.append({'item': item['name'], 'result': 'dropped'})
                    continue
                cls, relpath, f = found
                same = None
                if item.get('line'):
                    same = item['line'] in norm(f.read_text(errors='replace'))
                if same is False:
                    cls = 'replaced'
                count[cls] += 1
                if cls != 'native':
                    rows.append({'item': item['name'], 'result': cls, 'staged_as': relpath})
            cells[agent][kind] = count
            detail[agent][kind] = rows
        # hooks: one item per hooks file of the package
        count = dict(source=min(1, len(inv['hooks'])), native=0, substitute=0, copied=0, replaced=0, dropped=0)
        note = None
        if inv['hooks']:
            where = {'claude': 'hooks/hooks.json', 'cursor': 'hooks/hooks.json', 'codex': 'hooks.json',
                     'opencode': 'plugins/agentpack-hooks/index.js', 'grok': 'hooks/hooks.json', 'agy': 'hooks/hooks.json'}[agent]
            f = root / where if root else None
            cls = 'dropped'
            if f and f.is_file():
                cls = {'claude': 'native', 'cursor': 'native', 'codex': 'native', 'opencode': 'substitute',
                       'grok': 'copied', 'agy': 'copied'}[agent]
                if f.suffix == '.json':
                    n, events = hook_handlers(read_json(f) or {})
                    note = {'staged_as': where, 'handlers': n, 'events': events}
                else:
                    note = {'staged_as': where}
            count[cls] += 1
        cells[agent]['hooks'] = count
        detail[agent]['hooks'] = [note] if note else []
        names = [s['name'] for s in inv['mcp']]
        have = [n for n in names if n in mcp[agent]]
        cells[agent]['mcp'] = dict(source=len(names), native=len(have), substitute=0, copied=0, replaced=0,
                                   dropped=len(names) - len(have))
        detail[agent]['mcp'] = [{'item': n, 'result': 'dropped'} for n in names if n not in mcp[agent]]
        # staged entries that no source item accounts for (other than Agentpack's own files)
        if root and root.is_dir():
            extra = {}
            for folder, pattern in (('skills', '*/SKILL.md'), ('commands', '**/*.md'), ('agents', '**/*.md')):
                if (root / folder).is_dir():
                    extra[folder] = len([p for p in (root / folder).glob(pattern) if p.is_file()])
            detail[agent]['staged_file_counts'] = extra
    return cells, detail, mcp


# ---------------------------------------------------------------- running agentpack

def environment(base, stubs=None, offline=False):
    home = base / 'home'
    home.mkdir(parents=True, exist_ok=True)
    env = {'PATH': os.environ['PATH'], 'HOME': str(home), 'USERPROFILE': str(home), 'XDG_CONFIG_HOME': str(home / 'config'),
           'XDG_DATA_HOME': str(home / 'data'), 'XDG_CACHE_HOME': str(home / 'cache'), 'XDG_STATE_HOME': str(home / 'state'),
           'AGENTPACK_HOME': str(base / 'ap'), 'AGENTPACK_STAGING_ROOT': str(base / 'staging'), 'CI': '1', 'NO_COLOR': '1',
           'TERM': 'dumb'}
    for agent, var in BIN_ENV.items():
        if BINS[agent]:
            env[var] = str(BINS[agent])
    if stubs:
        env['PATH'] = f'{stubs}:/usr/bin:/bin'
    if offline:
        dead = 'http://127.0.0.1:9'
        env.update({'HTTP_PROXY': dead, 'HTTPS_PROXY': dead, 'ALL_PROXY': dead, 'http_proxy': dead, 'https_proxy': dead,
                    'all_proxy': dead, 'NO_PROXY': '', 'no_proxy': '', 'npm_config_registry': dead + '/'})
    return env


def project(base):
    ws = base / 'ws'
    ws.mkdir(parents=True)
    subprocess.run(['git', 'init', '-q', str(ws)], check=True)
    return ws


def step(steps, base, ws, env, *cmd, timeout=600):
    rc, out, err = sh([AP, '--no-progress', *cmd], cwd=ws, env=env, timeout=timeout)
    steps.append({'command': 'agentpack ' + ' '.join(str(c).replace(str(base), '<work>') for c in cmd), 'exit': rc,
                  'messages': messages(out, err, base)})
    return rc


def read_lock(ws):
    try:
        return tomllib.loads((ws / 'pack.lock').read_text())
    except Exception:
        return {}


# ---------------------------------------------------------------- part 2: what the agents list

def listed(name, text):
    return re.search(r'(?m)(^|[\s:●✓⚠✗])' + re.escape(name) + r'($|[\s:,])', text) is not None


def listing(base, ws, env, inv, lock_ok):
    """Ask each agent, with no prompt, what it has. Returns per agent the raw material and names."""
    out = {}

    def run(agent, *cmd, timeout=180):
        rc, so, se = sh([AP, '-q', '--no-progress', *cmd], cwd=ws, env=env, timeout=timeout)
        return rc, clean(so, base), clean(se, base)

    def probe(agent):
        run(agent, 'sync')
        rc, so, se = run(agent, 'probe', '--agent', agent, '--json', timeout=900)
        try:
            receipt = json.loads(so)
        except Exception:
            return {'exit': rc, 'error': messages('', se, base)[:6]}
        names = {k: [] for k in ('skill', 'command', 'agent')}
        for p in receipt.get('properties', []):
            if p.get('scope') == 'workspace_session' and p.get('property') == 'presence' and p.get('value') is True \
                    and p.get('category') in names:
                names[p['category']].append(p['artifact_id'].split('/', 2)[-1] if agent == 'claude'
                                            else p['artifact_id'].split('/', 1)[-1])
        return {'exit': rc, 'overall_status': receipt.get('overall_status'), 'model_calls': receipt.get('effects', {}).get('model_calls'),
                'findings': [f"{f.get('code')}: {f.get('message')}" for f in receipt.get('findings', []) if f.get('severity') != 'info'],
                'coverage': [f"{c['category']}/{c['scope']}: {c['completeness']} ({c.get('reason')})" for c in receipt.get('coverage', [])],
                'names': names}

    # Probes first: a launcher run changes the inputs a probe checks (it reports STAGING_INPUTS_STALE afterwards).
    probes = {a: probe(a) for a in ('claude', 'codex') if BINS[a]}
    for agent in AGENTS:
        if not BINS[agent]:
            out[agent] = {'skipped': 'executable not given'}
            continue
        entry = {'commands': []}
        rc, so, se = run(agent, LAUNCHER[agent], 'mcp', 'list')
        entry['mcp_list'] = {'command': f'agentpack {LAUNCHER[agent]} mcp list', 'exit': rc, 'output': so.strip()[:6000],
                             'stderr': [m for m in messages('', se, base) if not m['text'].startswith('warning: ')][:10]}
        names = {'skill': None, 'command': None, 'agent': None}
        how = {}
        if agent in ('claude', 'codex'):
            entry['probe'] = probes[agent]
            if 'names' in entry['probe']:
                names = entry['probe'].pop('names')
                how = dict.fromkeys(names, f'agentpack probe --agent {agent} (session catalog, no model call)')
                if agent == 'codex':
                    # Codex has skills only; the probe reports each packaged item once, under its own kind
                    everything = sorted(set(names['skill']) | set(names['command']) | set(names['agent']))
                    names = {'skill': everything, 'command': everything, 'agent': everything}
        elif agent == 'opencode':
            # `agentpack probe --agent opencode` samples the same command 60 times (minutes per package); once is enough here
            rc, so, se = run(agent, 'opencode', 'debug', 'skill', '--pure')
            skills = read_json_text(so)
            if isinstance(skills, list):
                names['skill'] = [s.get('name') for s in skills if '<work>' in str(s.get('location'))]
                how['skill'] = 'opencode debug skill --pure'
            else:
                entry['commands'].append({'command': 'opencode debug skill --pure', 'exit': rc, 'error': (se or so)[-600:]})
            rc, so, se = run(agent, 'opencode', 'debug', 'config', '--pure')
            config = read_json_text(so)
            if isinstance(config, dict):
                names['command'] = sorted((config.get('command') or {}).keys())
                names['agent'] = sorted((config.get('agent') or {}).keys())
                entry['config_mcp'] = sorted((config.get('mcp') or {}).keys())
                how['command'] = how['agent'] = 'opencode debug config --pure'
            else:
                entry['commands'].append({'command': 'opencode debug config --pure', 'exit': rc, 'error': (se or so)[-600:]})
        elif agent == 'grok':
            rc, so, se = run(agent, 'grok', 'inspect', '--json')
            data = read_json_text(so)
            if isinstance(data, dict):
                skills = [s.get('name') for s in data.get('skills', [])]
                names['skill'] = names['command'] = skills    # Grok lists plugin commands among its skills
                names['agent'] = [a.get('name', '').split(':')[-1] for a in data.get('agents', [])
                                  if a.get('source', {}).get('type') != 'builtin']
                entry['inspect'] = {'plugins': [{k: p.get(k) for k in ('name', 'enabled', 'provides')} for p in data.get('plugins', [])],
                                    'mcpServers': [m.get('name') for m in data.get('mcpServers', [])],
                                    'hooks': len(data.get('hooks', []))}
                how = dict.fromkeys(names, 'grok inspect --json')
            else:
                entry['commands'].append({'command': 'grok inspect --json', 'exit': rc, 'error': (se or so)[-600:]})
        elif agent == 'agy':
            rc, so, se = run(agent, 'agy', 'agents')
            entry['commands'].append({'command': 'agy agents', 'exit': rc, 'output': (so + se).strip()[:3000]})
            if rc == 0:
                names['agent'] = [l.strip() for l in so.splitlines() if l.strip()]
                how['agent'] = 'agy agents'
            rc, so, se = run(agent, 'agy', 'plugin', 'validate', '.agents/plugins/agentpack-bundle')
            entry['plugin_validate'] = {'command': 'agy plugin validate .agents/plugins/agentpack-bundle (static check of the staged plugin, not a session listing)',
                                        'exit': rc, 'output': (so + se).strip()[:1500]}
        entry['how'] = how
        entry['listed_names'] = {k: (sorted(set(v))[:400] if v is not None else None) for k, v in names.items()}
        # compare with the package's own items
        seen = {}
        for kind in ('skill', 'command', 'agent'):
            have = names.get(kind)
            items = inv[kind]
            if have is None:
                seen[kind] = {'source': len(items), 'expected': len(items), 'listed': None}
                continue
            pool = set(have) | {h.lstrip('_') for h in have if h}
            hidden = [i['name'] for i in items if agent in i.get('hidden_by_own_metadata_for', [])]
            missing = [i['name'] for i in items if i['name'] not in hidden
                       and not ({i['name'], i['name'].split('/')[-1], i['declared_name'], i['name'].split('/')[-1].lstrip('_')} & pool)]
            seen[kind] = {'source': len(items), 'hidden_by_own_metadata': len(hidden), 'expected': len(items) - len(hidden),
                          'listed': len(items) - len(hidden) - len(missing), 'not_listed': missing}
        servers = [s['name'] for s in inv['mcp']]
        text = entry['mcp_list']['output']
        missing = [n for n in servers if not listed(n, text)]
        seen['mcp'] = {'source': len(servers), 'expected': len(servers), 'listed': len(servers) - len(missing), 'not_listed': missing}
        entry['seen'] = seen
        out[agent] = entry
    return out


def read_json_text(text):
    start = min([i for i in (text.find('['), text.find('{')) if i >= 0], default=-1)
    if start < 0:
        return None
    try:
        return json.loads(text[start:])
    except Exception:
        return None


# ---------------------------------------------------------------- one package

def run_package(p, work, src_root, stubs):
    base = work / 'pkg' / p['id'].replace('/', '__')
    shutil.rmtree(base, ignore_errors=True)
    ws = project(base)
    env = environment(base)
    root = checkout(src_root, p['repo'], p['commit'])
    pkg = root / p['path'] if p['path'] else root
    entry = None
    for catalog in ('.claude-plugin/marketplace.json', '.cursor-plugin/marketplace.json', '.agents/plugins/marketplace.json'):
        for e in (read_json(root / catalog) or {}).get('plugins', []):
            src = e.get('source')
            if p['path'] and isinstance(src, str) and src.strip('./') == p['path']:
                entry = entry or e
    inv = inventory(root, pkg, entry)
    row = dict(p, contains={k: (min(1, len(inv[k])) if k == 'hooks' else len(inv[k])) for k in KINDS}, inventory=inv, steps=[])
    steps = row['steps']
    step(steps, base, ws, env, 'init', '--name', 'breadth')
    add = step(steps, base, ws, env, 'add', p['spec'])
    # `add` locks and then syncs; when the sync part fails the package is already in pack.lock, so go on
    written = any(x.get('module') == p['module'] for x in read_lock(ws).get('packages', []))
    lock_rc = step(steps, base, ws, env, 'lock') if written else None
    sync_rc = step(steps, base, ws, env, 'sync') if written else None
    lock = read_lock(ws)
    mine = [x for x in lock.get('packages', []) if x.get('module') == p['module']]
    row['lock'] = {
        'packages': [{k: x.get(k) for k in ('module', 'direct', 'kind', 'name', 'path', 'commit', 'content_hash')} for x in lock.get('packages', [])],
        'mcp_servers': lock.get('mcp_servers', []),
        'resolved_and_locked': bool(mine and mine[0].get('commit') == p['commit'] and str(mine[0].get('content_hash', '')).startswith('sha256')),
    }
    row['exit'] = {'add': add, 'lock': lock_rc, 'sync': sync_rc}
    roots = staged_roots(base)
    row['staged'] = bool(roots and sync_rc == 0)
    if row['staged']:
        row['cells'], row['cell_detail'], mcp = compare(inv, roots)
        row['staged_mcp'] = mcp
        row['project_hash'] = roots['project_hash']
        rc, so, se = sh([AP, '--no-progress', 'mcp', 'list'], cwd=ws, env=env)
        row['mcp_list'] = clean(so + se, base).strip()
        if not args.skip_listing:
            row['listing'] = listing(base, ws, environment(base, stubs), inv, True)
    elif roots:
        row['project_hash'] = roots['project_hash']
    discard(base, row.get('project_hash'))
    print(f"  {p['id']}: add={add} lock={lock_rc} sync={sync_rc} locked={row['lock']['resolved_and_locked']}", file=sys.stderr, flush=True)
    return row


# ---------------------------------------------------------------- part 3: pinning

def npm_servers(src_root):
    """Every MCP server started with an npm launcher anywhere in the source repositories."""
    found = []
    repos = {(s['repo'], s['commit']) for s in SOURCES} | {(c['repo'], c['commit']) for c in COMMUNITY}
    for repo, commit in sorted(repos):
        root = checkout(src_root, repo, commit)
        files = [f for f in root.rglob('*.json') if f.name in ('.mcp.json', 'mcp.json', 'plugin.json')
                 and not {'.git', 'node_modules'} & set(f.relative_to(root).parts)]
        for f in sorted(files):
            _, servers = servers_of(read_json(f))
            pkg = f.parent.parent if f.parent.name.endswith('-plugin') else f.parent
            for name, server in servers.items():
                first = (server.get('args') or [''])[0]
                registry = server.get('command') in ('npx', 'bunx') or (server.get('command') in ('pnpm', 'yarn') and first == 'dlx') \
                    or (server.get('command') == 'bun' and first == 'x')
                if registry:
                    path = str(pkg.relative_to(root)) if pkg != root else ''
                    key = (repo, path, name)
                    same = [x for x in found if (x['repo'], x['path'], x['name']) == key]
                    form = 'mcpServers' if isinstance((read_json(f) or {}).get('mcpServers'), dict) else 'bare map'
                    defn = {'file': str(f.relative_to(pkg)), 'form': form, 'command': server['command'], 'args': server.get('args', [])}
                    if same:
                        same[0]['definitions'].append(defn)
                    else:
                        found.append({'repo': repo, 'commit': commit, 'path': path, 'name': name, 'definitions': [defn]})
    return found


def version_in(server, agent):
    """The command line an agent's configuration holds for a server."""
    if not isinstance(server, dict):
        return None
    cmd = server.get('command')
    if isinstance(cmd, list):
        return ' '.join(map(str, cmd))
    return ' '.join([str(cmd), *map(str, server.get('args', []))]) if cmd else server.get('url') or server.get('serverUrl')


def pin_case(tag, work, stubs, setup):
    base = work / 'pin' / tag
    shutil.rmtree(base, ignore_errors=True)
    ws = project(base)
    env = environment(base)
    steps = []
    step(steps, base, ws, env, 'init', '--name', 'pinning')
    setup(steps, base, ws, env)
    step(steps, base, ws, env, 'lock')
    sync = step(steps, base, ws, env, 'sync')
    lock = read_lock(ws)
    roots = staged_roots(base)
    case = {'steps': steps, 'lock_mcp_servers': lock.get('mcp_servers', [])}
    if not roots or sync != 0:
        discard(base, roots['project_hash'] if roots else None)
        return case
    case['project_hash'] = roots['project_hash']
    before = {a: {n: version_in(s, a) for n, s in servers.items()} for a, servers in staged_mcp(roots).items()}
    case['staged'] = before
    rc, so, se = sh([AP, '--no-progress', 'mcp', 'list'], cwd=ws, env=env)
    case['mcp_list'] = clean(so + se, base).strip()
    lock_bytes = (ws / 'pack.lock').read_bytes()
    off = environment(base, offline=True)
    offline_steps = []
    rc = step(offline_steps, base, ws, off, 'sync')
    same_lock = (ws / 'pack.lock').read_bytes() == lock_bytes
    roots2 = staged_roots(base)
    after = {a: {n: version_in(s, a) for n, s in servers.items()} for a, servers in staged_mcp(roots2).items()} if roots2 else None
    control = step(offline_steps, base, ws, off, 'lock', '--update')
    case['offline'] = {'steps': offline_steps, 'sync_exit': rc, 'staged_unchanged': after == before,
                       'lock_unchanged_after_sync': same_lock, 'control_lock_update_exit': control,
                       'lock_unchanged_after_control': (ws / 'pack.lock').read_bytes() == lock_bytes}
    if not args.skip_listing:
        seen = {}
        lenv = environment(base, stubs)
        for agent in AGENTS:
            if not BINS[agent]:
                continue
            rc, so, se = sh([AP, '-q', '--no-progress', LAUNCHER[agent], 'mcp', 'list'], cwd=ws, env=lenv, timeout=180)
            seen[agent] = {'exit': rc, 'output': clean(so + se, base).strip()[:3000]}
        case['agent_mcp_list'] = seen
    discard(base, case['project_hash'])
    return case


def run_pinning(found, work, stubs):
    out = []
    for s in found:
        tag = (s['repo'] + '_' + s['path'] + '_' + s['name']).replace('/', '_')
        spec = (f"{s['repo']}/{s['path']}" if s['path'] else s['repo']) + '@' + s['commit']
        row = dict(s, spec=spec)
        row['through_package'] = pin_case(tag + '-package', work, stubs,
                                          lambda steps, base, ws, env: step(steps, base, ws, env, 'add', spec, '--no-sync'))
        d = s['definitions'][0]

        def manifest(steps, base, ws, env, d=d, name=s['name']):
            with open(ws / 'agentpack.toml', 'a') as f:
                f.write(f'\n[mcp.servers.{json.dumps(name)}]\ncommand = {json.dumps(d["command"])}\nargs = {json.dumps(d["args"])}\n')
        row['manifest_definition'] = {'command': d['command'], 'args': d['args']}
        row['through_manifest'] = pin_case(tag + '-manifest', work, stubs, manifest)
        for route in ('through_package', 'through_manifest'):
            case = row[route]
            rec = [r for r in case.get('lock_mcp_servers', []) if r.get('name') == s['name']]
            case['record'] = rec[0] if rec else None
            version = rec[0].get('version') if rec else None
            case['pinned'] = bool(rec and rec[0].get('status') == 'pinned' and version)
            if case['pinned'] and 'staged' in case:
                want = f"{rec[0]['package']}@{version}"
                case['exact_version_in_config'] = {a: want in (v.get(s['name']) or '') for a, v in case['staged'].items()}
                if 'agent_mcp_list' in case:
                    case['exact_version_listed_by_agent'] = {a: want in v['output'] for a, v in case['agent_mcp_list'].items()}
        print(f"  pin {spec} {s['name']}: package={row['through_package']['pinned']} manifest={row['through_manifest']['pinned']}",
              file=sys.stderr, flush=True)
        out.append(row)
    return out


# ---------------------------------------------------------------- summary

def summarise(rows):
    s = {'packages': len(rows), 'resolved_and_locked': sum(r['lock']['resolved_and_locked'] for r in rows),
         'staged': sum(r['staged'] for r in rows),
         'not_locked': [{'id': r['id'], 'messages': [m['text'] for st in r['steps'] if st['exit'] != 0 for m in st['messages']]}
                        for r in rows if not r['lock']['resolved_and_locked']],
         'source_items': {k: sum(r['contains'][k] for r in rows) for k in KINDS},
         'source_items_in_staged_packages': {k: sum(r['contains'][k] for r in rows if r['staged']) for k in KINDS},
         'packages_with_kind': {k: sum(1 for r in rows if r['contains'][k]) for k in KINDS},
         'delivery': {}, 'listing': {}}
    for agent in AGENTS:
        s['delivery'][agent] = {}
        for kind in KINDS:
            total = dict(source=0, native=0, substitute=0, copied=0, replaced=0, dropped=0)
            for r in rows:
                if r['staged']:
                    for k, v in r['cells'][agent][kind].items():
                        total[k] += v
            s['delivery'][agent][kind] = total
        s['listing'][agent] = {}
        for kind in ('skill', 'command', 'agent', 'mcp'):
            src = exp = got = 0
            checked = False
            for r in rows:
                seen = r.get('listing', {}).get(agent, {}).get('seen')
                if seen and seen[kind]['listed'] is not None:
                    checked = True
                    src += seen[kind]['source']
                    exp += seen[kind]['expected']
                    got += seen[kind]['listed']
            s['listing'][agent][kind] = ({'source': src, 'hidden_by_own_metadata': src - exp, 'expected': exp, 'listed': got}
                                         if checked else 'no listing without a model')
    return s


def main():
    work = (args.work or Path(tempfile.mkdtemp(prefix='agentpack-plugins-'))).resolve()
    work.mkdir(parents=True, exist_ok=True)
    src_root = work / 'src'
    stubs = None
    if not args.allow_server_start:
        stubs = work / 'stubs'
        stubs.mkdir(exist_ok=True)
        for name in STUBS:
            (stubs / name).write_text(f'#!/bin/sh\necho "{name}: not started by plugins.py" >&2\nexit 127\n')
            (stubs / name).chmod(0o755)
    home = work / 'version-home'
    home.mkdir(exist_ok=True)
    venv = {'PATH': os.environ['PATH'], 'HOME': str(home), 'AGENTPACK_HOME': str(home / 'ap')}
    versions = {a: (sh([b, '--version'], env=venv, timeout=60)[1].strip().splitlines() or ['?'])[-1] if b else None for a, b in BINS.items()}
    corpus = select(src_root)
    args.corpus_output.write_text(json.dumps({
        'selected_on': '2026-10-13',
        'rule': {s['id']: {'repo': s['repo'], 'commit': s['commit'], 'rule': s['rule']} for s in SOURCES} | {
            'community': {'rule': COMMUNITY_RULE, 'repositories': COMMUNITY}},
        'packages': [{k: p.get(k) for k in ('id', 'source', 'format', 'repo', 'path', 'commit', 'url', 'universe', 'stars')} for p in corpus],
    }, indent=1) + '\n')
    if args.only:
        corpus = [p for p in corpus if p['id'] in args.only.split(',')]
    print(f'{len(corpus)} packages, work directory {work}', file=sys.stderr)
    with concurrent.futures.ThreadPoolExecutor(max_workers=args.jobs) as pool:
        rows = list(pool.map(lambda p: run_package(p, work, src_root, stubs), corpus))
    result = {
        'recorded': datetime.datetime.now(datetime.timezone.utc).isoformat(timespec='seconds'),
        'host': f'{platform.system()} {platform.release()} {platform.machine()}',
        'agentpack': {'version': sh([AP, '--version'], env=venv)[1].strip(), 'sha256': hashlib.sha256(AP.read_bytes()).hexdigest()},
        'agents': versions,
        'isolation': {'home': 'temporary', 'mcp_server_launchers_stubbed_during_listings': None if args.allow_server_start else STUBS},
        'classes': {'native': "found where the agent's own format expects it", 'substitute': 'found as the fallback named in harnesses/conversion.md',
                    'copied': 'the file is in the staged tree but not in a form the documentation says the agent reads',
                    'replaced': 'a staged file has the name but holds the content of another item with the same name',
                    'dropped': 'not found in the staged tree'},
        'summary': summarise(rows),
        'packages': rows,
    }
    if not args.skip_pinning and not args.only:
        found = npm_servers(src_root)
        result['pinning'] = run_pinning(found, work, stubs)
    args.output.write_text(json.dumps(result, indent=1, ensure_ascii=False) + '\n')
    print(json.dumps(result['summary'], indent=1))
    if os.environ.get('PLUGINS_TIMES'):
        print(sorted(TIMES, reverse=True)[:25], file=sys.stderr)
    if not args.work:
        shutil.rmtree(work, ignore_errors=True)


if __name__ == '__main__':
    main()
