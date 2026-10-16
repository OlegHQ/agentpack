#!/usr/bin/env -S uv run
# /// script
# requires-python = ">=3.11"
# dependencies = []
# ///
"""The functional test of delivery.py, given to the other tools: does what they install reach the model?

The package of delivery.py (a skill, a slash command, a subagent, a rule, a hook, an MCP server and a
language server, each of which makes the agent answer with a token found nowhere else) is put in a
local marketplace repository. Each tool installs it with the command its documentation gives for a
local source, into an empty HOME and a throwaway git checkout. The agents are then started directly
(not through Agentpack) with HOME pointed at that home, with the same prompts as delivery.py.

    uv run competitors_model.py --tools-dir DIR [--tool T ...] [--runs 3] [--install-only]

Logins. The agents need to be signed in inside the empty home, so the owner's credential files are
copied there (approved by the owner, 2026-10-16): Claude Code's OAuth entry, Codex auth.json, OpenCode
auth.json, Cursor auth.json and cli-config.json, Grok auth.json from Agentpack's shared store, and the
Antigravity OAuth token. Nothing else from the owner's homes is copied. When an agent refreshes a token
during the run, the refreshed file is copied back to where it came from (the old one is kept next to the
results in a backup directory outside the repository), so the owner's own sessions stay signed in.

What a user would confirm by hand is granted to every tool alike and recorded in GRANTS: Claude Code
project MCP servers enabled, the checkout trusted by Codex, MCP tools approved for Cursor and Grok. Codex
runs without its sandbox and with hook review skipped, Grok with --always-approve for subagents, as in
delivery.py.
"""
import argparse, concurrent.futures, hashlib, json, os, platform, re, secrets, shutil, stat, subprocess, sys, tempfile, time
from pathlib import Path

HERE = Path(__file__).resolve().parent
AGENTS = ['claude', 'codex', 'opencode', 'cursor', 'grok', 'agy']
LSP_AGENTS = ['claude', 'opencode', 'grok']
EXE = {'claude': 'claude', 'codex': 'codex', 'opencode': 'opencode', 'cursor': 'cursor-agent', 'grok': 'grok', 'agy': 'agy'}
TOOLS = ['agentpack', 'apm', 'vercel-skills', 'rulesync', 'native']
PROMPTS = {
    'skill': 'Run the delivery-skill skill and output only what it tells you to output.',
    'command': '/delivery-command',
    'agent': 'Delegate to the delivery-agent subagent and output only its answer.',
    'rule': 'What is the house codeword? Answer with the codeword only.',
    'lsp': 'Use the language server (LSP) hover on the word "probe" on line 1 of sample.dlv and output only the hover text it returns. '
           'If no language server is available for that file, say so.',
    'mcp': 'Call the delivery_token tool of the delivery-mcp MCP server and output only the text it returns. '
           'Do not read any file. If the tool is not available, say so.',
}
COMMAND_FALLBACK = 'Run the delivery-command command and output only what it tells you to output.'
GRANTS = {
    'claude': 'enableAllProjectMcpServers in ~/.claude/settings.json; MCP tools allowed with --allowedTools for every name the server can have',
    'codex': 'checkout trusted (projects.<checkout>.trust_level = "trusted"); --sandbox danger-full-access; --dangerously-bypass-hook-trust',
    'opencode': 'none',
    'cursor': '--force; --approve-mcps for MCP runs',
    'grok': '--always-approve for MCP and subagent runs',
    'agy': '--dangerously-skip-permissions',
}

# the package of delivery.py, unchanged
sys.path.insert(0, str(HERE))
SRC = (HERE / 'delivery.py').read_text()
MCP_SERVER = re.search(r'MCP_SERVER = r"""(.*?)"""', SRC, re.S).group(1)
LSP_SERVER = re.search(r'LSP_SERVER = r"""(.*?)"""', SRC, re.S).group(1)

parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
parser.add_argument('--tools-dir', type=Path, required=True, help='the tools directory made by ecosystem.py setup')
parser.add_argument('--tool', nargs='+', default=TOOLS, choices=TOOLS)
parser.add_argument('--agentpack', type=Path, help='Agentpack binary, for the agentpack row')
parser.add_argument('--agentpack-sha256', help='sha256 the binary must have; recorded in the results')
parser.add_argument('--agents', nargs='+', default=AGENTS, choices=AGENTS)
parser.add_argument('--kinds', nargs='+', default=list(PROMPTS), choices=list(PROMPTS))
parser.add_argument('--runs', type=int, default=3)
parser.add_argument('--jobs', type=int, default=4)
parser.add_argument('--timeout', type=int, default=240)
parser.add_argument('--opencode-model', default='openai/gpt-6.1-sol')
parser.add_argument('--install-only', action='store_true', help='install and list files; no login copied, no model called')
parser.add_argument('--work', type=Path, default=None)
parser.add_argument('--backup-dir', type=Path, required=True, help='where replaced credential files are kept (outside the repository)')
parser.add_argument('--output', type=Path, default=HERE / 'competitors-model-results.json')
args = parser.parse_args()
TD = args.tools_dir.resolve()
EXES = {'apm': TD / 'apm/bin/apm-linux-arm64/apm', 'vercel-skills': TD / 'node/node_modules/.bin/skills',
        'rulesync': TD / 'node/node_modules/.bin/rulesync'}
REAL = Path.home()
if 'agentpack' in args.tool:
    if not args.agentpack or hashlib.sha256(args.agentpack.read_bytes()).hexdigest() != (args.agentpack_sha256 or '').lower():
        sys.exit('the agentpack row needs --agentpack and its --agentpack-sha256')
    AP = args.agentpack.resolve()


def write(path, text):
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(text)


def new_tokens():
    """Fresh tokens for one tool's run, so that a file left by another run, or by another tool's install next door,
    can never answer for this one."""
    n = lambda: str(secrets.randbelow(900000) + 100000)
    return {'skill': 'SKILL-OK-' + n(), 'command': 'COMMAND-OK-' + n(), 'agent': 'AGENT-OK-' + n(), 'rule': 'RULE-OK-' + n(),
            'mcp': 'MCP-OK-' + n(), 'lsp': 'LSP-OK-' + n()}


def build_repo(repo, TOKENS):
    """A marketplace repository holding the delivery package as plugins/delivery-pack (a Claude Code plugin)."""
    root = repo / 'plugins/delivery-pack'
    write(root / '.claude-plugin/plugin.json', json.dumps({'name': 'delivery-pack', 'version': '1.0.0', 'description': 'Delivery test package'}))
    write(root / 'skills/delivery-skill/SKILL.md',
          '---\nname: delivery-skill\ndescription: Delivery test skill. Use when asked to run the delivery-skill skill.\n---\n\n'
          f'Reply with exactly this line and nothing else: {TOKENS["skill"]}\n')
    write(root / 'commands/delivery-command.md', '---\ndescription: Delivery test command\n---\n\n'
          f'Reply with exactly this line and nothing else: {TOKENS["command"]}\n')
    write(root / 'agents/delivery-agent.md',
          '---\nname: delivery-agent\ndescription: Delivery test subagent. Use when asked to delegate to delivery-agent.\n---\n\n'
          f'You are the delivery test subagent. Whatever you are asked, answer with exactly this line and nothing else: {TOKENS["agent"]}\n')
    write(root / 'rules/delivery-rule.mdc', '---\ndescription: House codeword\nalwaysApply: true\n---\n\n'
          f'The house codeword is {TOKENS["rule"]}. When asked for the house codeword, answer with it.\n')
    # the servers build their token at run time, so it is in no file as text
    write(root / 'mcp/server.mjs', MCP_SERVER.replace('String(7000 + 735)', "'" + "', '".join(TOKENS['mcp'].split('-')[2:]) + "'"))
    write(root / '.mcp.json', json.dumps({'mcpServers': {'delivery-mcp': {'command': 'node', 'args': ['${CLAUDE_PLUGIN_ROOT}/mcp/server.mjs']}}}, indent=1))
    write(root / 'lsp/server.mjs', LSP_SERVER.replace('String(8000 + 246)', "'" + TOKENS['lsp'].split('-')[2] + "'"))
    write(root / '.lsp.json', json.dumps({'delivery-lsp': {'command': 'node', 'args': ['${CLAUDE_PLUGIN_ROOT}/lsp/server.mjs'],
                                                           'extensionToLanguage': {'.dlv': 'delivery'}}}, indent=1))
    write(root / 'hooks/hooks.json', json.dumps({'hooks': {'UserPromptSubmit': [{'hooks': [
        {'type': 'command', 'command': 'sh -c \'echo hook-ran >> "$DELIVERY_HOOK_MARK"\''}]}]}}, indent=1))
    write(repo / '.claude-plugin/marketplace.json', json.dumps({'name': 'delivery-market', 'owner': {'name': 'test'},
          'plugins': [{'name': 'delivery-pack', 'source': './plugins/delivery-pack', 'description': 'Delivery test package'}]}, indent=1))
    for cmd in (['init', '-q'], ['add', '-A'], ['-c', 'user.name=t', '-c', 'user.email=t@t', 'commit', '-qm', 'delivery package']):
        subprocess.run(['git', *cmd], cwd=repo, check=True)
    return root


