#!/usr/bin/env python3
"""Does Agentpack report the name clash in real public repositories?

Takes repositories from prevalence-results.json that carry a project skill named like a skill of
anthropics/skills with different content, ten per project root. From each repository only that
skill root is checked out (sparse, no other file), so nothing else in the repository can
influence the agents. The matching upstream skill is pinned with Agentpack, and Claude Code and
Codex are probed in the checkout. No model call is made.

The expected verdicts are fixed per root before running, from what each agent reads
(collision-results.json): the observed requirement must be violated for Claude Code when the
copy is in .claude/skills, for Codex when it is in .agents/skills or .codex/skills, and
satisfied otherwise.
"""
import argparse, hashlib, json, os, platform, shutil, subprocess, sys, tempfile, time
from pathlib import Path

REPO, PIN = 'anthropics/skills', 'dbd4588f9e1033efb41dad4bef2f7947c8993d44'
EXPECT_OBSERVED = {'.claude/skills': {'claude': 'violated', 'codex': 'satisfied'},
                   '.agents/skills': {'claude': 'satisfied', 'codex': 'violated'},
                   '.codex/skills': {'claude': 'satisfied', 'codex': 'violated'}}

parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
parser.add_argument('--agentpack', type=Path, required=True)
parser.add_argument('--per-root', type=int, default=10)
parser.add_argument('--output', type=Path, default=Path(__file__).resolve().parent / 'realrepos-results.json')
args = parser.parse_args()
AP = args.agentpack.resolve()
HERE = Path(__file__).resolve().parent
prevalence = json.loads((HERE / 'prevalence-results.json').read_text())


def sha(path):
    return hashlib.sha256(Path(path).read_bytes()).hexdigest()


# Deterministic sample: walk skills in name order round-robin, first unused repository each time.
candidates = {}
for root in EXPECT_OBSERVED:
    per_skill = {}
    for row in prevalence['rows']:
        if row['root'] == root:
            per_skill[row['skill']] = sorted(f['repository'] for f in row['files']
                                             if f['kind'] == 'differs_from_every_upstream_version')
    chosen, used, depth = [], set(), 0
    while len(chosen) < args.per_root * 2 and depth < 100:
        for skill in sorted(per_skill):
            if depth < len(per_skill[skill]) and per_skill[skill][depth] not in used:
                used.add(per_skill[skill][depth])
                chosen.append((per_skill[skill][depth], skill))
        depth += 1
    candidates[root] = chosen

