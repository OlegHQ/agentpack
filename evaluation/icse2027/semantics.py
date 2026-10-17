#!/usr/bin/env -S uv run
# /// script
# requires-python = ">=3.11"
# dependencies = []
# ///
"""Does converted content keep its behaviour? Four probes where a conversion could change what an item does.

competitors_model.py shows that each item reaches the model. This script asks whether it still behaves as its
author wrote it, after each tool has installed it for each agent:

  args      a command that takes an argument ($ARGUMENTS); invoked by name with a random argument, the answer
            must contain the command's token joined to that argument. On Codex and Antigravity Agentpack delivers
            the command as a skill.
  sign      an always-apply rule ("end every answer with this line"); the prompt is unrelated to the rule
            ("What is 17 plus 25?"). A rule that became something loaded only when relevant fails here.
  block     a PreToolUse hook on shell commands that reads the event from stdin, records it, and blocks (exit 2)
            any command naming a probe file. The agent is asked to create that file; the probe passes when the
            file does not exist afterwards and the hook's record holds the command.
  readonly  a subagent restricted to read-only tools (tools: Read, Grep, Glob) is asked, through delegation, to
            create a file; the probe passes when the file does not exist afterwards. The weakest oracle of the
            four: a main agent that refuses to delegate and declines the task also passes.

Every probe also runs once per agent with nothing installed (tool "none"), as a control: the args and sign
tokens must not appear, and the block and readonly files must get created, which shows the agent would run
the command when nothing stops it.

    uv run semantics.py --tools-dir DIR --backup-dir DIR [--tool T ...] [--agents A ...] [--runs 3]

Logins, grants, hiding of token files outside an agent's own locations and the package-source rule are those
of competitors_model.py (owner's approval for copying logins: 2026-10-16). Shell commands need permission in
non-interactive runs, so every agent runs with its own skip-permission flag for the block and readonly probes,
for every tool alike: a hook that blocks must block even then.
"""
import argparse, concurrent.futures, hashlib, json, os, platform, re, secrets, shutil, stat, subprocess, sys, tempfile, time
from pathlib import Path

HERE = Path(__file__).resolve().parent
AGENTS = ['claude', 'codex', 'opencode', 'cursor', 'grok', 'agy']
EXE = {'claude': 'claude', 'codex': 'codex', 'opencode': 'opencode', 'cursor': 'cursor-agent', 'grok': 'grok', 'agy': 'agy'}
TOOLS = ['agentpack', 'apm', 'rulesync', 'native', 'none']
PROBES = ['args', 'sign', 'block', 'readonly']
GRANTS = {
    'claude': 'enableAllProjectMcpServers; --dangerously-skip-permissions for block and readonly',
    'codex': 'checkout trusted; --sandbox danger-full-access; --dangerously-bypass-hook-trust',
    'opencode': 'none (shell allowed by default)',
    'cursor': '--force',
    'grok': '--always-approve',
    'agy': '--dangerously-skip-permissions',
}

parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
parser.add_argument('--tools-dir', type=Path, required=True, help='the tools directory made by ecosystem.py setup')
parser.add_argument('--tool', nargs='+', default=TOOLS, choices=TOOLS)
parser.add_argument('--agentpack', type=Path, help='Agentpack binary, for the agentpack row')
parser.add_argument('--agentpack-sha256', help='sha256 the binary must have; recorded in the results')
parser.add_argument('--agents', nargs='+', default=AGENTS, choices=AGENTS)
parser.add_argument('--probes', nargs='+', default=PROBES, choices=PROBES)
parser.add_argument('--runs', type=int, default=3)
parser.add_argument('--jobs', type=int, default=4)
parser.add_argument('--timeout', type=int, default=240)
parser.add_argument('--opencode-model', default='openai/gpt-6.1-sol')
parser.add_argument('--install-only', action='store_true', help='install and list files; no login copied, no model called')
parser.add_argument('--work', type=Path, default=None)
parser.add_argument('--backup-dir', type=Path, required=True, help='where replaced credential files are kept (outside the repository)')
parser.add_argument('--output', type=Path, default=HERE / 'semantics-results.json')
args = parser.parse_args()
TD = args.tools_dir.resolve()
EXES = {'apm': TD / 'apm/bin/apm-linux-arm64/apm', 'rulesync': TD / 'node/node_modules/.bin/rulesync'}
REAL = Path.home()
if 'agentpack' in args.tool:
    if not args.agentpack or hashlib.sha256(args.agentpack.read_bytes()).hexdigest() != (args.agentpack_sha256 or '').lower():
        sys.exit('the agentpack row needs --agentpack and its --agentpack-sha256')
    AP = args.agentpack.resolve()


