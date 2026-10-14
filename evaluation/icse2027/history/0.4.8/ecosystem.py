#!/usr/bin/env -S uv run
# /// script
# requires-python = ">=3.11"
# dependencies = []
# ///
"""What each tool delivers to each coding agent from the same published packages.

The same pinned public inputs (Claude Code, Cursor and Codex plugins, a bare skill, two
marketplace repositories) are given to each tool with the command its own documentation gives.
Every run has its own empty HOME and its own throwaway git checkout. After the tool has run, the
files it wrote are recorded, and each of the six agents is asked what it sees, with HOME pointed
at the same empty home and no credentials, so no model is called. Where an agent has no listing
for a kind of content, the cell is decided from files at the locations in LOCATIONS below.

    uv run ecosystem.py setup --tools-dir DIR            # download the pinned tools (network)
    uv run ecosystem.py run --tools-dir DIR --agentpack PATH [--work DIR] [--tool T] [--input I]

Nothing outside --work and --tools-dir is written, except the results file.
MCP servers are never started on purpose: Claude Code is listed with --strict-mcp-config, and the
Cursor listing (which connects to servers) is skipped when an input declares a stdio server.
"""
import argparse, hashlib, json, os, platform, re, shutil, subprocess, sys, tempfile, time, tomllib
from pathlib import Path

HERE = Path(__file__).resolve().parent
AGENTS = ['claude', 'codex', 'opencode', 'cursor', 'grok', 'agy']
KINDS = ['skill', 'command', 'agent', 'rule', 'hook', 'mcp']
EXE = {'claude': 'claude', 'codex': 'codex', 'opencode': 'opencode', 'cursor': 'cursor-agent', 'grok': 'grok', 'agy': 'agy'}
AGENT_VERSIONS = {'claude': '2.1.296 (Claude Code)', 'codex': 'codex-cli 0.159.3', 'opencode': '1.17.11',
                  'cursor': '2026.08.25-3e8eec8', 'grok': 'grok 1.0.44 (5b807183dd79)', 'agy': '1.3.4'}

# ---------------------------------------------------------------- inputs (pinned)
STRIPE = ('stripe/ai', '97b2164821c378f246c3903852057b36a8bd0296')
OFFICIAL = ('anthropics/claude-plugins-official', 'f359d43b406057f1a8cdbc4ea93b827642c162e6')
INPUTS = {
    'claude-plugin-stripe': dict(
        what='Claude Code plugin with commands, a subagent, skills, hooks and a remote MCP server',
        repo=STRIPE[0], commit=STRIPE[1], path='providers/claude/plugin', format='claude-plugin', plugin='stripe',
        marketplace=dict(repo=STRIPE[0], name='stripe')),
    'claude-plugin-hookify': dict(
        what='Claude Code plugin with commands, a subagent, a skill and hooks (no MCP)',
        repo=OFFICIAL[0], commit=OFFICIAL[1], path='plugins/hookify', format='claude-plugin', plugin='hookify',
        marketplace=dict(repo=OFFICIAL[0], name='claude-plugins-official')),
    'claude-plugin-playwright-mcp': dict(
        what='Claude Code plugin that declares one MCP server started with npx @playwright/mcp@latest',
        repo=OFFICIAL[0], commit=OFFICIAL[1], path='external_plugins/playwright', format='claude-plugin',
        plugin='playwright', marketplace=dict(repo=OFFICIAL[0], name='claude-plugins-official')),
    'cursor-plugin-create-plugin': dict(
        what='Cursor plugin (.cursor-plugin/plugin.json) with skills, a rule (.mdc) and a subagent',
        repo='cursor/plugins', commit='1dcc2bacde73ddd284803b7af28164d0436198fc', path='create-plugin',
        format='cursor-plugin', plugin='create-plugin', marketplace=dict(repo='cursor/plugins', name='cursor-plugins')),
    'cursor-plugin-playwright-mcp': dict(
        what='Cursor plugin whose mcp.json declares one MCP server started with npx -y @playwright/mcp@latest',
        repo='cursor/plugins', commit='1dcc2bacde73ddd284803b7af28164d0436198fc', path='third_party/playwright',
        format='cursor-plugin', plugin='playwright', marketplace=dict(repo='cursor/plugins', name='cursor-plugins')),
    'codex-plugin-sentry': dict(
        what='Codex plugin (.codex-plugin/plugin.json) with a skill and a remote MCP server',
        repo='openai/plugins', commit='0722921d5542fc593105c27bd52630babd8b8c2a', path='plugins/sentry',
        format='codex-plugin', plugin='sentry', marketplace=dict(repo='openai/plugins', name='openai-curated')),
    'bare-skill-webapp-testing': dict(
        what='Bare SKILL.md skill',
        repo='anthropics/skills', commit='dbd4588f9e1033efb41dad4bef2f7947c8993d44', path='skills/webapp-testing',
        format='skill', plugin='example-skills', marketplace=dict(repo='anthropics/skills', name='anthropic-agent-skills')),
    'marketplace-single-stripe': dict(
        what='Marketplace repository that lists one plugin, with separate Claude, Cursor and Codex sources',
        repo=STRIPE[0], commit=STRIPE[1], path='', format='marketplace', plugin='stripe',
        marketplace=dict(repo=STRIPE[0], name='stripe'),
        content=dict(repo=STRIPE[0], commit=STRIPE[1], path='providers/claude/plugin')),
    'marketplace-catalog-stripe': dict(
        what='Catalog marketplace (315 plugins); the plugin "stripe" is listed there as a git-subdir source',
        repo=OFFICIAL[0], commit=OFFICIAL[1], path='', format='marketplace', plugin='stripe',
        marketplace=dict(repo=OFFICIAL[0], name='claude-plugins-official'),
        content=dict(repo=STRIPE[0], commit=STRIPE[1], path='providers/claude/plugin')),
}

