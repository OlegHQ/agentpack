#!/usr/bin/env python3
"""Same-named slash commands and subagents: what the agent lists, and what Agentpack reports.

The delivery package's command and subagent are pinned. Without Agentpack, the package is
installed as a Claude Code plugin and a project command or subagent with the same name is added;
the session listing is read 20 times (empty user home, no model call). With Agentpack, the same
package is restored and judged by a contract with one observed requirement per artifact, for
Claude Code and for Codex. Agentpack renders commands and subagents as skills for Codex, so
there the project copy is a skill in .agents/skills. The expected verdict follows from the
native listing by the rule used in collision.py.
"""
import argparse, hashlib, json, os, platform, shutil, subprocess, sys, tempfile, time
from pathlib import Path

parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
parser.add_argument('--agentpack', type=Path, required=True)
parser.add_argument('--repeats', type=int, default=20)
parser.add_argument('--output', type=Path, default=Path(__file__).resolve().parent / 'kinds-results.json')
args = parser.parse_args()
AP = args.agentpack.resolve()
KINDS = {'command': 'delivery-command', 'agent': 'delivery-agent'}
VERSIONS = {'claude': '2.1.296 (Claude Code)', 'codex': 'codex-cli 0.159.3'}
for name, want in VERSIONS.items():
    got = subprocess.check_output([shutil.which(name), '--version'], text=True).strip()
    if got != want:
        parser.error(f'{name}: recorded with {want}, found {got}')


def write(path, text):
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(text)


def package(root):
    write(root / '.claude-plugin/plugin.json', '{"name": "team", "version": "1.0.0"}')
    write(root / 'commands/delivery-command.md', '---\ndescription: Pinned command\n---\n\nReply PINNED.\n')
    write(root / 'agents/delivery-agent.md', '---\nname: delivery-agent\ndescription: Pinned subagent\n---\n\nReply PINNED.\n')


def project_copy(ws, kind, agent):
    """Put a same-named artifact where the given agent looks for project content."""
    if agent == 'codex':
        write(ws / '.agents/skills' / KINDS[kind] / 'SKILL.md',
              f'---\nname: {KINDS[kind]}\ndescription: Project copy.\n---\n\nReply PROJECT.\n')
    elif kind == 'command':
        write(ws / '.claude/commands/delivery-command.md', '---\ndescription: Project copy\n---\n\nReply PROJECT.\n')
    else:
        write(ws / '.claude/agents/delivery-agent.md', '---\nname: delivery-agent\ndescription: Project copy\n---\n\nReply PROJECT.\n')


def clean_env(home):
    return {'PATH': os.environ['PATH'], 'HOME': str(home), 'XDG_CONFIG_HOME': str(home / 'config'),
            'XDG_DATA_HOME': str(home / 'data'), 'XDG_CACHE_HOME': str(home / 'cache'),
            'XDG_STATE_HOME': str(home / 'state')}


def claude_listing(ws, plugin, base):
    home = Path(tempfile.mkdtemp(prefix='native-', dir=base))
    env = {**clean_env(home), 'CLAUDE_CONFIG_DIR': str(home / 'claude')}
    try:
        p = subprocess.run(['claude', '-p', 'listing', '--output-format', 'stream-json', '--verbose', '--plugin-dir',
                            str(plugin), '--strict-mcp-config', '--settings', '{"disableAllHooks":true}'],
                           cwd=ws, env=env, capture_output=True, text=True, timeout=90)
        init = json.loads(p.stdout.splitlines()[0])
        assert init['subtype'] == 'init' and init['apiKeySource'] == 'none'
        out = {}
        for kind, field in (('command', 'slash_commands'), ('agent', 'agents')):
            name = KINDS[kind]
            out[kind] = sorted('pinned' if x == f'team:{name}' else 'project' for x in init[field] if x in (name, f'team:{name}'))
        return out
    finally:
        shutil.rmtree(home, ignore_errors=True)