def write(path, text):
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(text)


def new_tokens():
    n = lambda: str(secrets.randbelow(900000) + 100000)
    return {'args': 'ARG-OK-' + n(), 'sign': 'SIGN-OK-' + n()}


BLOCK_SH = r'''#!/bin/sh
# reads the hook event from stdin, records it, and blocks any shell command that names a probe file
event=$(cat)
printf '%s\n' "$event" >> "$SEMANTICS_HOOK_LOG"
case "$event" in
  *blocked-probe*) echo "blocked by the package's policy hook" >&2; exit 2 ;;
esac
exit 0
'''


def build_repo(repo, TOKENS):
    """A marketplace repository holding the probe package as plugins/semantics-pack (a Claude Code plugin)."""
    root = repo / 'plugins/semantics-pack'
    write(root / '.claude-plugin/plugin.json', json.dumps({'name': 'semantics-pack', 'version': '1.0.0', 'description': 'Behaviour probe package'}))
    write(root / 'commands/delivery-args.md', '---\ndescription: Echo test command that takes one argument\nargument-hint: <word>\n---\n\n'
          f'Reply with exactly this line and nothing else, where WORD is the argument you were given: {TOKENS["args"]}-WORD\n\n'
          'The argument is: $ARGUMENTS\n')
    write(root / 'rules/always-sign.mdc', '---\ndescription: Answer signature\nalwaysApply: true\n---\n\n'
          f'End every answer you give with a final line that reads exactly: {TOKENS["sign"]}\n')
    write(root / 'hooks/block.sh', BLOCK_SH)
    (root / 'hooks/block.sh').chmod(0o755)
    write(root / 'hooks/hooks.json', json.dumps({'hooks': {'PreToolUse': [{'matcher': 'Bash', 'hooks': [
        {'type': 'command', 'command': 'sh "${CLAUDE_PLUGIN_ROOT}/hooks/block.sh"'}]}]}}, indent=1))
    write(root / 'agents/readonly-agent.md',
          '---\nname: readonly-agent\ndescription: Read-only reviewer. Use when asked to delegate to readonly-agent.\ntools: Read, Grep, Glob\n---\n\n'
          'You are a read-only reviewer. You may read and search files. You have no tool that writes files or runs commands.\n')
    write(repo / '.claude-plugin/marketplace.json', json.dumps({'name': 'semantics-market', 'owner': {'name': 'test'},
          'plugins': [{'name': 'semantics-pack', 'source': './plugins/semantics-pack', 'description': 'Behaviour probe package'}]}, indent=1))
    for cmd in (['init', '-q'], ['add', '-A'], ['-c', 'user.name=t', '-c', 'user.email=t@t', 'commit', '-qm', 'probe package']):
        subprocess.run(['git', *cmd], cwd=repo, check=True)
    return root


def prompt(probe, n, word):
    return {'args': f'/delivery-args {word}',
            'sign': 'What is 17 plus 25? Answer with the number.',
            'block': f'Run this shell command exactly once: touch blocked-probe-{n}.txt   Then reply with the single word DONE.',
            'readonly': f'Delegate this task to the readonly-agent subagent and do not do it yourself: create a file named '
                        f'readonly-probe-{n}.txt containing OK. Then output what the subagent reported.'}[probe]


ARGS_FALLBACK = 'Run the delivery-args command with the argument {word} and output only what it tells you to output.'


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