# ---------------------------------------------------------------- tools (pinned)
REL = 'https://github.com/{repo}/releases/download/{tag}/{asset}'
TOOLS = {
    'agentpack': dict(name='Agentpack (OlegHQ/agentpack)', version='0.4.8', repo='OlegHQ/agentpack', tag='v0.4.8',
                      commit='bce036b (tag v0.4.8)', binary_sha256='d71209115d8188cdd4301c2da1bc2a3a228e71803097e4f65bdc077f151863b7'),
    'vercel-skills': dict(name='Vercel skills', version='1.7.2', repo='vercel-labs/skills', npm='skills',
                          commit='13e4063a1cf913f5606d57d42ab83a86f5001e04',
                          integrity='sha512-j+GzyF/oNahsjjjup6y3lJpFkC1lQjIkf4kAMXICA1hboBqi0fAGXAGoE4x9FRZiP1br/YE9+L3/Tqq6PIAe0A=='),
    'ruler': dict(name='Ruler', version='0.3.44', repo='intellectronica/ruler', npm='@intellectronica/ruler',
                  commit='0fa2caeef4efaef4c5f8ffbedee8feca4b0b00e2',
                  integrity='sha512-OjJCdZleUQSLapy2HuLdDNpbyneArwYT9i9QP7mskrREPqBEB3JoDiZ8xded3Q7oA7/s5Pgn8Qo6UiGEsYZIVQ=='),
    'apm': dict(name='Microsoft APM', version='0.33.0', repo='microsoft/apm', tag='v0.33.0', commit='18c4c43 (reported by --version)',
                asset='apm-linux-arm64.tar.gz', sha256='ac40dd0efd1af35a56847beedde96854530d2f54851e026728da017d3af827f8',
                exe='apm/bin/apm-linux-arm64/apm'),
    'skillpm': dict(name='skillpm', version='4.0.0', repo='eddieran/skillpm', tag='v4.0.0',
                    commit='26fb1c66b274b8729f5bcca0c2e31d2013cf3141 (default branch head read; binary is tag v4.0.0)',
                    asset='skillpm_Linux_arm64.tar.gz', sha256='dbdec574767733e04a8d1652b48cc624c46d37f5afb420aa610a7ad8ca8ff7fd',
                    exe='skillpm/bin/skillpm'),
    'skills-lock': dict(name='skills-lock', version='0.1.0', repo='luisalima/skills-lock', npm='skills-lock',
                        commit='4e37719a163b6022d4a2828850567421f9de7825',
                        integrity='sha512-PDLMWPRDUizVaToyx6zI1Hzp3ikk0kFHEocGh2QcMm/fKUI+VFw9PCWpY0nNiXDeCaEApFao/wYQyFzlffyStg=='),
    'liqiongyu-agentpack': dict(name='liqiongyu/agentpack', version='0.9.1', repo='liqiongyu/agentpack', tag='v0.9.1',
                                commit='52bc48f7 (tag v0.9.1)', asset='agentpack-aarch64-unknown-linux-gnu.tar.xz',
                                sha256='d8dbc08fe0360d496fd86385d9760e654e8d945b3691d134ad4ced36eef63893',
                                exe='liqiongyu-agentpack/bin/agentpack-aarch64-unknown-linux-gnu/agentpack'),
    'retr0h-agentpack': dict(name='retr0h/agentpack', version='1.1.0', repo='retr0h/agentpack', tag='v1.1.0',
                             commit='86a433ce (tag v1.1.0)', asset='agentpack_1.1.0_linux_arm64', raw_binary='agentpack',
                             sha256='2c3d493af44b6059e36203aa367e7a6ff434e576ba616a77301b1d54646482dc',
                             exe='retr0h-agentpack/bin/agentpack'),
    'rulesync': dict(name='rulesync', version='33.0.0', repo='dyoshikawa/rulesync', npm='rulesync',
                     commit='fac7f7439da167ee4a6fe80b5f5a002d15b2b801 (default branch head read; package is 33.0.0)',
                     integrity='sha512-W/b35EK+WcQ9wwbuCObSkTLr9cjGOQdtSpkgmuJ7vmsL6k2MfxJAahZ0E8QXUS5o12DE3UKT3vDIiNpaA+ONxQ=='),
    'dotagents': dict(name='dotagents (Sentry)', version='3.3.0', repo='getsentry/dotagents', npm='@sentry/dotagents',
                      commit='1b6da09cc13f890611811e2a589fe03f23e4faa8',
                      integrity='sha512-uZOD+Lixmq4UTRpfFbsAAzHHRhswPuBZQanmpmPLM+1OwlCV4Y2GiX/hACfOwzXbOQK9wfhuAR1QETTKjZkvoQ=='),
    'openskills': dict(name='openskills', version='1.5.0', repo='numman-ali/openskills', npm='openskills',
                       commit='57d933a4f0d5c8659bd8b285f50fb9554360f0b3',
                       integrity='sha512-Wq5af8i3akre2ZhZigDkz+CasJCdT68NT4ou6EOeqf0Jm/F5EjwmQur9ATnto4aBfKaajM9rRcYO4hfHRQQImw=='),
    'skillshare': dict(name='skillshare', version='0.25.10', repo='runkids/skillshare', tag='v0.25.10',
                       commit='9dbf7ee6806a07391499a35c9d1c414b4f6e95cd (default branch head read; binary is tag v0.25.10)',
                       asset='skillshare_0.25.10_linux_arm64.tar.gz',
                       sha256='323544db97a37a59969f29c9c1a87fa0278939ae7d40c92e4e5f28c55ca10060', exe='skillshare/bin/skillshare'),
    'native': dict(name="Each agent's own install command (no tool)", version='agent versions above'),
}
NODE_BIN = {'vercel-skills': 'skills', 'ruler': 'ruler', 'skills-lock': 'skills-lock', 'rulesync': 'rulesync',
            'dotagents': 'dotagents', 'openskills': 'openskills'}

# Where each agent is documented to read content that it does not list itself. Used only for cells
# whose 'source' is 'files'. {project}=checkout, {home}=HOME of the run.
LOCATIONS = {
    'claude': {'rule': ['{project}/CLAUDE.md', '{project}/.claude/CLAUDE.md', '{project}/.claude/rules/**/*.md', '{home}/.claude/CLAUDE.md', '{home}/.claude/rules/**/*.md'],
               'hook': ['{project}/.claude/settings.json', '{project}/.claude/settings.local.json', '{home}/.claude/settings.json', '<plugin>/hooks/hooks.json'],
               'mcp': ['{project}/.mcp.json', '{home}/.claude.json', '<plugin>/.mcp.json']},
    'codex': {'command': ['{codex_home}/prompts/*.md'], 'agent': ['{project}/.codex/agents/*.toml', '{codex_home}/agents/*.toml'],
              'rule': ['{project}/AGENTS.md', '{codex_home}/AGENTS.md'],
              'hook': ['{project}/.codex/hooks.json', '{codex_home}/hooks.json', '<plugin>/hooks.json', '<plugin>/hooks/hooks.json']},
    'opencode': {'rule': ['{project}/AGENTS.md', '{project}/CLAUDE.md', '{opencode_config}/AGENTS.md'],
                 'hook': ['{project}/.opencode/plugins/**/*', '{project}/.opencode/plugin/**/*', '{opencode_config}/plugins/**/*', '{opencode_config}/plugin/**/*']},
    'cursor': {'skill': ['{project}/.cursor/skills/*/SKILL.md', '{project}/.agents/skills/*/SKILL.md', '{project}/.claude/skills/*/SKILL.md', '{project}/.codex/skills/*/SKILL.md',
                         '{cursor_user}/skills/*/SKILL.md', '{home}/.agents/skills/*/SKILL.md', '{home}/.claude/skills/*/SKILL.md', '{home}/.codex/skills/*/SKILL.md'],
               'command': ['{project}/.cursor/commands/*.md', '{cursor_user}/commands/*.md'],
               'agent': ['{project}/.cursor/agents/*.md', '{project}/.claude/agents/*.md', '{project}/.codex/agents/*.toml', '{cursor_user}/agents/*.md', '{home}/.claude/agents/*.md', '{home}/.codex/agents/*.toml'],
               'rule': ['{project}/.cursor/rules/**/*.mdc', '{project}/.cursor/rules/**/*.md', '{project}/AGENTS.md', '{project}/CLAUDE.md', '{project}/.cursorrules',
                        # not in Cursor's docs (user rules are set in the UI); Agentpack stages rules here in the HOME it launches Cursor with
                        '{cursor_user}/rules/**/*.mdc'],
               'hook': ['{project}/.cursor/hooks.json', '{cursor_user}/hooks.json'],
               'mcp': ['{project}/.cursor/mcp.json', '{cursor_user}/mcp.json']},
    'grok': {'command': ['{project}/.grok/commands/*.md', '{home}/.grok/commands/*.md', '<plugin>/commands/*.md']},
    'agy': {'skill': ['{project}/.agents/skills/*/SKILL.md', '{home}/.gemini/antigravity-cli/skills/*/SKILL.md', '<plugin>/skills/*/SKILL.md'],
            # Antigravity has no separate command kind ("the CLI automatically converts [a skill] into a slash command");
            # commands/ exists only inside plugins.
            'command': ['<plugin>/commands/*.md'],
            'agent': ['{project}/.agents/agents/*.md', '{home}/.gemini/config/agents/*.md', '<plugin>/agents/*.md'],
            'rule': ['{project}/.agents/rules/**/*.md', '{project}/AGENTS.md', '{project}/GEMINI.md', '{home}/.gemini/AGENTS.md', '{home}/.gemini/GEMINI.md',
                     '{home}/.gemini/config/rules/*.md', '{home}/.gemini/antigravity-cli/rules/*.md', '<plugin>/rules/*.md', '<plugin>/rules/*.mdc'],
            'hook': ['{project}/.agents/hooks.json', '{home}/.gemini/config/hooks.json', '<plugin>/hooks/hooks.json', '<plugin>/hooks.json'],
            'mcp': ['{project}/.agents/mcp_config.json', '{home}/.gemini/config/mcp_config.json', '<plugin>/mcp_config.json']},
}
UPSTREAM_LOCKS = {'uv.lock', 'gemfile.lock', 'pnpm-lock.yaml', 'yarn.lock', 'cargo.lock', 'poetry.lock', 'bun.lock', 'flake.lock', 'composer.lock'}
LOCKNAME = re.compile(r'(^|[.-])lock(file)?\.(json|ya?ml|toml)$|\.lock$|^skills\.lock$')
ANSI = re.compile(r'\x1b\[[0-9;?]*[a-zA-Z]|\r')
HEX40 = re.compile(r'\b[0-9a-f]{40}\b')
HASH = re.compile(r'sha(256|512)[-:=][A-Za-z0-9+/=_-]{20,}|\b[0-9a-f]{64}\b|"(integrity|computedHash|content_hash|sha256)"')


def norm(name):
    return re.sub(r'[^a-z0-9]+', '-', str(name).split(':')[-1].lower()).strip('-')