def clean_env(home, extra=None):
    env = {'PATH': os.environ['PATH'], 'HOME': str(home), 'USERPROFILE': str(home), 'XDG_CONFIG_HOME': str(home / '.config'),
           'XDG_DATA_HOME': str(home / '.local/share'), 'XDG_CACHE_HOME': str(home / '.cache'), 'XDG_STATE_HOME': str(home / '.local/state'),
           'GIT_TERMINAL_PROMPT': '0', 'NO_COLOR': '1', 'TERM': 'dumb', 'LANG': 'C.UTF-8', 'DISABLE_TELEMETRY': '1',
           'DISABLE_AUTOUPDATER': '1', 'GROK_DISABLE_AUTOUPDATER': '1', 'AGY_CLI_DISABLE_AUTO_UPDATE': '1', 'OPENCODE_DISABLE_AUTOUPDATE': '1'}
    env.update(extra or {})
    return env


def run(argv, cwd, env, timeout=600):
    t = time.time()
    with tempfile.TemporaryFile('w+', errors='replace') as fo, tempfile.TemporaryFile('w+', errors='replace') as fe:
        try:
            code = subprocess.run([str(a) for a in argv], cwd=cwd, env=env, stdout=fo, stderr=fe, timeout=timeout, stdin=subprocess.DEVNULL).returncode
        except subprocess.TimeoutExpired:
            code = 'timeout'
        except FileNotFoundError:
            code = 'not-found'
        fo.seek(0), fe.seek(0)
        return {'argv': [str(a) for a in argv], 'exit': code, 'seconds': round(time.time() - t, 1), 'out': fo.read() + '\n' + fe.read()}


def install(tool, repo, plugin, home, ws):
    env = clean_env(home, {'AGENTPACK_STAGING_ROOT': str(home.parent / 'staging')} if tool == 'agentpack' else None)
    steps = []
    if tool == 'agentpack':
        steps += [[AP, '--no-progress', 'init'], [AP, '--no-progress', 'add', str(plugin)], [AP, '--no-progress', 'lock'],
                  [AP, '--no-progress', 'sync']]
    elif tool == 'apm':
        steps.append([EXES['apm'], 'install', str(plugin), '-t', 'claude,codex,cursor,opencode,grok-build,antigravity', '--trust-transitive-mcp'])
    elif tool == 'vercel-skills':
        steps.append([EXES['vercel-skills'], 'add', str(plugin), '--skill', '*',
                      '-a', 'claude-code', 'codex', 'opencode', 'cursor', 'grok', 'antigravity', 'antigravity-cli', '-y'])
    elif tool == 'rulesync':
        x = EXES['rulesync']
        steps += [[x, 'init'], [x, 'import', '--targets', 'claudecode-plugin', '--features', '*', '--output-root', str(plugin)],
                  [x, 'generate', '--targets', 'claudecode,codexcli,opencode,cursor,grokcli,antigravity-cli', '--features', '*']]
    elif tool == 'native':
        steps += [['claude', 'plugin', 'marketplace', 'add', str(repo)],
                  ['claude', 'plugin', 'install', 'delivery-pack@delivery-market', '--scope', 'project'],
                  ['codex', 'plugin', 'marketplace', 'add', str(repo)],
                  ['codex', 'plugin', 'add', 'delivery-pack@delivery-market'],
                  ['grok', 'plugin', 'install', str(plugin), '--trust'],
                  ['cursor-agent', 'plugin', 'marketplace', 'add', str(repo)],
                  ['agy', 'plugin', 'install', str(plugin)]]
    out = []
    for s in steps:
        r = run(s, ws, env, timeout=600)
        out.append({'argv': [a.replace(str(home.parent), '<tmp>') for a in r['argv']], 'exit': r['exit'],
                    'tail': r['out'].replace(str(home.parent), '<tmp>').strip()[-600:]})
    return out