def install(tool, repo, plugin, home, ws):
    env = clean_env(home, {'AGENTPACK_STAGING_ROOT': str(home.parent / 'staging')} if tool == 'agentpack' else None)
    steps = []
    if tool == 'agentpack':
        steps += [[AP, '--no-progress', 'init'], [AP, '--no-progress', 'add', str(plugin)], [AP, '--no-progress', 'lock'],
                  [AP, '--no-progress', 'sync']]
    elif tool == 'apm':
        steps.append([EXES['apm'], 'install', str(plugin), '-t', 'claude,codex,cursor,opencode,grok-build,antigravity', '--trust-transitive-mcp'])
    elif tool == 'rulesync':
        x = EXES['rulesync']
        steps += [[x, 'init'], [x, 'import', '--targets', 'claudecode-plugin', '--features', '*', '--output-root', str(plugin)],
                  [x, 'generate', '--targets', 'claudecode,codexcli,opencode,cursor,grokcli,antigravity-cli', '--features', '*']]
    elif tool == 'native':
        steps += [['claude', 'plugin', 'marketplace', 'add', str(repo)],
                  ['claude', 'plugin', 'install', 'semantics-pack@semantics-market', '--scope', 'project'],
                  ['codex', 'plugin', 'marketplace', 'add', str(repo)],
                  ['codex', 'plugin', 'add', 'semantics-pack@semantics-market'],
                  ['grok', 'plugin', 'install', str(plugin), '--trust'],
                  ['cursor-agent', 'plugin', 'marketplace', 'add', str(repo)],
                  ['agy', 'plugin', 'install', str(plugin)]]
    out = []
    for s in steps:
        r = run(s, ws, env, timeout=600)
        out.append({'argv': [a.replace(str(home.parent), '<tmp>') for a in r['argv']], 'exit': r['exit'],
                    'tail': r['out'].replace(str(home.parent), '<tmp>').strip()[-600:]})
    return out


def launch(agent, text, probe, tool):
    shell = probe in ('block', 'readonly')
    if agent == 'claude':
        argv = ['claude', '-p', text, '--output-format', 'stream-json', '--verbose', *(['--dangerously-skip-permissions'] if shell else [])]
    elif agent == 'codex':
        argv = ['codex', 'exec', '--json', '--skip-git-repo-check', '--sandbox', 'danger-full-access', '--dangerously-bypass-hook-trust', text]
    elif agent == 'opencode':
        argv = (['opencode', 'run', '--format', 'json', '-m', args.opencode_model, '--command', 'delivery-args', text.split(' ', 1)[1]]
                if probe == 'args' and text.startswith('/') else ['opencode', 'run', '--format', 'json', '-m', args.opencode_model, text])
    elif agent == 'cursor':
        argv = ['cursor-agent', '-p', text, '--output-format', 'stream-json', '--force']
    elif agent == 'grok':
        argv = ['grok', '-p', text, '--output-format', 'streaming-messages-json', '--always-approve']
    else:
        argv = ['agy', '-p', text, '--output-format', 'stream-json', '--dangerously-skip-permissions']
    if tool == 'agentpack':
        argv = [AP, 'agent' if agent == 'cursor' else argv[0], *argv[1:]]
    return argv


def one_tool(tool, base):
    root = base / tool
    repo, home, ws = root / 'repo', root / 'home', root / 'checkout'
    for d in (repo, home, ws):
        d.mkdir(parents=True)
    TOKENS = new_tokens()
    plugin = build_repo(repo, TOKENS)
    subprocess.run(['git', 'init', '-q', str(ws)], check=True)
    (ws / 'README.md').write_text('Benign project\n')
    before = set(written(root))
    copied = {} if args.install_only else sign_in(home, ws)
    steps = install(tool, repo, plugin, home, ws) if tool != 'none' else []
    files = [f for f in written(root) if f not in before and not f.startswith('repo/')]
    shutil.rmtree(repo / '.git', ignore_errors=True)
    result = {'tool': tool, 'tokens': TOKENS, 'install_steps': steps, 'files_written': files, 'rows': [], 'agents': {}}
    if args.install_only:
        return result
    sign_in(home, ws)
    result['logins_copied'] = sorted(copied)
    log = root / 'hook-events.log'
    env = clean_env(home, {'SEMANTICS_HOOK_LOG': str(log)})
    if tool == 'agentpack':
        env['AGENTPACK_STAGING_ROOT'] = str(root / 'staging')
        env['PATH'] = f"{AP.parent}{os.pathsep}{env['PATH']}"
    notes = []
    for agent in args.agents:
        hidden = hide_for(agent, root, TOKENS, tool)
        try:
            agent_runs(tool, agent, root, ws, env, TOKENS, result, log, base)
        finally:
            restore(hidden)
        result['agents'][agent]['files_hidden'] = len(hidden)
        notes += hand_back(home, copied, args.backup_dir)
    result['credential_notes'] = notes
    return result


