#!/usr/bin/env -S uv run
# /// script
# requires-python = ">=3.11"
# dependencies = []
# ///
"""After `agentpack eject`, does each agent's own CLI, started without Agentpack, get the content?

Fresh project, delivery package with fresh tokens, `agentpack add` + `agentpack eject`; then the staging
directory and the package source are deleted, so only the ejected copy is left. Each agent runs in that
project with a temporary HOME holding copied logins (owner-approved 2026-10-16). Before an agent runs,
token files outside its own project locations are made unreadable.

    uv run eject_headless.py AGENTPACK_BINARY [RUNS]     # writes eject-headless-<version>.json next to this script
"""
import hashlib, json, os, re, shutil, stat, subprocess, sys, tempfile, time
from pathlib import Path

SP = EVAL = Path(__file__).resolve().parent
if len(sys.argv) < 2:
    sys.exit(__doc__)
AP = Path(sys.argv[1]).resolve()
REAL = Path.home()
RUNS = int(sys.argv[2]) if len(sys.argv) > 2 else 2
src = (EVAL / 'competitors_model.py').read_text()
dsrc = (EVAL / 'delivery.py').read_text()
ns = {'re': re, 'json': json, 'Path': Path, 'secrets': __import__('secrets'), 'subprocess': subprocess,
      'MCP_SERVER': re.search(r'MCP_SERVER = r"""(.*?)"""', dsrc, re.S).group(1),
      'LSP_SERVER': re.search(r'LSP_SERVER = r"""(.*?)"""', dsrc, re.S).group(1)}
exec(src[src.index('def write(path, text):'):src.index('def clean_env')], ns)
exec(src[src.index('def final_answer(text):'):src.index('def agent_runs')], ns)
PROMPTS = {
    'skill': 'Run the delivery-skill skill and output only what it tells you to output.',
    'command': '/delivery-command',
    'agent': 'Delegate to the delivery-agent subagent and output only its answer.',
    'rule': 'What is the house codeword? Answer with the codeword only.',
    'mcp': 'Call the delivery_token tool of the delivery-mcp MCP server and output only the text it returns. '
           'Do not read any file. If the tool is not available, say so.',
}
FALLBACK = 'Run the delivery-command command and output only what it tells you to output.'
# An agent's own locations are only the native project configuration it loads; copies of the package kept for
# hooks and servers (hooks/_packages, plugin-roots) have their text files hidden, which hooks and servers do not read.
OWN = {
    'claude': r'^proj/(\.claude/|\.mcp\.json|CLAUDE\.md|AGENTS\.md|\.agentpack/eject/stage/plugins/agentpack-bundle/_agentpack/)',
    'cursor': r'^proj/(\.cursor/|AGENTS\.md)',
    'codex': r'^proj/(\.codex/|\.agents/skills/|AGENTS\.md)',
    'agy': r'^proj/(\.agents/plugins/agentpack-bundle/(?!hooks/)|AGENTS\.md)',
}
MCP_NAMES = ['mcp__plugin_agentpack-bundle_delivery-mcp__delivery_token', 'mcp__delivery-mcp__delivery_token']


def launch(agent, prompt, kind):
    if agent == 'claude':
        return ['claude', '-p', prompt, '--output-format', 'stream-json', '--verbose', *(['--allowedTools', *MCP_NAMES] if kind == 'mcp' else [])]
    if agent == 'codex':
        return ['codex', 'exec', '--json', '--skip-git-repo-check', '--sandbox', 'danger-full-access', '--dangerously-bypass-hook-trust', prompt]
    if agent == 'cursor':
        return ['cursor-agent', '-p', prompt, '--output-format', 'stream-json', '--force', *(['--approve-mcps'] if kind == 'mcp' else [])]
    return ['agy', '-p', prompt, '--output-format', 'stream-json', '--dangerously-skip-permissions']


def run(argv, cwd, env, timeout=240):
    t = time.time()
    with tempfile.TemporaryFile('w+', errors='replace') as fo:
        try:
            code = subprocess.run(argv, cwd=cwd, env=env, stdout=fo, stderr=subprocess.STDOUT, timeout=timeout, stdin=subprocess.DEVNULL).returncode
        except subprocess.TimeoutExpired:
            code = 'timeout'
        fo.seek(0)
        return fo.read(), code, round(time.time() - t, 1)