SKIP = {'.git', 'node_modules', '.cache', '.npm', 'mcp', 'lsp'}


def written(root):
    return sorted(str(p.relative_to(root)) for p in root.rglob('*') if p.is_file() and not (set(p.relative_to(root).parts) & SKIP))


# ------------------------------------------------------------- logins
def creds():
    """(source on the owner's machine, path under the test home) for every login copied."""
    shared = REAL / '.local/share/agentpack/shared'
    return [(REAL / '.claude/.credentials.json', '.claude/.credentials.json'),
            (REAL / '.codex/auth.json', '.codex/auth.json'),
            (REAL / '.local/share/opencode/auth.json', '.local/share/opencode/auth.json'),
            (REAL / '.config/cursor/auth.json', '.config/cursor/auth.json'),
            (shared / 'grok/auth.json', '.grok/auth.json'),
            (REAL / '.gemini/antigravity-cli/antigravity-oauth-token', '.gemini/antigravity-cli/antigravity-oauth-token')]


def merge_json(path, patch):
    d = {}
    if path.is_file():
        try:
            d = json.loads(path.read_text() or '{}')
        except json.JSONDecodeError:
            d = {}
    d.update(patch)
    write(path, json.dumps(d, indent=1))


def sign_in(home, ws):
    copied = {}
    for src, rel in creds():
        if not src.is_file():
            continue
        dest = home / rel
        dest.parent.mkdir(parents=True, exist_ok=True)
        data = src.read_bytes()
        if rel == '.claude/.credentials.json':      # the account login only, not the owner's MCP server tokens
            data = json.dumps({'claudeAiOauth': json.loads(data)['claudeAiOauth']}).encode()
        dest.write_bytes(data)
        dest.chmod(0o600)
        copied[rel] = {'source': str(src), 'sha256': hashlib.sha256(data).hexdigest(), 'source_sha256': hashlib.sha256(src.read_bytes()).hexdigest()}
    # Cursor reads its settings next to the login; Agentpack copies the same file
    if (REAL / '.config/cursor/cli-config.json').is_file() and not (home / '.config/cursor/cli-config.json').exists():
        shutil.copy(REAL / '.config/cursor/cli-config.json', home / '.config/cursor/cli-config.json')
    real = json.loads((REAL / '.claude.json').read_text())
    merge_json(home / '.claude.json', {k: real[k] for k in ('oauthAccount', 'userID', 'hasCompletedOnboarding', 'lastOnboardingVersion') if k in real})
    merge_json(home / '.claude/settings.json', {'enableAllProjectMcpServers': True})
    cfg = home / '.codex/config.toml'
    text = cfg.read_text() if cfg.is_file() else ''
    if 'cli_auth_credentials_store' not in text:
        text = 'cli_auth_credentials_store = "file"\n' + text
    if f'[projects."{ws}"]' not in text:
        text += f'\n[projects."{ws}"]\ntrust_level = "trusted"\n'
    write(cfg, text)
    return copied