rows = []
with tempfile.TemporaryDirectory(prefix='agentpack-kinds-') as td:
    base = Path(td).resolve()
    pack = base / 'team-pack'
    package(pack)
    home = base / 'home'
    home.mkdir()
    env = {**clean_env(home), 'USERPROFILE': str(home), 'AGENTPACK_HOME': str(base / 'managed'),
           'AGENTPACK_STAGING_ROOT': str(base / 'staging'), 'CLAUDE_CODE_PATH': shutil.which('claude'),
           'CODEX_PATH': shutil.which('codex'), 'CI': '1'}
    definition, ws, native_ws = base / 'definition', base / 'checkout', base / 'native-checkout'
    for checkout in (ws, native_ws):
        checkout.mkdir()
        subprocess.run(['git', 'init', '-q', str(checkout)], check=True)
        (checkout / 'README.md').write_text('Benign project\n')

    def ap(*cmd):
        return subprocess.run([str(c) for c in cmd], cwd=ws, env=env, capture_output=True, text=True, timeout=300)

    sel = [AP, '--env', definition, '--workspace', ws]
    assert ap(AP, 'env', 'init', '--dir', definition, '--name', 'kinds').returncode == 0
    assert ap(*sel, 'add', pack, '--no-sync').returncode == 0
    (definition / 'contract.json').write_text(json.dumps({'schema_version': 1, 'unknown_required': 'fail', 'requirements': [
        {'id': f'observed-winner:{kind}', 'selector': {'category': kind, 'name': name, 'property': 'winner',
                                                       'scope': 'workspace_session'},
         'predicate': 'source_allowed', 'expected': 'package', 'minimum_evidence': 'observed'}
        for kind, name in KINDS.items()]}, indent=1))
    lock = None
    for clash in (None, 'command', 'agent'):
        for agent in VERSIONS:
            for checkout in (ws, native_ws):
                for d in ('.claude', '.agents'):
                    shutil.rmtree(checkout / d, ignore_errors=True)
                if clash:
                    project_copy(checkout, clash, agent)
            assert ap(*sel, 'env', 'restore').returncode == 0
            lock = lock or hashlib.sha256((definition / 'pack.lock').read_bytes()).hexdigest()
            native = None
            if agent == 'claude':
                runs = [claude_listing(native_ws, pack, base) for _ in range(args.repeats)]
                native = {kind: {'+'.join(r[kind]) or 'none': sum(x[kind] == r[kind] for x in runs) for r in runs} for kind in KINDS}
            probe = ap(*sel, 'probe', '--agent', agent, '--json')
            pre = ap(*sel, 'preflight', '--agent', agent, '--receipt', 'latest', '--json')
            results = {r['requirement_id']: r['result'] for r in json.loads(pre.stdout).get('contract_results', [])}
            for kind in KINDS:
                # The clash concerns one kind; the other must stay satisfied.
                want = 'violated' if clash == kind else 'satisfied'
                if native is not None:
                    want = 'satisfied' if set(native[kind]) == {'pinned'} else 'violated'
                got = results.get(f'observed-winner:{kind}')
                rows.append({'same_named_project': clash or 'none', 'agent': agent, 'kind': kind,
                             'native_listing': native[kind] if native else 'rendered as a skill; see collision.py',
                             'expected': want, 'observed_winner': got, 'as_expected': got == want,
                             'probe_exit': probe.returncode, 'preflight_exit': pre.returncode,
                             'lock_unchanged': hashlib.sha256((definition / 'pack.lock').read_bytes()).hexdigest() == lock})
                print(f"{clash or 'none':8} {agent:7} {kind:8} native={json.dumps(rows[-1]['native_listing'])[:40]:40} "
                      f"observed={got!s:10} {'' if got == want else 'MISMATCH'}", flush=True)

summary = {'cells': len(rows), 'as_expected': sum(r['as_expected'] for r in rows),
           'lock_unchanged': all(r['lock_unchanged'] for r in rows)}
args.output.write_text(json.dumps({'date': time.strftime('%Y-%m-%d'), 'platform': platform.platform(), 'versions': VERSIONS,
                                   'agentpack_version': subprocess.check_output([AP, '--version'], text=True).strip(),
                                   'model_inference_requested': False, 'summary': summary, 'rows': rows}, indent=1) + '\n')
print(json.dumps(summary))
sys.exit(0 if summary['as_expected'] == len(rows) else 1)