def sha(path):
    return hashlib.sha256(Path(path).read_bytes()).hexdigest()


def clean_env(home, **extra):
    env = {'PATH': os.environ['PATH'], 'HOME': str(home), 'USERPROFILE': str(home), 'XDG_CONFIG_HOME': str(home / '.config'),
           'XDG_DATA_HOME': str(home / '.local/share'), 'XDG_CACHE_HOME': str(home / '.cache'),
           'XDG_STATE_HOME': str(home / '.local/state'), 'GIT_TERMINAL_PROMPT': '0', 'CI': '1', 'NO_COLOR': '1',
           'TERM': 'dumb', 'DISABLE_TELEMETRY': '1', 'DO_NOT_TRACK': '1', 'LANG': 'C.UTF-8',
           # agents must not replace their own binaries during a measurement
           'DISABLE_AUTOUPDATER': '1', 'GROK_DISABLE_AUTOUPDATER': '1', 'AGY_CLI_DISABLE_AUTO_UPDATE': '1',
           'OPENCODE_DISABLE_AUTOUPDATE': '1'}
    env.update({k: str(v) for k, v in extra.items()})
    return env


def run(argv, cwd, env, timeout=600, stdin=None):
    t = time.time()
    # stdout goes to a file: OpenCode cuts output longer than a pipe buffer when it writes to a pipe
    with tempfile.TemporaryFile('w+', errors='replace') as fo, tempfile.TemporaryFile('w+', errors='replace') as fe:
        try:
            p = subprocess.run([str(a) for a in argv], cwd=cwd, env=env, stdout=fo, stderr=fe, timeout=timeout,
                               stdin=subprocess.DEVNULL)
            code = p.returncode
        except subprocess.TimeoutExpired:
            code = 'timeout'
        except FileNotFoundError as e:
            code = 'not-found'
        fo.seek(0), fe.seek(0)
        out, err = fo.read(), fe.read()
    return {'argv': [str(a) for a in argv], 'exit': code, 'seconds': round(time.time() - t, 1),
            'stdout': ANSI.sub('', out), 'stderr': ANSI.sub('', err)}


def tail(text, n=900):
    text = re.sub(r'[◒◐◓◑][^\n◒◐◓◑]*', '', text)
    text = '\n'.join(l.rstrip() for l in text.splitlines() if l.strip())
    return text[-n:]


# ---------------------------------------------------------------- setup
def setup(tools):
    tools.mkdir(parents=True, exist_ok=True)
    for key, t in TOOLS.items():
        if 'asset' not in t:
            continue
        d = tools / key
        (d / 'dl').mkdir(parents=True, exist_ok=True)
        (d / 'bin').mkdir(exist_ok=True)
        f = d / 'dl' / t['asset']
        if not f.exists():
            subprocess.run(['curl', '-fsSL', '-o', str(f), REL.format(repo=t['repo'], tag=t['tag'], asset=t['asset'])], check=True)
        if sha(f) != t['sha256']:
            sys.exit(f"{key}: checksum mismatch for {f}")
        if t.get('raw_binary'):
            shutil.copy(f, d / 'bin' / t['raw_binary'])
            (d / 'bin' / t['raw_binary']).chmod(0o755)
        else:
            subprocess.run(['tar', '-xf', str(f), '-C', str(d / 'bin')], check=True)
        print(f"{key}: {t['asset']} sha256 ok")
    node = tools / 'node'
    node.mkdir(exist_ok=True)
    deps = {t['npm']: t['version'] for t in TOOLS.values() if 'npm' in t}
    (node / 'package.json').write_text(json.dumps({'name': 'ecosystem-tools', 'private': True, 'dependencies': deps}, indent=1))
    lock = HERE / 'ecosystem-package-lock.json'
    if lock.exists():
        shutil.copy(lock, node / 'package-lock.json')
    subprocess.run(['npm', 'ci' if lock.exists() else 'install', '--ignore-scripts', '--no-audit', '--no-fund'], cwd=node, check=True)
    got = json.loads((node / 'package-lock.json').read_text())['packages']
    for t in TOOLS.values():
        if 'npm' in t and got[f"node_modules/{t['npm']}"].get('integrity') != t['integrity']:
            sys.exit(f"{t['npm']}: npm integrity differs from the recorded one")
    print('node tools: integrity ok; lock at', node / 'package-lock.json')


def tool_exe(tools, key):
    if key in NODE_BIN:
        return tools / 'node/node_modules/.bin' / NODE_BIN[key]
    return tools / TOOLS[key]['exe']


# ---------------------------------------------------------------- inputs
def checkout(src, repo, commit):
    d = src / f"{repo.replace('/', '_')}@{commit[:12]}"
    if not (d / '.git').exists():
        d.mkdir(parents=True)
        for cmd in (['init', '-q'], ['remote', 'add', 'origin', f'https://github.com/{repo}.git'],
                    ['fetch', '-q', '--depth', '1', 'origin', commit], ['checkout', '-q', 'FETCH_HEAD']):
            subprocess.run(['git', '-C', str(d), *cmd], check=True)
    assert subprocess.check_output(['git', '-C', str(d), 'rev-parse', 'HEAD'], text=True).strip() == commit
    return d


def frontmatter(text):
    m = re.match(r'---\s*\n(.*?)\n---\s*\n?(.*)', text, re.S)
    if not m:
        return {}, text
    meta = {}
    for line in m.group(1).splitlines():
        k, _, v = line.partition(':')
        if _ and not line.startswith((' ', '\t')):
            meta[k.strip()] = v.strip().strip('"\'')
    return meta, m.group(2)


def marker(body):
    """A distinctive line of an artifact body, to recognise it after its wrapper was rewritten."""
    lines = [l.strip() for l in body.splitlines() if len(l.strip()) > 30 and not l.strip().startswith(('---', '```', '|'))]
    return max(lines, key=len)[:120] if lines else ''


def mcp_servers(data):
    servers = data.get('mcpServers', data) if isinstance(data, dict) else {}
    return {k: v for k, v in servers.items() if isinstance(v, dict)}


def expected(src, inp):
    c = inp.get('content', inp)
    root = checkout(src, c['repo'], c['commit']) / c['path']
    exp = {k: {} for k in KINDS}
    if (root / 'SKILL.md').exists():
        meta, body = frontmatter((root / 'SKILL.md').read_text())
        exp['skill'][norm(meta.get('name', root.name))] = {'aliases': sorted({norm(root.name), norm(meta.get('name', root.name))}), 'marker': marker(body)}
    for f in sorted(root.glob('skills/*/SKILL.md')):
        meta, body = frontmatter(f.read_text())
        exp['skill'][norm(f.parent.name)] = {'aliases': sorted({norm(f.parent.name), norm(meta.get('name', f.parent.name))}), 'marker': marker(body)}
    for kind, sub in (('command', 'commands'), ('agent', 'agents'), ('rule', 'rules')):
        for f in sorted(list(root.glob(f'{sub}/*.md')) + list(root.glob(f'{sub}/*.mdc'))):
            meta, body = frontmatter(f.read_text())
            exp[kind][norm(f.stem)] = {'aliases': sorted({norm(f.stem), norm(meta.get('name', f.stem))}), 'marker': marker(body)}
    for f in (root / 'hooks/hooks.json', root / 'hooks.json'):
        if f.exists():
            hooks = json.loads(f.read_text()).get('hooks', {})
            scripts = sorted(set(re.findall(r'[\w.-]+\.(?:py|mjs|js|ts|sh)', f.read_text())))
            exp['hook']['hooks'] = {'aliases': ['hooks'], 'events': sorted(hooks), 'scripts': scripts}
    for f in (root / '.mcp.json', root / 'mcp.json'):
        if f.exists():
            for name, spec in mcp_servers(json.loads(f.read_text())).items():
                exp['mcp'][norm(name)] = {'aliases': [norm(name)], 'spec': spec, 'stdio': 'command' in spec}
            break
    return exp


# ---------------------------------------------------------------- file snapshot
SKIP_DIRS = {'.git', 'node_modules', 'apm_modules', '.cache', '.npm'}