base = Path(tempfile.mkdtemp(prefix='eject-headless-'))
repo, proj, home, staging = base / 'repo', base / 'proj', base / 'home', base / 'staging'
for d in (repo, proj, home):
    d.mkdir()
TOKENS = ns['new_tokens']()
ns['build_repo'](repo, TOKENS)
subprocess.run(['git', 'init', '-q', str(proj)], check=True)
(proj / 'README.md').write_text('Benign project\n')
apenv = {**os.environ, 'AGENTPACK_STAGING_ROOT': str(staging)}
for cmd in (['init'], ['add', str(repo / 'plugins/delivery-pack')], ['eject']):
    p = subprocess.run([str(AP), '--no-progress', *cmd], cwd=proj, env=apenv, capture_output=True, text=True)
    print(' '.join(cmd), p.returncode, (p.stdout + p.stderr).strip().splitlines()[-1:])
    assert p.returncode == 0, p.stderr
# only the ejected copy is left: no staging, no package source
shutil.rmtree(staging)
shutil.rmtree(repo)
for d in ('codex-generations', 'grok-homes'):
    pass
# logins (copied as competitors_model.sign_in does)
creds = [(REAL / '.claude/.credentials.json', '.claude/.credentials.json'), (REAL / '.codex/auth.json', '.codex/auth.json'),
         (REAL / '.config/cursor/auth.json', '.config/cursor/auth.json'),
         (REAL / '.gemini/antigravity-cli/antigravity-oauth-token', '.gemini/antigravity-cli/antigravity-oauth-token')]
copied = {}
for s, rel in creds:
    data = s.read_bytes()
    if rel == '.claude/.credentials.json':
        data = json.dumps({'claudeAiOauth': json.loads(data)['claudeAiOauth']}).encode()
    (home / rel).parent.mkdir(parents=True, exist_ok=True)
    (home / rel).write_bytes(data)
    (home / rel).chmod(0o600)
    copied[rel] = (s, hashlib.sha256(data).hexdigest(), hashlib.sha256(s.read_bytes()).hexdigest())
shutil.copy(REAL / '.config/cursor/cli-config.json', home / '.config/cursor/cli-config.json')
r = json.loads((REAL / '.claude.json').read_text())
(home / '.claude.json').write_text(json.dumps({k: r[k] for k in ('oauthAccount', 'userID', 'hasCompletedOnboarding') if k in r}))
(home / '.codex/config.toml').write_text(f'cli_auth_credentials_store = "file"\n\n[projects."{proj}"]\ntrust_level = "trusted"\n')
mark = base / 'hook-mark.txt'
env = {'PATH': os.environ['PATH'], 'HOME': str(home), 'XDG_CONFIG_HOME': str(home / '.config'), 'XDG_DATA_HOME': str(home / '.local/share'),
       'XDG_CACHE_HOME': str(home / '.cache'), 'XDG_STATE_HOME': str(home / '.local/state'), 'LANG': 'C.UTF-8', 'TERM': 'dumb',
       'NO_COLOR': '1', 'DISABLE_AUTOUPDATER': '1', 'AGY_CLI_DISABLE_AUTO_UPDATE': '1', 'DELIVERY_HOOK_MARK': str(mark)}


def hide(agent):
    own, hidden = re.compile(OWN[agent]), []
    for dirpath, dirnames, filenames in os.walk(base):
        dirnames[:] = [d for d in dirnames if d not in ('.git', 'transcripts')]
        for name in filenames:
            f = Path(dirpath) / name
            rel = str(f.relative_to(base))
            if f.is_symlink() or own.search(rel) or rel.startswith('home/'):
                continue
            try:
                data = f.read_bytes()
            except OSError:
                continue
            if any(t.encode() in data for t in TOKENS.values()):
                hidden.append((f, stat.S_IMODE(f.stat().st_mode)))
                f.chmod(0)
    return hidden