def hand_back(home, copied, backups):
    """Copy a token the agent refreshed back to where it came from, so the owner stays signed in."""
    notes = []
    for rel, c in copied.items():
        dest, src = home / rel, Path(c['source'])
        shared = home / '.local/share/agentpack/shared/grok/auth.json'
        if rel == '.grok/auth.json' and shared.is_file() and (not dest.is_file() or shared.stat().st_mtime > dest.stat().st_mtime):
            dest = shared       # the Agentpack row keeps Grok's login in its own store
        if not dest.is_file():
            continue
        now = hashlib.sha256(dest.read_bytes()).hexdigest()
        if now == c['sha256']:
            continue
        if rel == '.claude/.credentials.json':
            new = json.loads(dest.read_text()).get('claudeAiOauth')
            merged = json.loads(src.read_text())
            if hashlib.sha256(src.read_bytes()).hexdigest() != c['source_sha256']:
                notes.append(f'{rel}: refreshed in the test home, but the source changed meanwhile; not copied back')
                continue
            merged['claudeAiOauth'] = new
            data = json.dumps(merged).encode()
        else:
            if hashlib.sha256(src.read_bytes()).hexdigest() != c['source_sha256']:
                notes.append(f'{rel}: refreshed in the test home, but the source changed meanwhile; not copied back')
                continue
            data = dest.read_bytes()
        backups.mkdir(parents=True, exist_ok=True)
        shutil.copy2(src, backups / (rel.replace('/', '_') + time.strftime('.%Y%m%dT%H%M%S')))
        tmp = src.with_name(src.name + '.tmp-competitors')
        tmp.write_bytes(data)
        tmp.chmod(0o600)
        os.replace(tmp, src)
        c['sha256'] = now
        c['source_sha256'] = hashlib.sha256(data).hexdigest()
        notes.append(f'{rel}: refreshed during the run and copied back to {src}')
    return notes


# ------------------------------------------------------------- launching
MCP_NAMES = ['mcp__delivery-mcp__delivery_token', 'mcp__plugin_delivery-pack_delivery-mcp__delivery_token']


def launch(agent, prompt, kind, tool):
    """The agent's own non-interactive launch, with its JSON event output so that every tool call is recorded."""
    if agent == 'claude':
        argv = ['claude', '-p', prompt, '--output-format', 'stream-json', '--verbose',
                *(['--allowedTools', *MCP_NAMES] if kind == 'mcp' else [])]
    elif agent == 'codex':
        argv = ['codex', 'exec', '--json', '--skip-git-repo-check', '--sandbox', 'danger-full-access', '--dangerously-bypass-hook-trust', prompt]
    elif agent == 'opencode':
        argv = (['opencode', 'run', '--format', 'json', '-m', args.opencode_model, '--command', 'delivery-command', '']
                if prompt == PROMPTS['command'] else ['opencode', 'run', '--format', 'json', '-m', args.opencode_model, prompt])
    elif agent == 'cursor':
        argv = ['cursor-agent', '-p', prompt, '--output-format', 'stream-json', '--force', *(['--approve-mcps'] if kind == 'mcp' else [])]
    elif agent == 'grok':
        argv = ['grok', '-p', prompt, '--output-format', 'streaming-messages-json', *(['--always-approve'] if kind in ('mcp', 'agent') else [])]
    else:
        argv = ['agy', '-p', prompt, '--output-format', 'stream-json', '--dangerously-skip-permissions']
    if tool == 'agentpack':
        # Agentpack starts the same agent through its launcher; Cursor's launcher is named after its binary, agent
        argv = [AP, 'agent' if agent == 'cursor' else argv[0], *argv[1:]]
    return argv