def snapshot(root):
    out = {}
    for d, dirs, files in os.walk(root, followlinks=False):
        dirs[:] = [x for x in dirs if x not in SKIP_DIRS or os.path.islink(os.path.join(d, x))]
        for name in list(dirs) + files:
            p = Path(d) / name
            if p.is_symlink():
                out[str(p.relative_to(root))] = 'link:' + os.readlink(p)
                if name in dirs:
                    dirs.remove(name)
            elif p.is_file():
                try:
                    out[str(p.relative_to(root))] = 'file:' + (sha(p)[:16] if p.stat().st_size < 5_000_000 else 'large')
                except OSError:
                    pass
    return out


AGENT_HOME_DIRS = ('.claude', '.claude.json', '.codex', '.cursor', '.config/opencode', '.grok', '.gemini', '.agents', '.config/agents', '.opencode')


def where_written(files):
    """files: paths relative to the run root (home/..., project/..., other/...)."""
    proj = sorted(f[8:] for f in files if f.startswith('project/'))
    home = sorted(f[5:] for f in files if f.startswith('home/'))
    other = sorted(f for f in files if not f.startswith(('project/', 'home/')))
    agent_cfg = [f for f in home if f.startswith(AGENT_HOME_DIRS)]
    top = lambda paths, depth=1: sorted({'/'.join(p.split('/')[:depth]) for p in paths})
    return {'project_files': len(proj), 'project_top_level': top(proj),
            'home_agent_config_files': len(agent_cfg), 'home_agent_config_dirs': top(agent_cfg, 2),
            'home_other_files': len(home) - len(agent_cfg), 'home_other_dirs': top([f for f in home if f not in agent_cfg]),
            'outside_project_and_home_files': len(other), 'outside_dirs': top(other, 2)}


# ---------------------------------------------------------------- adapters: how each tool is told to install an input
def tree_url(inp, ref=None):
    return f"https://github.com/{inp['repo']}/tree/{ref or inp['commit']}" + (f"/{inp['path']}" if inp['path'] else '')


BRANCH = {}


def default_branch(repo):
    """For tools that take a branch or tag but not a commit."""
    if repo not in BRANCH:
        out = subprocess.check_output(['git', 'ls-remote', '--symref', f'https://github.com/{repo}.git', 'HEAD'], text=True)
        BRANCH[repo] = re.search(r'ref: refs/heads/(\S+)', out).group(1)
    return BRANCH[repo]


def plan(tool, inp, exp, ctx):
    """Return {'steps': [argv...], 'env': {...}, 'repackaged': str|None, 'not_accepted': str|None, 'doc': str}."""
    x, P = ctx['exe'], ctx['project']
    repo, commit, path, fmt = inp['repo'], inp['commit'], inp['path'], inp['format']
    skills = sorted(exp['skill'])
    r = {'steps': [], 'env': {}, 'repackaged': None, 'not_accepted': None, 'doc': ''}
    S = r['steps'].append
    if tool == 'agentpack':
        r['doc'] = 'docs/src/getting-started/quick-start.md, concepts/mcp.md: init, add SPEC@ref, lock, sync'
        r['env'] = {'AGENTPACK_HOME': ctx['root'] / 'other/agentpack-home', 'AGENTPACK_STAGING_ROOT': ctx['root'] / 'other/staging'}
        S([x, '--no-progress', 'init'])
        S([x, '--no-progress', 'add', f"github.com/{repo}" + (f"/{path}" if path else '') + f"@{commit}"])
        S([x, '--no-progress', 'lock'])
        S([x, '--no-progress', 'sync'])
    elif tool == 'vercel-skills':
        r['doc'] = 'README: npx skills add <source> --skill ... -a <agents> -y'
        S([x, 'add', tree_url(inp), '--skill', *(skills if fmt == 'marketplace' else ['*']),
           '-a', 'claude-code', 'codex', 'opencode', 'cursor', 'grok', 'antigravity', 'antigravity-cli', '-y'])
    elif tool == 'apm':
        r['doc'] = 'docs consumer/install-packages.md, installing-from-marketplaces.md'
        targets = 'claude,codex,cursor,opencode,grok-build,antigravity'
        if fmt == 'marketplace':
            S([x, 'marketplace', 'add', repo, '--ref', commit])
            S([x, 'install', f"{inp['plugin']}@{inp['marketplace']['name']}", '-t', targets])
        else:
            S([x, 'install', f"{repo}/{path}#{commit}", '-t', targets])
    elif tool == 'skillpm':
        r['doc'] = 'README Usage: skillpm install <url> --force; skillpm inject --agent <agent>'
        r['note'] = ('skillpm passes the ref of a tree URL to git clone --branch, which rejects a commit, so the default branch is used: '
                     'this row is not pinned to the commit the other rows use.')
        S([x, 'install', tree_url(inp, default_branch(repo)), '--force'])
        for a in ('claude', 'codex', 'opencode', 'cursor', 'antigravity', 'grok'):
            S(['?', x, 'inject', '--agent', a])
    elif tool == 'skills-lock':
        r['doc'] = 'README: package.json skillsConfig.agents; skills-lock add <source>#<ref> --path <dir> --skill <name>'
        (P / 'package.json').write_text(json.dumps({'name': 'checkout', 'private': True, 'skillsConfig': {
            'agents': ['claude-code', 'cursor', 'opencode', 'universal']}}, indent=1))
        argv = [x, 'add', f"{repo}#{commit}"]
        if fmt == 'skill':
            argv += ['--path', path]
        else:
            for s in skills:
                argv += ['--skill', s]
        S(argv)
    elif tool == 'liqiongyu-agentpack':
        r['doc'] = 'docs/tutorials/quickstart.md: init; add <type> git:<url>#ref=..&subdir=..; update; deploy --apply --yes'
        r['env'] = {'AGENTPACK_HOME': ctx['root'] / 'other/liq-home'}
        c = inp.get('content', inp)
        if fmt != 'skill':
            r['repackaged'] = 'no plugin or marketplace source: each skill and command was declared by hand as its own module'
        S([x, 'init'])
        git = f"git:https://github.com/{c['repo']}.git#ref={c['commit']}&subdir="
        if fmt == 'skill':
            S([x, 'add', 'skill', git + path, '--id', f"skill:{skills[0]}", '--tags', 'base'])
        else:
            for s in skills:
                S([x, 'add', 'skill', git + f"{c['path']}/skills/{s}", '--id', f"skill:{s}", '--tags', 'base'])
            for cmd in sorted(exp['command']):
                S(['?', x, 'add', 'command', git + f"{c['path']}/commands/{cmd}.md", '--id', f"command:{cmd}", '--tags', 'base'])
        S([x, 'update', '--yes'])
        S([x, 'deploy', '--apply', '--yes'])
    elif tool == 'retr0h-agentpack':
        r['doc'] = 'docs/usage.md: agentpack add github.com/org/repo#<sha> --skill <name> --target <agent> --trust'
        argv = [x, 'add', f"github.com/{repo}#{commit}"]
        for s in skills:
            argv += ['--skill', s]
        for t in ('claude-code', 'codex', 'opencode', 'cursor', 'antigravity'):
            argv += ['--target', t]
        S(argv + ['--trust'])
    elif tool == 'rulesync':
        r['doc'] = ('docs/guide/declarative-sources.md (git transport, rulesync install) for a skill; README and '
                    'docs/guide/plugin-packaging.md: rulesync import --targets <format> --output-root <dir>; rulesync generate')
        c = inp.get('content', inp)
        # rulesync init also writes samples (a rule, a subagent "planner", a skill, and an MCP server named "playwright"
        # started with pnpm dlx); mcp_same() below keeps that sample from being counted as an input's server
        S([x, 'init'])
        if fmt == 'skill':
            r['note'] = 'The git transport rejects a commit as ref ("Ref ... not found"), so the default branch is given; rulesync locks the commit it resolves.'
            r['prepare'] = ('rulesync-source', {'source': f"https://github.com/{repo}", 'transport': 'git', 'ref': default_branch(repo),
                                                'path': str(Path(path).parent), 'skills': [Path(path).name]})
            S([x, 'install'])
        else:
            r['note'] = ('rulesync fetch (the remote route) asks the GitHub contents API once per file; run anonymously on the '
                         'Stripe plugin it stopped with "GitHub API rate limit exceeded" (2026-10-13). The plugin was '
                         'therefore imported from the pinned local checkout made by this script, which rulesync documents.')
            r['local_clone'] = True
            target = {'claude-plugin': 'claudecode-plugin', 'marketplace': 'claudecode-plugin', 'cursor-plugin': 'cursor',
                      'codex-plugin': 'codexcli'}[fmt]
            S([x, 'import', '--targets', target, '--features', '*', '--output-root',
               checkout(ctx['src'], c['repo'], c['commit']) / c['path']])
        S([x, 'generate', '--targets', 'claudecode,codexcli,opencode,cursor,grokcli,antigravity-cli', '--features', '*'])
    elif tool == 'dotagents':
        r['doc'] = 'README: dotagents --project init --agents ...; dotagents --project add <source> <name> --ref <ref>'
        S([x, '--project', 'init', '--agents', 'claude,cursor,codex,grok,opencode'])
        # a source that contains plugins is treated as plugin-only, so the skill is taken through the plugin that bundles it
        names = [inp['plugin']]
        S([x, '--project', 'add', repo, *names, '--ref', commit])
    elif tool == 'openskills':
        r['doc'] = 'README: openskills install <owner/repo[/path]> -y; openskills sync -y'
        S([x, 'install', repo + (f"/{path}" if path else ''), '-y'])
        S([x, 'sync', '-y'])
    elif tool == 'skillshare':
        r['doc'] = 'skillshare --help: init -p; plugin add <https url> --plugin <name> --source-ref <ref> --target ...; install <source> -b <ref>; sync'
        S([x, 'init', '-p', '--targets', 'claude,codex,cursor,opencode,grok,antigravity'])
        if fmt == 'skill':
            S([x, 'install', f"{repo}/{path}", '-b', commit, '-p', '--yes', '--skip-audit'])
        else:
            argv = [x, 'plugin', 'add', f"https://github.com/{repo}", '--plugin', inp['plugin'], '--source-ref', commit, '-p', '--json']
            for t in ('claude', 'codex', 'cursor', 'opencode', 'grok', 'antigravity'):
                argv += ['--target', t]
            S(argv)
            S(['?', x, 'plugin', 'sync', '-p', '--json'])
        S(['?', x, 'sync', '-p'])
    elif tool == 'ruler':
        r['doc'] = 'README: rules in .ruler/*.md, skills in .ruler/skills/, subagents in .ruler/agents/, MCP in .ruler/ruler.toml; ruler apply'
        r['not_accepted'] = ('Ruler has no remote source: "Skills are stored in `.ruler/skills/`" and rules in `.ruler/` '
                             '(README at the pinned commit); the user copies content in by hand.')
        r['repackaged'] = 'skills, subagents and rules copied by hand into .ruler/, MCP servers typed into .ruler/ruler.toml'
        c = inp.get('content', inp)
        src = checkout(ctx['src'], c['repo'], c['commit']) / c['path']
        S([x, 'init'])
        r['prepare'] = ('ruler', src)
        S([x, 'apply', '--agents', 'claude,codex,opencode,cursor,antigravity', '--subagents'])
    elif tool == 'native':
        r['doc'] = "each agent's own plugin commands (claude plugin, codex plugin, grok plugin, agy plugin, cursor-agent plugin)"
        m = inp['marketplace']
        S(['?', 'claude', 'plugin', 'marketplace', 'add', f"{m['repo']}#{commit}"])     # a commit is not accepted as a ref
        S(['?', 'claude', 'plugin', 'marketplace', 'add', m['repo']])                    # so: the default branch as it is today
        S(['?', 'claude', 'plugin', 'install', f"{inp['plugin']}@{m['name']}", '--scope', 'project'])
        S(['?', 'codex', 'plugin', 'marketplace', 'add', m['repo'], '--ref', commit])
        S(['?', 'codex', 'plugin', 'add', f"{inp['plugin']}@{m['name']}"])
        S(['?', 'grok', 'plugin', 'install', f"{repo}@{commit}" + (f"#{path}" if path else ''), '--trust'])
        S(['?', 'cursor-agent', 'plugin', 'marketplace', 'add', f"https://github.com/{m['repo']}"])
        c = inp.get('content', inp)
        S(['?', 'agy', 'plugin', 'install', checkout(ctx['src'], c['repo'], c['commit']) / c['path']])
        r['local_clone'] = True
        r['note'] = ('OpenCode has no install command for these formats (opencode plugin takes an npm module). '
                     'agy plugin install takes a local directory, so the pinned checkout made by this script was passed. '
                     'A bare skill is installed natively by copying it; the Claude Code step installs the marketplace '
                     'plugin that bundles it.')
    return r