def agent_runs(tool, agent, root, ws, env, TOKENS, result, log, base):
    source = str(root / 'repo')
    r = run(launch(agent, 'Reply with the single word PONG', None, tool), ws, env, timeout=args.timeout)
    if 'PONG' not in r['out']:
        result['agents'][agent] = {'status': 'could not answer a plain prompt', 'detail': r['out'].strip()[-300:]}
        print(tool, agent, 'could not answer a plain prompt', flush=True)
        return
    result['agents'][agent] = {'status': 'signed in'}
    runs = 1 if tool == 'none' else args.runs
    for probe in args.probes:
        for n in range(runs):
            word = 'w' + secrets.token_hex(3)
            probe_file = ws / f'{"blocked" if probe == "block" else "readonly"}-probe-{n}.txt'
            probe_file.unlink(missing_ok=True)
            log.unlink(missing_ok=True)
            row = {'tool': tool, 'agent': agent, 'probe': probe, 'run': n}
            texts = [prompt(probe, n, word)] + ([ARGS_FALLBACK.format(word=word)] if probe == 'args' else [])
            for attempt, text in enumerate(texts):
                r = run(launch(agent, text, probe, tool), ws, env, timeout=args.timeout)
                answer = final_answer(r['out'])
                touched = read_source(r['out'], source, tool, agent)
                (root / 'transcripts').mkdir(exist_ok=True)
                (root / 'transcripts' / f'{agent}-{probe}-{n}-{attempt}.jsonl').write_text(r['out'])
                hook_saw = log.is_file() and probe_file.name in log.read_text(errors='replace')
                created = probe_file.exists()
                if probe == 'args':
                    passed = f'{TOKENS["args"]}-{word}' in answer
                    row['token_without_argument'] = TOKENS['args'] in answer and not passed
                elif probe == 'sign':
                    passed = TOKENS['sign'] in answer
                elif probe == 'block':
                    passed = not created and hook_saw
                else:
                    passed = not created
                passed = passed and not touched
                row.update(passed=passed, read_source=touched, file_created=created if probe in ('block', 'readonly') else None,
                           hook_saw_command=hook_saw if probe == 'block' else None,
                           invoked_as=('slash' if attempt == 0 else 'request') if probe == 'args' else None,
                           answer=answer.strip()[-200:], exit=r['exit'], seconds=r['seconds'])
                if passed or probe != 'args':
                    break
            result['rows'].append(row)
            print(tool, agent, probe, n, 'PASS' if row['passed'] else 'fail',
                  ('created' if row['file_created'] else '') if probe in ('block', 'readonly') else '', flush=True)


base = (args.work or Path(tempfile.mkdtemp(prefix='semantics-'))).resolve()
base.mkdir(parents=True, exist_ok=True)
try:
    with concurrent.futures.ThreadPoolExecutor(max_workers=args.jobs) as pool:
        def safe(t):
            try:
                res = one_tool(t, base)
            except Exception as e:
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
        cell = matrix.setdefault(res['tool'], {}).setdefault(row['agent'], {}).setdefault(row['probe'], {'passed': 0, 'runs': 0})
        cell['runs'] += 1
        cell['passed'] += row['passed']
versions = {a: subprocess.run([EXE[a], '--version'], capture_output=True, text=True).stdout.strip().splitlines()[:1] for a in AGENTS if shutil.which(EXE[a])}
out = {'date': time.strftime('%Y-%m-%d'), 'platform': platform.platform(), 'agent_versions': versions,
       'tools': {'agentpack': {'version': subprocess.check_output([str(AP), '--version'], text=True).strip(), 'sha256': args.agentpack_sha256}
                 if 'agentpack' in args.tool else None, 'apm': '0.33.0', 'rulesync': '33.0.0', 'native': 'agent versions', 'none': 'nothing installed'},
       'model_inference_requested': not args.install_only, 'runs_per_cell': args.runs, 'grants': GRANTS,
       'matrix': matrix, 'results': results}
if args.install_only:
    print(json.dumps([{k: r[k] for k in ('tool', 'install_steps', 'files_written')} for r in results], indent=1)[:20000])
else:
    args.output.write_text(json.dumps(out, indent=1) + '\n')
    print(json.dumps(matrix, indent=1))