# Where each agent itself loads content from, as paths relative to one tool's run directory (checkout/, home/, and
# for the Agentpack row its staging/ and its data directory under home/). Before an agent is started, every file that
# holds a token anywhere else is made unreadable (mode 000) and is restored afterwards: an agent that searches the
# disk finds nothing it does not load itself. The lists are generous to the tools: every directory an agent is
# documented to read skills, subagents or rules from counts as its own, whichever agent the tool wrote it for.
AP_DATA = r'home/\.local/share/agentpack/projects/[^/]+/'
OWN = {
    'claude': r'^checkout/(\.claude/|CLAUDE\.md|\.mcp\.json)|^home/\.claude(/|\.json)|^staging/modes/[^/]+/plugins/',
    'codex': r'^checkout/(\.codex/|\.agents/|AGENTS\.md)|^home/(\.codex/|\.agents/)|^' + AP_DATA + 'codex-generations/',
    'opencode': r'^checkout/(\.opencode/|\.claude/skills/|\.agents/skills/|AGENTS\.md|CLAUDE\.md|opencode\.jsonc?)'
                r'|^home/(\.config/opencode/|\.claude/skills/|\.agents/skills/)|^staging/modes/[^/]+/opencode/',
    'cursor': r'^checkout/(\.cursor/|\.claude/(skills|agents)/|\.codex/(skills|agents)/|\.agents/skills/|AGENTS\.md|CLAUDE\.md|\.cursorrules)'
              r'|^home/(\.cursor/|\.config/cursor/|\.claude/(skills|agents)/|\.codex/(skills|agents)/|\.agents/skills/)'
              r'|^staging/modes/[^/]+/(cursor|cursor-home)/',
    'grok': r'^checkout/(\.grok/|\.claude/|\.agents/|AGENTS\.md|CLAUDE\.md)|^home/(\.grok/|\.claude/|\.agents/)'
            r'|^staging/modes/[^/]+/grok/|^' + AP_DATA + 'grok-homes/',
    'agy': r'^checkout/(\.agents/|AGENTS\.md|GEMINI\.md)|^home/(\.gemini/|\.agents/)|^staging/modes/[^/]+/agy/',
}


def hide_for(agent, root, tokens, tool):
    """Make every token-holding file outside the agent's own locations unreadable; return what to restore.

    Agentpack reads a local package live at every launch, so for its row the package source stays readable; a run
    whose transcript names the source is not counted (see SOURCE below)."""
    hidden = []
    own = re.compile(OWN[agent])
    for dirpath, dirnames, filenames in os.walk(root):        # symbolic links are not followed
        dirnames[:] = [d for d in dirnames if d not in ('.git', 'node_modules', 'transcripts')]
        for name in filenames:
            f = Path(dirpath) / name
            rel = str(f.relative_to(root))
            if f.is_symlink() or own.search(rel) or (rel.startswith('repo/') and source_is_installed(tool, agent)):
                continue
            if f.suffix in ('.db', '.sqlite', '.jsonl', '.log') or 'transcripts' in f.parts:
                continue                # agents' own session state, not content a tool installed
            try:
                data = f.read_bytes()
            except OSError:
                continue
            if any(t.encode() in data for t in tokens.values()):
                hidden.append((f, stat.S_IMODE(f.stat().st_mode)))
                f.chmod(0)
    return hidden


def source_is_installed(tool, agent):
    """Where the package source is itself the installed copy: Agentpack reads a local package live, and Claude Code
    and Cursor load a plugin from a local marketplace in place."""
    return tool == 'agentpack' or (tool == 'native' and agent in ('claude', 'cursor'))


def read_source(text, source, tool, agent):
    """Whether the agent itself touched the package source: the path in any event but the agent's start-up report."""
    if source_is_installed(tool, agent) and tool == 'native':
        return False
    for line in text.splitlines():
        if source not in line:
            continue
        try:
            e = json.loads(line)
        except ValueError:
            return True
        if isinstance(e, dict) and (e.get('type') == 'system' or e.get('subtype') == 'init'):
            continue
        return True
    return False


def restore(hidden):
    for f, mode in hidden:
        try:
            f.chmod(mode)
        except FileNotFoundError:       # an agent's own state file that it replaced meanwhile
            pass