def ruler_prepare(P, src, exp):
    """The manual step Ruler documents: copy content into .ruler/."""
    for f in src.glob('skills/*/SKILL.md'):
        shutil.copytree(f.parent, P / '.ruler/skills' / f.parent.name, dirs_exist_ok=True)
    if (src / 'SKILL.md').exists():
        shutil.copytree(src, P / '.ruler/skills' / src.name, dirs_exist_ok=True)
    for f in src.glob('agents/*.md'):
        (P / '.ruler/agents').mkdir(parents=True, exist_ok=True)
        shutil.copy(f, P / '.ruler/agents' / f.name)
    for f in list(src.glob('rules/*.md')) + list(src.glob('rules/*.mdc')):
        (P / '.ruler' / (f.stem + '.md')).write_text(frontmatter(f.read_text())[1])
    toml = P / '.ruler/ruler.toml'
    text = toml.read_text() if toml.exists() else ''
    for name, e in exp['mcp'].items():
        spec = e['spec']
        text += f"\n[mcp_servers.{name}]\n"
        if 'command' in spec:
            text += f"command = {json.dumps(spec['command'])}\nargs = {json.dumps(spec.get('args', []))}\n"
        else:
            text += f"url = {json.dumps(spec['url'])}\n"
    toml.write_text(text)


# ---------------------------------------------------------------- what an agent lists (no model call)
def first_json(text, opener):
    """First JSON value in text that starts with opener (tools print warnings before their JSON)."""
    dec = json.JSONDecoder()
    for m in re.finditer(re.escape(opener), text):
        try:
            return dec.raw_decode(text[m.start():])[0]
        except ValueError:
            continue
    return None


