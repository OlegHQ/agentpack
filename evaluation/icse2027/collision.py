#!/usr/bin/env python3
"""What happens when a checkout carries a skill with the same name as a pinned one?

Part A, without Agentpack. The pinned skill is fetched with git and installed the way each
agent documents: a plugin directory for Claude Code, CODEX_HOME/skills for Codex, the
configuration directory for OpenCode. A project skill with the same name is then placed in
one project root at a time, and the agent is asked what a session in the checkout lists
(empty user home, no model call). Where the agent's output carries skill descriptions
(Codex, OpenCode) the two copies are told apart by their description text, not by path.

Part B, with Agentpack. The same pins are restored by Agentpack and judged by a contract with
two requirements: the planned winner (generated evidence) and the observed winner (from
Agentpack's probe). The expected observed result follows from part A by one rule, stated in
`expected_observed`. Part B also checks that a receipt taken in the clean checkout stops
counting as soon as the project copy appears.
"""
import argparse, hashlib, json, os, platform, re, shutil, subprocess, sys, tempfile, time
from pathlib import Path

SKILLS = ['webapp-testing', 'mcp-builder', 'internal-comms']
REPO, PIN = 'anthropics/skills', 'dbd4588f9e1033efb41dad4bef2f7947c8993d44'
LOCATIONS = [None, '.claude/skills', '.agents/skills', '.codex/skills', '.opencode/skills']
REPEATS = {'claude': 100, 'codex': 100, 'opencode': 100}
VERSIONS = {'claude': '2.1.296 (Claude Code)', 'codex': 'codex-cli 0.159.3', 'opencode': '1.17.11'}
PROJECT_MARK = 'PROJECT-COPY of the testing skill'

parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
parser.add_argument('--agentpack', type=Path, required=True)
parser.add_argument('--repeats', type=int, default=100, help='native listings per cell and agent')
parser.add_argument('--output', type=Path, default=Path(__file__).resolve().parent / 'collision-results.json')
args = parser.parse_args()
REPEATS = {name: args.repeats for name in REPEATS}
AP = args.agentpack.resolve()
EXE = {}
for name, want in VERSIONS.items():
    path = shutil.which(name)
    got = subprocess.check_output([path, '--version'], text=True).strip() if path else 'not installed'
    if not path or got != want:
        parser.error(f'{name}: recorded with {want}, found {got}')
    EXE[name] = path


def sha(path):
    return hashlib.sha256(Path(path).read_bytes()).hexdigest()


def clean_env(home):
    return {'PATH': os.environ['PATH'], 'HOME': str(home), 'XDG_CONFIG_HOME': str(home / 'config'),
            'XDG_DATA_HOME': str(home / 'data'), 'XDG_CACHE_HOME': str(home / 'cache'),
            'XDG_STATE_HOME': str(home / 'state'), 'GIT_TERMINAL_PROMPT': '0'}


def native_listing(agent, ws, pinned_root, marks, base):
    """For every skill, the copies one session lists under its name: 'pinned', 'project', or both."""
    home = Path(tempfile.mkdtemp(prefix=f'native-{agent}-', dir=base))
    env = clean_env(home)
    try:
        if agent == 'claude':
            plugin = home / 'team-plugin'
            (plugin / '.claude-plugin').mkdir(parents=True)
            (plugin / '.claude-plugin/plugin.json').write_text('{"name": "team", "version": "1.0.0"}')
            for skill in SKILLS:
                shutil.copytree(pinned_root / skill, plugin / 'skills' / skill)
            env['CLAUDE_CONFIG_DIR'] = str(home / 'claude')
            p = subprocess.run([EXE[agent], '-p', 'catalog', '--output-format', 'stream-json', '--verbose',
                                '--plugin-dir', str(plugin), '--strict-mcp-config', '--settings',
                                '{"disableAllHooks":true}'], cwd=ws, env=env, capture_output=True, text=True, timeout=90)
            init = json.loads(p.stdout.splitlines()[0])
            assert init['subtype'] == 'init' and init['apiKeySource'] == 'none'
            # The init event carries names only: a plugin skill is namespaced, a project skill is bare.
            return {skill: sorted({'pinned' if s == f'team:{skill}' else 'project' for s in init['skills']
                                   if s in (skill, f'team:{skill}')}) for skill in SKILLS}
        if agent == 'codex':
            for skill in SKILLS:
                shutil.copytree(pinned_root / skill, home / 'codex/skills' / skill)
            env['CODEX_HOME'] = str(home / 'codex')
            p = subprocess.run([EXE[agent], '--no-daemon', '-c', 'cli_auth_credentials_store="file"', '-c',
                                'cloud_skill_enabled=false', 'debug', 'prompt-input'], cwd=ws, env=env,
                               capture_output=True, text=True, timeout=90)
            text = json.dumps(json.loads(p.stdout)).replace('\\n', '\n').splitlines()
            out = {}
            for skill in SKILLS:
                lines = [line for line in text if line.startswith(f'- {skill}: ')]
                out[skill] = sorted('project' if PROJECT_MARK in line else 'pinned' for line in lines
                                    if PROJECT_MARK in line or marks[skill] in line)
            return out
        for skill in SKILLS:
            shutil.copytree(pinned_root / skill, home / 'opencode-config/skills' / skill)
        env['OPENCODE_CONFIG_DIR'] = str(home / 'opencode-config')
        p = subprocess.run([EXE[agent], 'debug', 'skill', '--pure'], cwd=ws, env=env, capture_output=True,
                           text=True, timeout=120)
        listed = json.loads(p.stdout[p.stdout.index('['):])
        return {skill: sorted('project' if PROJECT_MARK in s['description'] else 'pinned'
                              for s in listed if s['name'] == skill) for skill in SKILLS}
    finally:
        shutil.rmtree(home, ignore_errors=True)