rows = []
with tempfile.TemporaryDirectory(prefix='agentpack-realrepos-') as td:
    base = Path(td).resolve()
    home = base / 'home'
    home.mkdir()
    env = {'PATH': os.environ['PATH'], 'HOME': str(home), 'USERPROFILE': str(home),
           'XDG_CONFIG_HOME': str(home / 'config'), 'XDG_DATA_HOME': str(home / 'data'),
           'XDG_CACHE_HOME': str(home / 'cache'), 'XDG_STATE_HOME': str(home / 'state'),
           'AGENTPACK_HOME': str(base / 'managed'), 'AGENTPACK_STAGING_ROOT': str(base / 'staging'),
           'CLAUDE_CODE_PATH': shutil.which('claude'), 'CODEX_PATH': shutil.which('codex'), 'CI': '1',
           'GIT_TERMINAL_PROMPT': '0', 'DISABLE_TELEMETRY': '1'}

    def run(*cmd, cwd=None):
        return subprocess.run([str(c) for c in cmd], cwd=cwd, env=env, capture_output=True, text=True, timeout=300)

    for root, chosen in candidates.items():
        done = 0
        for index, (repository, skill) in enumerate(chosen):
            if done == args.per_root:
                break
            ws = base / f"ws-{root.strip('.').replace('/', '-')}-{index}"
            clone = run('git', 'clone', '-q', '--depth', '1', '--filter=blob:none', '--no-checkout',
                        f'https://github.com/{repository}.git', ws)
            if clone.returncode:
                rows.append({'repository': repository, 'root': root, 'skill': skill, 'status': 'clone failed'})
                continue
            run('git', '-C', ws, 'sparse-checkout', 'set', '--no-cone', f'/{root}/')
            run('git', '-C', ws, 'checkout', '-q')
            copy = ws / root / skill / 'SKILL.md'
            row = {'repository': repository, 'root': root, 'skill': skill,
                   'commit': run('git', '-C', ws, 'rev-parse', 'HEAD').stdout.strip()}
            if not copy.is_file():
                rows.append({**row, 'status': 'copy no longer at that path'})
                shutil.rmtree(ws, ignore_errors=True)
                continue
            definition = base / 'definitions' / skill
            if not definition.exists():
                assert run(AP, 'env', 'init', '--dir', definition, '--name', f'pin-{skill}', cwd=ws).returncode == 0
                assert run(AP, '--env', definition, '--workspace', ws, 'add', f'{REPO}/skills/{skill}@{PIN}',
                           '--no-sync', cwd=ws).returncode == 0
                (definition / 'contract.json').write_text(json.dumps({'schema_version': 1, 'unknown_required': 'fail', 'requirements': [
                    {'id': 'planned-winner', 'selector': {'category': 'skill', 'name': skill, 'property': 'winner'},
                     'predicate': 'source_allowed', 'expected': 'package', 'minimum_evidence': 'generated'},
                    {'id': 'observed-winner', 'selector': {'category': 'skill', 'name': skill, 'property': 'winner',
                                                           'scope': 'workspace_session'},
                     'predicate': 'source_allowed', 'expected': 'package', 'minimum_evidence': 'observed'}]}))
            sel = [AP, '--env', definition, '--workspace', ws]
            lock_before = sha(definition / 'pack.lock')
            restore = run(*sel, 'env', 'restore', cwd=ws)
            row.update(status='checked', project_skills_in_root=sum(1 for _ in (ws / root).glob('*/SKILL.md')), agents={})
            for agent in ('claude', 'codex'):
                probe = run(*sel, 'probe', '--agent', agent, '--json', cwd=ws)
                pre = run(*sel, 'preflight', '--agent', agent, '--receipt', 'latest', '--json', cwd=ws)
                try:
                    report = json.loads(pre.stdout)
                    results = {r['requirement_id']: r['result'] for r in report.get('contract_results', [])}
                except ValueError:
                    results = {}
                want = EXPECT_OBSERVED[root][agent]
                row['agents'][agent] = {'restore_exit': restore.returncode, 'probe_exit': probe.returncode,
                                        'preflight_exit': pre.returncode, **results, 'expected_observed': want,
                                        'as_expected': results.get('observed-winner') == want}
            row['lock_unchanged'] = sha(definition / 'pack.lock') == lock_before
            rows.append(row)
            done += 1
            print(repository, root, skill, {a: (v.get('planned-winner'), v.get('observed-winner'), v['as_expected'])
                                            for a, v in row['agents'].items()}, flush=True)
            shutil.rmtree(ws, ignore_errors=True)

checked = [r for r in rows if r['status'] == 'checked']
summary = {'repositories_checked': len(checked), 'skipped': len(rows) - len(checked),
           'lock_unchanged': sum(r['lock_unchanged'] for r in checked)}
for agent in ('claude', 'codex'):
    summary[f'{agent}_observed_as_expected'] = sum(r['agents'][agent]['as_expected'] for r in checked)
    summary[f'{agent}_observed_violated'] = sum(r['agents'][agent].get('observed-winner') == 'violated' for r in checked)
    summary[f'{agent}_planned_violated'] = sum(r['agents'][agent].get('planned-winner') == 'violated' for r in checked)
summary['clash_reported_by_at_least_one_grade'] = sum(
    any(v.get('planned-winner') == 'violated' or v.get('observed-winner') == 'violated' for v in r['agents'].values())
    for r in checked)
args.output.write_text(json.dumps({
    'date': time.strftime('%Y-%m-%d'), 'platform': platform.platform(),
    'agentpack_version': subprocess.check_output([AP, '--version'], text=True).strip(),
    'versions': {a: subprocess.check_output([a, '--version'], text=True).strip() for a in ('claude', 'codex')},
    'pin': f'{REPO}@{PIN}', 'model_inference_requested': False, 'summary': summary, 'rows': rows}, indent=1) + '\n')
print(json.dumps(summary, indent=1))
sys.exit(0 if all(r['agents'][a]['as_expected'] for r in checked for a in r['agents']) else 1)