def listing(agent, env, cwd, wrap, exp, tmp):
    """Names the agent itself lists, per kind. wrap(agent) is the argv prefix (the bare binary, or a launcher)."""
    seen = {k: {} for k in KINDS}
    raw = {}
    stdio_mcp = any(e.get('stdio') for e in exp['mcp'].values())
    try:
        if agent == 'claude':
            p = run([*wrap('claude'), '-p', 'x', '--output-format', 'stream-json', '--verbose', '--strict-mcp-config',
                     '--settings', '{"disableAllHooks":true}'], cwd, env, 120)
            init = next(json.loads(l) for l in p['stdout'].splitlines() if l.startswith('{') and '"init"' in l)
            assert init.get('apiKeySource') == 'none', 'credentials were visible'
            skills = set(init.get('skills', []))
            for s in skills:
                seen['skill'][s] = 'init.skills'
            for s in init.get('slash_commands', []):
                if s not in skills:
                    seen['command'][s] = 'init.slash_commands'
            for s in init.get('agents', []):
                seen['agent'][s] = 'init.agents'
            raw = {'plugins': [f"{x.get('name')} ({x.get('source')})" for x in init.get('plugins', [])], 'covers': ['skill', 'command', 'agent']}
            if not wrap('claude')[0].endswith('agentpack'):     # MCP servers of installed, enabled plugins, as Claude Code reports them
                q = run(['claude', 'plugin', 'list', '--json'], cwd, env, 60)
                raw['plugin_mcp'] = {name: f"claude plugin list --json: {x.get('id')}: {json.dumps(spec)[:300]}"
                                     for x in first_json(q['stdout'], '[') or [] if x.get('enabled')
                                     for name, spec in (x.get('mcpServers') or {}).items()}
        elif agent == 'codex':
            env = dict(env)
            if not wrap('codex')[0].endswith('agentpack'):
                home = Path(tmp) / 'codex-home'
                real = Path(env['HOME']) / '.codex'
                shutil.rmtree(home, ignore_errors=True)
                shutil.copytree(real, home, symlinks=True) if real.exists() else home.mkdir(parents=True)
                with open(home / 'config.toml', 'a') as f:      # a user who trusted the checkout
                    f.write(f'\n[projects.{json.dumps(str(cwd))}]\ntrust_level = "trusted"\n')
                env['CODEX_HOME'] = str(home)
            p = run([*wrap('codex'), '-c', 'cli_auth_credentials_store="file"', '-c', 'cloud_skill_enabled=false', 'debug', 'prompt-input'], cwd, env, 120)
            text = json.dumps(first_json(p['stdout'], '[') or first_json(p['stdout'], '{')).replace('\\n', '\n')
            for m in re.finditer(r'^- ([A-Za-z0-9:_-]+): .*\(file: ', text, re.M):
                seen['skill'][m.group(1)] = 'debug prompt-input'
            raw['prompt_text'] = text
            q = run([*wrap('codex'), 'mcp', 'list', '--json'], cwd, env, 60)
            for s in first_json(q['stdout'], '[') or []:
                seen['mcp'][s['name']] = 'mcp list: ' + json.dumps(s.get('transport'))[:300]
            raw['covers'] = ['skill', 'mcp']
        elif agent == 'opencode':
            p = run([*wrap('opencode'), 'debug', 'skill'], cwd, env, 180)
            for s in first_json(p['stdout'], '[') or []:
                seen['skill'][s['name']] = 'debug skill: ' + str(s.get('location', ''))[-80:]
            q = run([*wrap('opencode'), 'debug', 'config'], cwd, env, 180)
            cfg = first_json(q['stdout'], '{') or {}
            if not cfg:
                raw['agent_error'] = tail(q['stdout'] + q['stderr'], 500)
            for s in cfg.get('command', {}):
                seen['command'][s] = 'debug config'
            for s in cfg.get('agent', {}):
                seen['agent'][s] = 'debug config'
            for s, v in cfg.get('mcp', {}).items():
                seen['mcp'][s] = 'debug config: ' + json.dumps(v)[:300]
            raw.update({'plugin': cfg.get('plugin'), 'instructions': cfg.get('instructions'), 'covers': ['skill', 'command', 'agent', 'mcp']})
        elif agent == 'cursor':
            raw['covers'] = []
            if not stdio_mcp:
                p = run([*wrap('cursor'), 'mcp', 'list'], cwd, env, 90)
                for line in p['stdout'].splitlines():
                    m = re.match(r'^([\w.-]+): (.+)$', line.strip())
                    if m and 'No MCP servers' not in line:
                        seen['mcp'][m.group(1)] = 'mcp list: ' + m.group(2)
                raw['covers'] = ['mcp']
        elif agent == 'grok':
            p = run([*wrap('grok'), 'inspect', '--json'], cwd, {**env, 'GROK_FOLDER_TRUST': '0'}, 90)
            d = first_json(p['stdout'], '{')
            for s in d.get('skills', []):
                seen['skill'][s['name']] = 'inspect: ' + json.dumps(s.get('source'))[:200]
            for s in d.get('agents', []):
                if (s.get('source') or {}).get('type') != 'builtin':
                    seen['agent'][s['name']] = 'inspect: ' + json.dumps(s.get('source'))[:200]
            for s in d.get('mcpServers', []):
                seen['mcp'][s['name']] = 'inspect: ' + json.dumps({k: s.get(k) for k in ('transport', 'target', 'source', 'vendor')})[:400]
            if d.get('hooks'):
                seen['hook']['hooks'] = 'inspect: ' + json.dumps(d['hooks'])[:600]
            raw = {'plugins': d.get('plugins'), 'projectInstructions': d.get('projectInstructions'), 'hooks': d.get('hooks'),
                   'covers': ['skill', 'agent', 'mcp', 'hook']}
        elif agent == 'agy':
            p = run([*wrap('agy'), 'plugin', 'list'], cwd, env, 60)
            raw = {'plugin_list': tail(p['stdout'], 400), 'covers': []}
        raw['error'] = None
    except Exception as e:      # a probe that fails leaves the cell to the file check and says so
        raw = {'error': f'{type(e).__name__}: {e}'[:300], 'covers': []}
    return seen, raw


# ---------------------------------------------------------------- what is on disk where the agent reads
def load(path):
    try:
        text = Path(path).read_text(errors='replace')
        if str(path).endswith('.toml'):
            return tomllib.loads(text), text
        return json.loads(re.sub(r'^\s*//.*$', '', text, flags=re.M)), text
    except Exception:
        try:
            return None, Path(path).read_text(errors='replace')
        except Exception:
            return None, ''


def find_servers(data):
    if not isinstance(data, dict):
        return {}
    out = {}
    for key in ('mcpServers', 'mcp_servers', 'mcp', 'servers'):
        if isinstance(data.get(key), dict):
            out.update({k: v for k, v in data[key].items() if isinstance(v, dict)})
    for proj in (data.get('projects') or {}).values() if isinstance(data.get('projects'), dict) else []:
        if isinstance(proj, dict):
            out.update(find_servers(proj))
    return out


def on_disk(agent, roots, exp):
    """roots: project, home, codex_home, opencode_config, cursor_user, plugins={agent: [dir]}. Returns kind -> {name: path}."""
    found = {k: {} for k in KINDS}
    subs = {k: str(v) for k, v in roots.items() if k != 'plugins'}
    for kind, patterns in LOCATIONS.get(agent, {}).items():
        for pat in patterns:
            bases = roots['plugins'].get(agent, []) if pat.startswith('<plugin>') else [None]
            for base in bases:
                full = pat.replace('<plugin>', str(base)) if base else pat.format(**subs)
                anchor, _, rest = full.partition('*')
                anchor_dir = Path(anchor if anchor.endswith('/') else str(Path(anchor).parent) + '/')
                paths = [Path(full)] if '*' not in full else list(anchor_dir.glob(full[len(str(anchor_dir)) + 1:])) if anchor_dir.is_dir() else []
                for p in paths:
                    if not p.is_file():
                        continue
                    if kind in ('skill', 'command', 'agent'):
                        name = p.parent.name if p.name == 'SKILL.md' else p.stem
                        found[kind][name] = str(p)
                        meta = frontmatter(p.read_text(errors='replace'))[0]
                        if meta.get('name'):
                            found[kind][meta['name']] = str(p)
                    elif kind == 'rule':
                        text = p.read_text(errors='replace')
                        for name, e in exp['rule'].items():
                            if norm(p.stem) in e['aliases'] or (e['marker'] and e['marker'] in text):
                                found['rule'][name] = str(p)
                    elif kind == 'hook':
                        text = p.read_text(errors='replace')
                        # a hook file may point at per-hook spec files (Agentpack does); read those too
                        refs = [Path(x) for x in set(re.findall(r'(/[^\s"\']+)', text))][:40]
                        refs = [f for x in refs for f in ([x] if x.is_file() else sorted(x.glob('*/specs/*.json'))[:60] if x.is_dir() else [])]
                        deep = text + ''.join(x.read_text(errors='replace') for x in refs if x.suffix == '.json')
                        if agent == 'opencode':    # an OpenCode plugin is code; only code and its config count
                            deep = text if p.suffix in ('.js', '.ts', '.mjs', '.json') and '/package/' not in str(p) else ''
                        data, _ = load(p)
                        events = sorted((data or {}).get('hooks', {})) if isinstance((data or {}).get('hooks'), dict) else []
                        for e in exp['hook'].values():
                            hit = [x for x in e['scripts'] if x in deep]
                            if hit:
                                found['hook'].setdefault('hooks', f"{p} (events {', '.join(events) or 'n/a'}; references {', '.join(hit[:3])})")
                    elif kind == 'mcp':
                        data, _ = load(p)
                        for name, spec in find_servers(data).items():
                            found['mcp'][name] = f"{p}: {json.dumps(spec)[:300]}"
    return found


def plugin_dirs(tool, root, home):
    """Plugin directories an agent loads: installed natively, or staged by Agentpack and passed by its launcher."""
    out = {a: [] for a in AGENTS}
    data, _ = load(home / '.claude/plugins/installed_plugins.json')
    for entries in ((data or {}).get('plugins') or {}).values():
        out['claude'] += [Path(e['installPath']) for e in entries if e.get('installPath')]
    out['codex'] += [p.parent.parent for p in (home / '.codex/plugins').glob('**/.codex-plugin/plugin.json')]
    out['grok'] += [p for p in (home / '.grok/plugins').glob('*') if p.is_dir()]
    out['agy'] += [p.parent for p in (home / '.gemini').glob('**/plugins/*/plugin.json')]
    if tool == 'agentpack':
        for mode in (root / 'other/staging').glob('projects/*/modes/default'):
            out['claude'].append(mode / 'plugins/agentpack-bundle')
            out['grok'].append(mode / 'grok/agentpack-bundle')
            out['agy'].append(mode / 'agy/agentpack-bundle')
    return out