def expected_observed(outcomes):
    """Satisfied exactly when every run lists the pinned copy and nothing else under the name."""
    return 'satisfied' if set(outcomes) == {'pinned'} else 'violated'


with tempfile.TemporaryDirectory(prefix='agentpack-collision-') as td:
    base = Path(td).resolve()
    upstream = base / 'upstream'
    subprocess.run(['git', 'clone', '-q', f'https://github.com/{REPO}.git', str(upstream)], check=True)
    subprocess.run(['git', '-C', str(upstream), 'checkout', '-q', PIN], check=True)
    pinned_root = upstream / 'skills'
    marks = {}
    for skill in SKILLS:
        description = re.search(r'^description:\s*(.+)$', (pinned_root / skill / 'SKILL.md').read_text(), re.M).group(1)
        marks[skill] = description.strip().strip('"\'')[:40]

    home = base / 'home'
    home.mkdir()
    env = {**clean_env(home), 'USERPROFILE': str(home), 'AGENTPACK_HOME': str(base / 'managed'),
           'AGENTPACK_STAGING_ROOT': str(base / 'staging'), 'CLAUDE_CODE_PATH': EXE['claude'],
           'CODEX_PATH': EXE['codex'], 'OPENCODE_PATH': EXE['opencode'], 'CI': '1', 'DISABLE_TELEMETRY': '1'}
    # Part A uses its own checkout, which no Agentpack command ever touches.
    definition, ws, native_ws = base / 'definition', base / 'checkout', base / 'native-checkout'
    for checkout in (ws, native_ws):
        checkout.mkdir()
        subprocess.run(['git', 'init', '-q', str(checkout)], check=True)
        (checkout / 'README.md').write_text('Benign project\n')

    def ap(*cmd):
        return subprocess.run([str(c) for c in cmd], cwd=ws, env=env, capture_output=True, text=True, timeout=300)

    def preflight(agent, *flags):
        p = ap(*sel, 'preflight', '--agent', agent, '--strict-external', '--json', *flags)
        report = json.loads(p.stdout)
        return p.returncode, report, {r['requirement_id']: r['result'] for r in report.get('contract_results', [])}

    def set_project_copy(location, checkout=None):
        checkout = checkout or ws
        for d in ('.claude', '.agents', '.codex', '.opencode'):
            shutil.rmtree(checkout / d, ignore_errors=True)
        for skill in SKILLS if location else []:
            copy = checkout / location / skill / 'SKILL.md'
            copy.parent.mkdir(parents=True)
            copy.write_text(f'---\nname: {skill}\ndescription: {PROJECT_MARK} {skill}.\n---\n\nProject version.\n')

    sel = [AP, '--env', definition, '--workspace', ws]
    assert ap(AP, 'env', 'init', '--dir', definition, '--name', 'collision').returncode == 0
    for skill in SKILLS:
        assert ap(*sel, 'add', f'{REPO}/skills/{skill}@{PIN}', '--no-sync').returncode == 0
    requirements = []
    for skill in SKILLS:
        requirements += [
            {'id': f'planned-winner:{skill}', 'selector': {'category': 'skill', 'name': skill, 'property': 'winner'},
             'predicate': 'source_allowed', 'expected': 'package', 'minimum_evidence': 'generated'},
            {'id': f'observed-winner:{skill}',
             'selector': {'category': 'skill', 'name': skill, 'property': 'winner', 'scope': 'workspace_session'},
             'predicate': 'source_allowed', 'expected': 'package', 'minimum_evidence': 'observed'}]
    (definition / 'contract.json').write_text(json.dumps(
        {'schema_version': 1, 'unknown_required': 'fail', 'requirements': requirements}, indent=1))
    key = hashlib.sha256(str(definition.resolve()).encode()).hexdigest()[:16]
    claude_skills = base / 'staging/projects' / key / 'modes/default/plugins/agentpack-bundle/skills'
    clean_rendering = {}
    lock_ref = None
    cells = []
    for location in LOCATIONS:
        # Start every location from a clean, probed checkout, then add the project copy.
        set_project_copy(None)
        assert ap(*sel, 'env', 'restore', '--strict-external').returncode == 0
        lock_ref = lock_ref or sha(definition / 'pack.lock')
        retired = {}
        for agent in VERSIONS:
            assert ap(*sel, 'probe', '--agent', agent, '--strict-external', '--json').returncode == 0
            assert preflight(agent, '--receipt', 'latest')[0] == 0
            if location:
                set_project_copy(location)
                code, report, _ = preflight(agent, '--receipt', 'latest')
                retired[agent] = code != 0 and any(f['code'] == 'RECEIPT_STALE' for f in report['findings'])
                set_project_copy(None)
        set_project_copy(location)
        set_project_copy(location, native_ws)
        assert ap(*sel, 'env', 'restore', '--strict-external').returncode == 0
        for agent in VERSIONS:
            runs = [native_listing(agent, native_ws, pinned_root, marks, base) for _ in range(REPEATS[agent])]
            probe = ap(*sel, 'probe', '--agent', agent, '--strict-external', '--json')
            code, report, results = preflight(agent, '--receipt', 'latest')
            findings = sorted({f['code'] for f in report['findings'] if f['severity'] != 'info'})
            for skill in SKILLS:
                outcomes = {}
                for run in runs:
                    label = '+'.join(run[skill]) or 'none'
                    outcomes[label] = outcomes.get(label, 0) + 1
                want = expected_observed(outcomes)
                got = results.get(f'observed-winner:{skill}')
                cell = {'project_copy_at': location or 'none', 'agent': agent, 'skill': skill,
                        'native_runs': len(runs), 'native_outcomes': outcomes,
                        'lock_unchanged': sha(definition / 'pack.lock') == lock_ref,
                        'agentpack': {'probe_exit': probe.returncode, 'preflight_exit': code,
                                      'planned-winner': results.get(f'planned-winner:{skill}'),
                                      'observed-winner': got, 'findings': findings},
                        'expected_observed_winner': want, 'observed_matches_native': got == want}
                if location:
                    cell['earlier_receipt_retired'] = retired[agent]
                if agent == 'claude':
                    staged = claude_skills / skill / 'SKILL.md'
                    # Agentpack renders SKILL.md, so compare with its own rendering in the clean checkout.
                    if location is None:
                        clean_rendering[skill] = sha(staged)
                    cell['staged_skill_is_the_pinned_rendering'] = staged.exists() and sha(staged) == clean_rendering[skill]
                    cell['staged_skill_is_the_project_copy'] = bool(location) and staged.exists() and \
                        PROJECT_MARK in staged.read_text()
                cells.append(cell)

    probed = [c for c in cells if 'observed_matches_native' in c]
    expiring = [c for c in cells if 'earlier_receipt_retired' in c]
    by_agent = {a: [c for c in probed if c['agent'] == a] for a in VERSIONS}
    summary = {'cells': len(cells), 'probed_cells': len(probed),
               'observed_matches_native_by_agent': {a: f"{sum(c['observed_matches_native'] for c in v)} of {len(v)}" for a, v in by_agent.items()},
               'observed_matches_native': sum(c['observed_matches_native'] for c in probed),
               'receipt_expiry_checks': len(expiring),
               'earlier_receipt_retired': sum(c['earlier_receipt_retired'] for c in expiring),
               'lock_unchanged_in_all_cells': all(c['lock_unchanged'] for c in cells)}
    args.output.write_text(json.dumps({
        'date': time.strftime('%Y-%m-%d'), 'platform': platform.platform(), 'versions': VERSIONS,
        'agentpack_version': subprocess.check_output([AP, '--version'], text=True).strip(), 'agentpack_sha256': sha(AP),
        'skills': SKILLS, 'pin': f'{REPO}@{PIN}', 'model_inference_requested': False, 'summary': summary, 'cells': cells}, indent=1) + '\n')
    for c in cells:
        a = c['agentpack']
        print(f"{c['project_copy_at']:17} {c['agent']:9} {c['skill']:15} native={json.dumps(c['native_outcomes']):38} "
              f"planned={a.get('planned-winner')!s:9} observed={a.get('observed-winner')!s:9} exit={a['preflight_exit']} "
              f"retired={c.get('earlier_receipt_retired', '-')!s:5} {'' if c.get('observed_matches_native', True) else 'MISMATCH'}")
    print(json.dumps(summary))
    # OpenCode is sampled, so a rare instability can be missed there; that is reported, not a script failure.
    stable = by_agent['claude'] + by_agent['codex']
    ok = all(c['observed_matches_native'] for c in stable) and summary['earlier_receipt_retired'] == len(expiring)
    sys.exit(0 if ok else 1)