def one_tool(tool, base):
    root = base / tool
    repo, home, ws = root / 'repo', root / 'home', root / 'checkout'
    for d in (repo, home, ws):
        d.mkdir(parents=True)
    TOKENS = new_tokens()
    plugin = build_repo(repo, TOKENS)
    subprocess.run(['git', 'init', '-q', str(ws)], check=True)
    (ws / 'README.md').write_text('Benign project\n')
    (ws / 'sample.dlv').write_text('probe value\n')
    before = set(written(root))
    # Cursor's own install command needs a login, so the agents are signed in before any tool runs; the grants are
    # applied again afterwards in case a tool rewrote those settings files
    copied = {} if args.install_only else sign_in(home, ws)
    steps = install(tool, repo, plugin, home, ws)
    files = [f for f in written(root) if f not in before and not f.startswith('repo/')]
    # git keeps every file of the package in its object store, where a file's mode does not apply (an agent was seen
    # reading a hidden rule with `git show HEAD:...`); the installed copies are all that is left
    shutil.rmtree(repo / '.git', ignore_errors=True)
    result = {'tool': tool, 'tokens': TOKENS, 'install_steps': steps, 'files_written': files, 'rows': [], 'agents': {}}
    if args.install_only:
        return result
    sign_in(home, ws)
    result['logins_copied'] = sorted(copied)
    mark = root / 'hook-mark.txt'
    env = clean_env(home, {'DELIVERY_HOOK_MARK': str(mark)})
    if tool == 'agentpack':
        env['AGENTPACK_STAGING_ROOT'] = str(root / 'staging')
    if tool == 'agentpack':      # staged hooks call `agentpack hook-exec`, so the binary under test must be on PATH
        env['PATH'] = f"{AP.parent}{os.pathsep}{env['PATH']}"
    calls = lambda name: sum(len(f.read_text().splitlines()) for f in root.rglob(name) if 'repo' not in f.relative_to(root).parts)
    notes = []
    for agent in args.agents:
        hidden = hide_for(agent, root, TOKENS, tool)
        try:
            agent_runs(tool, agent, root, ws, env, TOKENS, result, calls, mark, base)
        finally:
            restore(hidden)
        result['agents'][agent]['files_hidden'] = len(hidden)
        notes += hand_back(home, copied, args.backup_dir)
    notes += hand_back(home, copied, args.backup_dir)
    result['credential_notes'] = notes
    return result




def final_answer(text):
    """The agent's final answer, from its JSON event output (each agent has its own event names)."""
    results, messages = [], []
    for line in text.splitlines():
        try:
            e = json.loads(line)
        except ValueError:
            continue
        if not isinstance(e, dict):
            continue
        kind = e.get('type') or e.get('event')
        if kind == 'result':
            r = e.get('result')
            if isinstance(r, dict):                                  # Antigravity
                r = r.get('response')
            if isinstance(r, str):
                results.append(r)
        elif kind == 'item.completed' and isinstance(e.get('item'), dict) and e['item'].get('type') == 'agent_message':
            messages.append(e['item'].get('text') or '')             # Codex
        elif kind == 'text' and isinstance(e.get('part'), dict):
            messages.append(e['part'].get('text') or '')             # OpenCode
        elif kind == 'assistant' and isinstance(e.get('message'), dict) and not e.get('parent_tool_use_id'):
            content = e['message'].get('content')                    # Claude Code, Cursor, Grok (main agent only)
            if isinstance(content, list):
                messages += [c.get('text', '') for c in content if isinstance(c, dict) and c.get('type') == 'text']
    return results[-1] if results else '\n'.join(messages)