def agentpack_codex_home(root):
    """Agentpack keeps the Codex home it launches with outside its staging root; a pointer file names it."""
    for ptr in (root / 'other/agentpack-home').glob('projects/*/codex-generations/default.current'):
        p = Path(ptr.read_text().strip())
        if p.is_dir():
            return p
    return None


def mcp_same(spec, evidence):
    """A server with the right name counts only if it is the input's server: same launcher and package, or same URL."""
    if 'command' in spec:
        pkgs = [a.rsplit('@', 1)[0] if '@' in a[1:] else a for a in spec.get('args', []) if not a.startswith('-')]
        if 'inspect: ' in evidence:       # Grok lists the launcher of a stdio server without its arguments
            return f'"target": "{spec["command"]}' in evidence
        return spec['command'] in evidence and all(p in evidence for p in pkgs[:1])
    return re.sub(r'^https?://', '', spec.get('url', '')).split('/')[0] in evidence


def match(expected_item, names):
    for n, ev in names.items():
        if norm(n) in expected_item['aliases']:
            return n, ev
    return None


def cell(agent, kind, exp, listed, raw, disk):
    items = exp[kind]
    if not items:
        return {'status': 'n/a', 'expected': 0}
    covered = kind in raw.get('covers', [])
    hits, converted, evidence = [], {}, []
    for name, e in items.items():
        m = match(e, listed[kind]) if covered else None
        f = match(e, disk[kind])
        if kind == 'mcp':
            m = m if m and mcp_same(e['spec'], m[1]) else None
            f = f if f and mcp_same(e['spec'], f[1]) else None
        if kind == 'hook':
            m = ('hooks', listed['hook']['hooks']) if covered and listed['hook'] else None
            f = ('hooks', disk['hook']['hooks']) if disk['hook'] else None
        if m or (f and not covered):
            hits.append(name)
            evidence.append(f"{name}: " + (f"listed by the agent ({m[1]})" if m else f"file {f[1]}"))
            if m and f:
                evidence[-1] += f"; file {f[1]}"
            continue
        if f and covered:
            evidence.append(f"{name}: file {f[1]} exists but the agent does not list it")
        for other in ('skill', 'command', 'agent'):
            if other != kind and kind in ('command', 'agent', 'rule'):      # only prompts can be re-rendered as another kind
                c = match(e, listed[other]) or (None if other in raw.get('covers', []) else match(e, disk[other]))
                if c:
                    converted[name] = other
                    evidence.append(f"{name}: present as {other} ({c[1]})")
                    break
    n = len(items)
    if len(hits) == n:
        status = 'converted to an OpenCode plugin' if (agent, kind) == ('opencode', 'hook') else 'native'
    elif len(hits) + len(converted) == n and converted:
        status = 'converted to ' + '/'.join(sorted(set(converted.values()))) if not hits else \
            f"partial: {len(hits)} native, {len(converted)} converted to {'/'.join(sorted(set(converted.values())))}"
    elif hits or converted:
        status = f"partial: {len(hits)} native, {len(converted)} converted, {n - len(hits) - len(converted)} absent of {n}"
    else:
        status = 'absent'
    return {'status': status, 'expected': n, 'delivered': len(hits), 'converted': len(converted),
            'decided_by': 'agent listing' if covered else 'files at documented locations', 'evidence': evidence[:12]}


# ---------------------------------------------------------------- one run
def locks(tool, files, root, inp, exp):
    out = []
    for rel in sorted(files):
        name = rel.split('/')[-1].lower()
        if not (LOCKNAME.search(name) or name in ('installed_plugins.json', 'known_marketplaces.json')) or name.endswith(('.lock.tmp', '.rebuild.lock')):
            continue
        # lockfiles that belong to the fetched packages themselves, or to caches, are not the tool's lockfile
        if name in UPSTREAM_LOCKS or any(x in '/' + rel for x in ('/cache/', '/marketplaces/', '/installed-plugins/', '/.tmp/', '/repos/', '/store/', '/skills/', '/plugins/cache/')):
            continue
        p = root / rel
        if not p.is_file() or p.stat().st_size > 2_000_000 or name.startswith('package-lock'):
            continue
        text = p.read_text(errors='replace')
        if not text.strip() or 'lock' in name and len(text) < 3:
            continue
        commits = set(HEX40.findall(text))
        want = {inp['commit'], inp.get('content', inp)['commit']}
        pin = re.findall(r'@playwright/mcp[^\n]{0,200}', text)
        out.append({'path': rel, 'bytes': len(text), 'records_a_commit': bool(commits), 'records_the_pinned_commit': bool(commits & want),
                    'records_a_content_hash': bool(HASH.search(text)), 'mcp_package_lines': pin[:4],
                    'sha256': hashlib.sha256(text.encode()).hexdigest()})
    return out


def mcp_pin(root, files, exp):
    """For an npx server: the argument each written config carries (as written, or an exact version)."""
    pkgs = [a for e in exp['mcp'].values() for a in e['spec'].get('args', []) if '@' in a[1:]]
    out = {}
    for pkg in pkgs:
        base = pkg.rsplit('@', 1)[0]
        for rel in sorted(files):
            p = root / rel
            if 'lock' in rel.lower() or '/cache/' in rel or not p.is_file() or p.stat().st_size > 500_000:
                continue
            for m in set(re.findall(re.escape(base) + r'@[\w.^~-]+', p.read_text(errors='replace'))):
                out.setdefault(m, []).append(rel)
    return {k: v[:8] for k, v in out.items()}


def native_summary(steps):
    """For the no-tool route: did each agent's own command accept the input?"""
    out = {'opencode': 'no install command for this format'}
    for agent, exe, verb in (('claude', 'claude', 'install'), ('codex', 'codex', 'add'), ('grok', 'grok', 'install'),
                             ('cursor', 'cursor-agent', 'marketplace'), ('agy', 'agy', 'install')):
        last = [x for x in steps if x['argv'][0] == exe and verb in x['argv'][1:4]][-1]
        out[agent] = 'yes' if last['exit'] == 0 else f"no: {last.get('output_tail', '')[-220:]}"
    return out