matrix, rows = {}, []
(base / 'transcripts').mkdir()
calls = lambda: sum(len(f.read_text().splitlines()) for f in proj.rglob('calls.log'))
for agent in ('claude', 'cursor', 'codex', 'agy'):
    hidden = hide(agent)
    try:
        out, code, _ = run(launch(agent, 'Reply with the single word PONG', None), proj, env)
        if 'PONG' not in out:
            matrix[agent] = {'status': 'could not answer a plain prompt', 'detail': out[-300:]}
            continue
        for kind in PROMPTS:
            for n in range(RUNS):
                mark.unlink(missing_ok=True)
                c0 = calls()
                row = {'agent': agent, 'kind': kind, 'run': n}
                for attempt, text in enumerate([PROMPTS[kind]] + ([FALLBACK] if kind == 'command' else [])):
                    out, code, secs = run(launch(agent, text, kind), proj, env)
                    (base / 'transcripts' / f'{agent}-{kind}-{n}-{attempt}.jsonl').write_text(out)
                    answer = ns['final_answer'](out)
                    row.update(delivered=TOKENS[kind] in answer, hook_ran=mark.exists(), exit=code, seconds=secs,
                               invoked_as='slash' if kind == 'command' and attempt == 0 else 'request', answer=answer.strip()[-160:])
                    if kind == 'mcp':
                        row['mcp_server_recorded_call'] = calls() > c0
                    if row['delivered']:
                        break
                rows.append(row)
                cell = matrix.setdefault(agent, {}).setdefault(kind, {'delivered': 0, 'runs': 0})
                cell['runs'] += 1
                cell['delivered'] += row['delivered']
                hk = matrix[agent].setdefault('hook', {'delivered': 0, 'runs': 0})
                hk['runs'] += 1
                hk['delivered'] += row['hook_ran']
                print(agent, kind, n, 'delivered' if row['delivered'] else 'NOT', 'hook' if row['hook_ran'] else '-', repr(row['answer'][:60]), flush=True)
    finally:
        for f, mode in hidden:
            f.chmod(mode)
# hand refreshed logins back (as competitors_model.hand_back)
notes = []
for rel, (s, sha_copy, sha_src) in copied.items():
    dest = home / rel
    now = hashlib.sha256(dest.read_bytes()).hexdigest()
    if now == sha_copy:
        continue
    if hashlib.sha256(s.read_bytes()).hexdigest() != sha_src:
        notes.append(f'{rel}: refreshed here, but the source changed meanwhile; not copied back')
        continue
    data = dest.read_bytes()
    if rel == '.claude/.credentials.json':
        merged = json.loads(s.read_text())
        merged['claudeAiOauth'] = json.loads(data)['claudeAiOauth']
        data = json.dumps(merged).encode()
    backup = SP / 'cred-backup'
    backup.mkdir(exist_ok=True)
    shutil.copy2(s, backup / (rel.replace('/', '_') + time.strftime('.%Y%m%dT%H%M%S')))
    tmp = s.with_name(s.name + '.tmp-eject')
    tmp.write_bytes(data)
    tmp.chmod(0o600)
    os.replace(tmp, s)
    notes.append(f'{rel}: refreshed during the run and copied back')
versions = {a: subprocess.run([b, '--version'], capture_output=True, text=True).stdout.strip().splitlines()[:1]
            for a, b in (('claude', 'claude'), ('codex', 'codex'), ('cursor', 'cursor-agent'), ('agy', 'agy'))}
result = {'date': time.strftime('%Y-%m-%d'), 'agentpack': subprocess.check_output([str(AP), '--version'], text=True).strip(),
          'agentpack_sha256': hashlib.sha256(AP.read_bytes()).hexdigest(), 'agent_versions': versions, 'runs_per_cell': RUNS,
          'setup': 'agentpack add + agentpack eject; staging and package source then deleted; agents started without agentpack, '
                   'temporary HOME with copied logins; token files outside the agent\'s own project locations unreadable',
          'tokens': TOKENS, 'matrix': matrix, 'rows': rows, 'credential_notes': notes, 'work': str(base)}
(SP / f"eject-headless-{result['agentpack'].split()[-1]}.json").write_text(json.dumps(result, indent=1) + '\n')
print(json.dumps(matrix, indent=1))
print(notes)