def agent_runs(tool, agent, root, ws, env, TOKENS, result, calls, mark, base):
    source = str(root / 'repo')      # a run in which the agent touched the package source is not counted
    read_source_of = lambda text: read_source(text, source, tool, agent)
    if True:
        r = run(launch(agent, 'Reply with the single word PONG', None, tool), ws, env, timeout=args.timeout)
        if 'PONG' not in r['out']:
            result['agents'][agent] = {'status': 'could not answer a plain prompt', 'detail': r['out'].strip()[-300:]}
            print(tool, agent, 'could not answer a plain prompt', flush=True)
            return
        result['agents'][agent] = {'status': 'signed in'}
        for kind in [k for k in args.kinds if k != 'lsp' or agent in LSP_AGENTS]:
            for n in range(args.runs):
                mark.unlink(missing_ok=True)
                mcp0, lsp0 = calls('calls.log'), calls('lsp-calls.log')
                row = {'tool': tool, 'agent': agent, 'artifact': kind, 'run': n}
                for attempt, text in enumerate([PROMPTS[kind]] + ([COMMAND_FALLBACK] if kind == 'command' else [])):
                    r = run(launch(agent, text, kind, tool), ws, env, timeout=args.timeout)
                    answer = final_answer(r['out'])
                    touched = read_source_of(r['out'])
                    delivered = TOKENS[kind] in answer and not touched
                    (root / 'transcripts').mkdir(exist_ok=True)
                    (root / 'transcripts' / f'{agent}-{kind}-{n}-{attempt}.jsonl').write_text(r['out'])
                    if kind == 'mcp':
                        row['mcp_server_recorded_call'] = calls('calls.log') > mcp0
                    if kind == 'lsp':
                        row['lsp_hover_token_in_answer'] = delivered
                        delivered = calls('lsp-calls.log') > lsp0 and not touched
                    row.update(delivered=delivered, read_source=touched, answer=answer.strip()[-200:], exit=r['exit'], seconds=r['seconds'], hook_ran=mark.exists(),
                               invoked_as='slash' if kind == 'command' and attempt == 0 else 'request',
                               answer_tail=r['out'].replace(str(base), '<tmp>').strip()[-200:])
                    if delivered:
                        break
                result['rows'].append(row)
                print(tool, agent, kind, n, 'delivered' if row['delivered'] else 'NOT delivered', 'hook' if row['hook_ran'] else '-', flush=True)


base = (args.work or Path(tempfile.mkdtemp(prefix='competitors-model-'))).resolve()
base.mkdir(parents=True, exist_ok=True)
try:
    with concurrent.futures.ThreadPoolExecutor(max_workers=args.jobs) as pool:
        def safe(t):
            try:
                res = one_tool(t, base)
            except Exception as e:      # one tool failing must not lose the others' results
                res = {'tool': t, 'error': repr(e), 'rows': [], 'agents': {}}
            (base / f'{t}-result.json').write_text(json.dumps(res, indent=1, default=str))
            return res
        results = list(pool.map(safe, args.tool))
finally:
    for r in base.glob('*/repo'):
        r.chmod(0o755)
    if not args.install_only and not args.work:
        shutil.rmtree(base, ignore_errors=True)

matrix = {}
for res in results:
    for row in res['rows']:
        m = matrix.setdefault(res['tool'], {}).setdefault(row['agent'], {})
        cell = m.setdefault(row['artifact'], {'delivered': 0, 'runs': 0})
        cell['runs'] += 1
        cell['delivered'] += row['delivered']
        hook = m.setdefault('hook', {'delivered': 0, 'runs': 0})
        hook['runs'] += 1
        hook['delivered'] += row['hook_ran']
versions = {a: subprocess.run([EXE[a], '--version'], capture_output=True, text=True).stdout.strip().splitlines()[:1] for a in AGENTS if shutil.which(EXE[a])}
out = {'date': time.strftime('%Y-%m-%d'), 'platform': platform.platform(), 'agent_versions': versions,
       'tools': {'agentpack': {'version': subprocess.check_output([str(AP), '--version'], text=True).strip(), 'sha256': args.agentpack_sha256}
                 if 'agentpack' in args.tool else None, 'apm': '0.33.0', 'vercel-skills': '1.7.2', 'rulesync': '33.0.0', 'native': 'agent versions'},
       'model_inference_requested': not args.install_only, 'runs_per_cell': args.runs, 'grants': GRANTS,
       'matrix': matrix, 'results': results}
if args.install_only:
    print(json.dumps([{k: r[k] for k in ('tool', 'install_steps', 'files_written')} for r in results], indent=1)[:20000])
else:
    args.output.write_text(json.dumps(out, indent=1) + '\n')
    print(json.dumps(matrix, indent=1))