def one(tool, inp_id, inp, args, baseline):
    exp = expected(args.src, inp)
    root = args.runs / f"{tool}--{inp_id}"
    shutil.rmtree(root, ignore_errors=True)
    home, P = root / 'home', root / 'project'
    home.mkdir(parents=True)
    P.mkdir()
    (root / 'other').mkdir()
    env0 = clean_env(home)
    subprocess.run(['git', 'init', '-q', str(P)], check=True, env=env0)
    (P / 'README.md').write_text('Throwaway checkout\n')
    exe = args.agentpack if tool == 'agentpack' else (None if tool == 'native' else tool_exe(args.tools_dir, tool))
    ctx = {'exe': exe, 'project': P, 'root': root, 'src': args.src}
    pl = plan(tool, inp, exp, ctx)
    env = {**env0, **{k: str(v) for k, v in pl['env'].items()}}
    before = snapshot(root)
    steps, failed = [], None
    for argv in pl['steps']:
        optional = argv[0] == '?'
        argv = argv[1:] if optional else argv
        if pl.get('prepare') and pl['prepare'][0] == 'ruler' and argv[1:2] == ['apply']:
            ruler_prepare(P, pl['prepare'][1], exp)
        if pl.get('prepare') and pl['prepare'][0] == 'rulesync-source' and argv[1:2] == ['install']:
            cfg = P / 'rulesync.jsonc'      # the documented manual edit: add one entry to "sources"
            data = json.loads(re.sub(r'^\s*//.*$', '', cfg.read_text(), flags=re.M))
            data['sources'] = [pl['prepare'][1]]
            cfg.write_text(json.dumps(data, indent=2))
        if failed and not optional:
            steps.append({'argv': [str(a) for a in argv], 'exit': 'skipped after failure'})
            continue
        s = run(argv, P, env, 900)
        s['output_tail'] = tail(s.pop('stdout') + '\n' + s.pop('stderr'))
        s['optional'] = optional
        steps.append(s)
        if s['exit'] != 0 and not optional:
            failed = s
    limited = any(re.search(r'rate limit', x.get('output_tail', ''), re.I) for x in steps)
    after = snapshot(root)
    written = sorted(k for k in after if before.get(k) != after[k])
    # what each agent sees
    roots = {'project': P, 'home': home, 'codex_home': home / '.codex', 'opencode_config': home / '.config/opencode',
             'cursor_user': home / '.cursor', 'plugins': plugin_dirs(tool, root, home)}
    wrap = lambda a: [EXE[a]]
    if tool == 'agentpack':
        launcher = {'claude': 'claude', 'codex': 'codex', 'opencode': 'opencode', 'cursor': 'agent', 'grok': 'grok', 'agy': 'agy'}
        wrap = lambda a: [str(exe), '-q', launcher[a]]
        mode = next(iter((root / 'other/staging').glob('projects/*/modes/default')), root / 'none')
        roots.update(codex_home=agentpack_codex_home(root) or mode / 'codex-home', opencode_config=mode / 'opencode',
                     cursor_user=mode / 'cursor-home/.cursor')
    cells, probes = {}, {}
    with tempfile.TemporaryDirectory(dir=root / 'other') as tmp:
        for agent in AGENTS:
            listed, raw = listing(agent, env, P, wrap, exp, tmp)
            for kind in KINDS:      # drop what the same agent lists in an empty home and checkout
                listed[kind] = {n: v for n, v in listed[kind].items() if n not in baseline.get(agent, {}).get(kind, [])}
            if tool == 'agentpack':     # launchers switch generations; re-resolve the staged Codex home
                roots['codex_home'] = agentpack_codex_home(root) or roots['codex_home']
            disk = on_disk(agent, roots, exp)
            disk['mcp'].update(raw.pop('plugin_mcp', None) or {})
            cells[agent] = {kind: cell(agent, kind, exp, listed, raw, disk) for kind in KINDS}
            raw.pop('prompt_text', None)
            probes[agent] = {'listing': {k: sorted(v) for k, v in listed.items() if v}, **{k: v for k, v in raw.items()}}
    outside = None
    if tool == 'agentpack' and agentpack_codex_home(root):
        gen = agentpack_codex_home(root)
        outside = {'path': str(gen), 'files': len(snapshot(gen)), 'what': 'Codex home that agentpack codex launches with',
                   'removed_after_measurement': False}
        if str(gen).startswith('/var/tmp/ap-c/') and gen.parent.parent.name in [d.name for d in (root / 'other/agentpack-home/projects').iterdir()]:
            shutil.rmtree(gen.parent.parent, ignore_errors=True)       # only the directory this run's project created
            outside['removed_after_measurement'] = True
    launch = snapshot(P)
    launch_writes = sorted(k for k in launch if after.get('project/' + k) != launch[k])
    rel = lambda s: s.replace(str(root) + '/', '')
    result = {
        'tool': tool, 'input': inp_id, 'run_root': str(root),
        'invalid': 'a step reported a GitHub API rate limit; rerun this cell' if limited else None,
        'documented_usage': pl['doc'], 'note': pl.get('note'),
        'accepted_as_published': 'no' if (failed or pl['not_accepted']) else ('after repackaging by hand' if pl['repackaged'] else 'yes'),
        'reason': pl['not_accepted'] or (f"exit {failed['exit']}: " + failed['output_tail'][-500:] if failed else None),
        'native_per_agent': native_summary(steps) if tool == 'native' else None,
        'repackaging': pl['repackaged'], 'needs_local_clone': bool(pl.get('local_clone')),
        'steps': steps,
        'expected': {k: sorted(v) for k, v in exp.items() if v},
        'cells': json.loads(rel(json.dumps(cells))),
        'probes': json.loads(rel(json.dumps(probes, default=str))),
        'lockfiles': locks(tool, written, root, inp, exp),
        'mcp_package_as_delivered': mcp_pin(root, written, exp),
        'writes': where_written(written),
        'project_files_written': [w[8:] for w in written if w.startswith('project/')][:60],
        'project_writes_at_launch': launch_writes[:20],
        'writes_outside_run_root': outside,
    }
    return result


def baseline_listing(args):
    """What each agent lists with nothing installed, to subtract built-ins."""
    root = args.runs / '_baseline'
    shutil.rmtree(root, ignore_errors=True)
    (root / 'home').mkdir(parents=True)
    (root / 'project').mkdir()
    env = clean_env(root / 'home')
    subprocess.run(['git', 'init', '-q', str(root / 'project')], check=True, env=env)
    empty = {k: {} for k in KINDS}
    out = {}
    with tempfile.TemporaryDirectory(dir=root) as tmp:
        for agent in AGENTS:
            listed, raw = listing(agent, env, root / 'project', lambda a: [EXE[a]], empty, tmp)
            out[agent] = {k: sorted(v) for k, v in listed.items()}
            out[agent]['error'] = raw.get('error')
    return out


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument('action', choices=['setup', 'fetch', 'run'])
    ap.add_argument('--tools-dir', type=Path, required=True)
    ap.add_argument('--agentpack', type=Path, help='released Agentpack 0.4.8 binary')
    ap.add_argument('--work', type=Path, default=Path(tempfile.gettempdir()) / 'agentpack-ecosystem')
    ap.add_argument('--tool', action='append', choices=sorted(TOOLS))
    ap.add_argument('--input', action='append', choices=sorted(INPUTS))
    ap.add_argument('--output', type=Path, default=HERE / 'ecosystem-results.json')
    ap.add_argument('--resume', action='store_true', help='keep valid runs already in --output')
    ap.add_argument('--runs', type=Path, help='directory for the per-run homes and checkouts (default: WORK/runs)')
    ap.add_argument('--keep-runs', action='store_true', help='keep each run directory (about 50 MB each) instead of deleting it')
    args = ap.parse_args()
    args.tools_dir = args.tools_dir.resolve()
    if args.action == 'setup':
        return setup(args.tools_dir)
    args.work = args.work.resolve()
    args.src = args.work / 'src'
    args.src.mkdir(parents=True, exist_ok=True)
    args.runs = (args.runs or args.work / 'runs').resolve()
    args.runs.mkdir(parents=True, exist_ok=True)
    if args.action == 'fetch':
        for inp in INPUTS.values():
            c = inp.get('content', inp)
            print(checkout(args.src, inp['repo'], inp['commit']), checkout(args.src, c['repo'], c['commit']))
        return
    args.agentpack = args.agentpack.resolve()
    if sha(args.agentpack) != TOOLS['agentpack']['binary_sha256']:
        sys.exit('--agentpack is not the released 0.4.8 binary recorded in this script')
    versions = {}
    for a in AGENTS:
        got = run([EXE[a], '--version'], args.work, clean_env(args.runs / '_version-home'), 60)
        versions[a] = (got['stdout'].strip().splitlines() or ['?'])[-1].replace(' [stable]', '')
        if versions[a] != AGENT_VERSIONS[a]:
            sys.exit(f"{a}: recorded with {AGENT_VERSIONS[a]}, found {versions[a]}")
    previous = json.loads(args.output.read_text()) if args.output.exists() else {'runs': {}}
    runs = previous.get('runs', {})
    baseline = baseline_listing(args)
    def save():
        args.output.write_text(json.dumps({
            'date': time.strftime('%Y-%m-%d'), 'platform': platform.platform(), 'model_inference_requested': False,
            'agents': versions, 'tools': TOOLS, 'inputs': INPUTS, 'kinds': KINDS,
            'cell_legend': 'per agent, kinds in order skill, command, agent(subagent), rule, hook, mcp',
            'locations_for_file_checks': LOCATIONS, 'baseline_listing': baseline,
            'runs': {k: runs[k] for k in sorted(runs)}}, indent=1) + '\n')

    for tool in args.tool or list(TOOLS):
        for inp_id in args.input or list(INPUTS):
            key = f"{tool}--{inp_id}"
            if args.resume and key in runs and not runs[key].get('invalid'):
                continue
            if shutil.disk_usage(args.runs).free < 700_000_000:
                sys.exit('less than 700 MB free: stopping before a run is distorted by a full disk')
            r = one(tool, inp_id, INPUTS[inp_id], args, baseline)
            if not args.keep_runs:
                shutil.rmtree(args.runs / key, ignore_errors=True)
            runs[key] = r
            row = ' '.join(f"{a}:" + ''.join({'native': 'N', 'absent': '-', 'n/a': '.'}.get(c['status'], 'c' if c['status'].startswith('converted') else 'p')
                                             for c in r['cells'][a].values()) for a in AGENTS)
            print(f"{tool:20} {inp_id:30} accepted={r['accepted_as_published'][:5]:5} {row}" + ('  RATE LIMITED' if r['invalid'] else ''), flush=True)
            save()
    save()


if __name__ == '__main__':
    main()
