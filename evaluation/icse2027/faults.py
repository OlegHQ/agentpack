#!/usr/bin/env python3
"""Seven authored fault classes and their clean controls, with semantic oracles.

Replaces the exit-status-only oracle of the 2026-10-11 runner. Each case must
produce its expected exit status AND its named finding AND its requirement
result, while pack.lock bytes and the checkout stay unchanged. The lock-hash
control is computed from the actual comparison, not asserted.

Cases 1-5 are offline: no native agent is executed. Cases 6-7 need a real
receipt and run only with --claude (Claude Code 2.1.296); they request no model
inference. Exit status is 0 only when every executed oracle holds.
"""
import argparse, hashlib, json, os, platform, shutil, stat, subprocess, sys, tempfile, time
from pathlib import Path

CLAUDE_VERSION = '2.1.296 (Claude Code)'
SKILL = 'held-out-review'
parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
parser.add_argument('--agentpack', type=Path, required=True, help='released Agentpack 0.4.4 binary')
parser.add_argument('--claude', help='Claude Code 2.1.296 executable; enables the two live-receipt cases')
parser.add_argument('--output', type=Path, default=Path(__file__).resolve().parent / 'fault-results.json')
args = parser.parse_args()
AP = args.agentpack.resolve()
CLAUDE = str(Path(args.claude).resolve()) if args.claude else None
if CLAUDE and subprocess.check_output([CLAUDE, '--version'], text=True).strip() != CLAUDE_VERSION:
    parser.error(f'live cases were recorded with {CLAUDE_VERSION}')

CASES = []


def sha(path):
    return hashlib.sha256(Path(path).read_bytes()).hexdigest()


def snap(root):
    out = {}
    for f in root.rglob('*'):
        mode = stat.S_IMODE(f.lstat().st_mode)
        out[str(f.relative_to(root))] = [mode, 'link:' + os.readlink(f) if f.is_symlink()
                                         else sha(f) if f.is_file() else 'directory']
    return out


with tempfile.TemporaryDirectory(prefix='agentpack-faults-') as td:
    base = Path(td).resolve()
    home = base / 'home'
    home.mkdir()
    # Offline cases only need a file to exist at the native path; it is never executed.
    env = {'PATH': os.environ['PATH'], 'HOME': str(home), 'USERPROFILE': str(home),
           'XDG_CONFIG_HOME': str(home / 'config'), 'XDG_DATA_HOME': str(home / 'data'),
           'XDG_STATE_HOME': str(home / 'state'), 'XDG_CACHE_HOME': str(home / 'cache'),
           'AGENTPACK_HOME': str(base / 'managed'), 'AGENTPACK_STAGING_ROOT': str(base / 'staging'),
           'CLAUDE_CODE_PATH': CLAUDE or str(AP), 'CURSOR_AGENT_PATH': str(AP), 'CI': '1',
           'DISABLE_TELEMETRY': '1', 'DO_NOT_TRACK': '1'}
    source = base / SKILL
    source.mkdir()
    (source / 'SKILL.md').write_text(f'---\nname: {SKILL}\ndescription: Benign review fixture.\n---\n\n'
                                     'Check the README. Do not invoke tools.\n')
    definition, ws = base / 'definition', base / 'workspace'
    ws.mkdir()
    (ws / 'README.md').write_text('Benign project\n')
    sel = [AP, '--env', definition, '--workspace', ws]

    def ap(*cmd):
        return subprocess.run([str(c) for c in cmd], cwd=ws, env=env, capture_output=True, text=True, timeout=120)

    def must(p):
        if p.returncode:
            sys.exit(f'setup failed: {p.args}\n{p.stderr}')
        return p

    must(ap(AP, 'env', 'init', '--dir', definition, '--name', 'faults'))
    must(ap(*sel, 'add', source, '--no-sync'))
    must(ap(*sel, 'env', 'restore', '--strict-external'))
    lock = definition / 'pack.lock'
    lock_ref = sha(lock)

    def contract(name, requirement):
        path = base / f'{name}.json'
        path.write_text(json.dumps({'schema_version': 1, 'unknown_required': 'fail', 'requirements': [requirement]}))
        return path

    no_write = contract('no-write', {'id': 'no-checkout-config', 'selector': {'category': 'planned_write'},
                                     'predicate': 'no_workspace_write', 'minimum_evidence': 'generated'})
    observed_unscoped = contract('observed', {'id': 'native-skill', 'selector': {'category': 'skill', 'name': SKILL},
                                              'predicate': 'present', 'minimum_evidence': 'observed'})
    wrong_name = contract('wrong-name', {'id': 'named', 'selector': {'category': 'skill', 'name': 'not-installed'},
                                         'predicate': 'present', 'minimum_evidence': 'generated'})
    right_name = contract('right-name', {'id': 'named', 'selector': {'category': 'skill', 'name': SKILL},
                                         'predicate': 'present', 'minimum_evidence': 'generated'})
    scoped = {'id': 'controlled-skill', 'target': 'claude',
              'selector': {'category': 'skill', 'name': SKILL, 'scope': 'controlled_projection'},
              'predicate': 'present', 'minimum_evidence': 'observed'}

    def case(name, kind, flags, exit_code, finding=None, requirement=None, absent=None):
        before = snap(ws)
        start = time.perf_counter()
        p = ap(*sel, 'preflight', '--json', *flags)
        elapsed = round((time.perf_counter() - start) * 1000, 1)
        try:
            report = json.loads(p.stdout)
        except ValueError:
            report = {}
        codes = sorted({f['code'] for f in report.get('findings', [])})
        results = {r['requirement_id']: r['result'] for r in report.get('contract_results', [])}
        lock_signal = sha(lock) != lock_ref
        oracle = {'exit': p.returncode == exit_code,
                  'finding': finding is None or finding in codes,
                  'finding_absent': absent is None or absent not in codes,
                  'requirement': requirement is None or results.get(requirement[0]) == requirement[1],
                  'lock_bytes_unchanged': not lock_signal,
                  'checkout_unchanged': snap(ws) == before}
        CASES.append({'case': name, 'kind': kind, 'expected': {'exit': exit_code, 'finding': finding,
                      'finding_absent': absent, 'requirement': requirement},
                      'observed': {'exit': p.returncode, 'finding_codes': codes, 'requirements': results},
                      'lock_hash_control_signals': lock_signal, 'elapsed_ms': elapsed,
                      'oracle': oracle, 'pass': all(oracle.values())})

    static = ['--agent', 'claude', '--strict-external']
    case('clean_no_checkout_write', 'clean', [*static, '--contract', no_write], 0, None, ('no-checkout-config', 'satisfied'))
    case('1_required_observation_no_receipt', 'fault', [*static, '--contract', observed_unscoped], 4,
         'OBSERVATION_UNAVAILABLE', ('native-skill', 'unknown'))
    case('2_strict_target_needs_checkout_overlay', 'fault', ['--agent', 'cursor', '--strict-external', '--contract', no_write],
         3, 'WORKSPACE_WRITE_REQUIRED', ('no-checkout-config', 'violated'))
    case('clean_ci_policy_no_user_settings', 'clean', [*static, '--policy', 'ci', '--contract', no_write], 0, None,
         ('no-checkout-config', 'satisfied'), absent='INHERITED_CONFIG')
    settings = home / '.claude/settings.json'
    settings.parent.mkdir(parents=True)
    settings.write_text('{"env":{"BENIGN_HELD_OUT":"1"}}')
    case('3_undeclared_user_settings_under_ci', 'fault', [*static, '--policy', 'ci', '--contract', no_write], 3,
         'INHERITED_CONFIG')
    shutil.rmtree(home / '.claude')
    case('clean_exact_skill_name', 'clean', ['--agent', 'claude', '--contract', right_name], 0, None, ('named', 'satisfied'))
    case('4_required_exact_skill_name_differs', 'fault', ['--agent', 'claude', '--contract', wrong_name], 3,
         'CONTRACT_MISMATCH', ('named', 'violated'))
    forged = base / 'forged-receipt.json'
    forged.write_text('{"schema_version":1,"coverage":[{"category":"skill","scope":"native_catalog","completeness":"complete"}]}')
    case('5_malformed_receipt_claiming_completeness', 'fault',
         ['--agent', 'claude', '--receipt', forged, '--contract', observed_unscoped], 5, 'RECEIPT_STALE',
         ('native-skill', 'unknown'))

    probe_check = None
    if CLAUDE:
        (definition / 'contract.json').write_text(json.dumps(
            {'schema_version': 1, 'unknown_required': 'fail', 'requirements': [scoped]}))
        must(ap(*sel, 'env', 'restore', '--strict-external'))
        before = snap(ws)
        p = must(ap(*sel, 'probe', '--agent', 'claude', '--strict-external', '--json'))
        receipt = json.loads(p.stdout)
        hits = [q for q in receipt['properties'] if q.get('category') == 'skill' and q.get('property') == 'presence'
                and q.get('value') is True and q.get('scope') == 'controlled_projection'
                and q.get('evidence') == 'observed' and Path(q.get('artifact_id', '')).name == SKILL]
        probe_check = {'exact_scoped_skill_entries': len(hits), 'checkout_unchanged': snap(ws) == before,
                       'native_version': receipt['target']['native_version'],
                       'skill_coverage': [c for c in receipt['coverage'] if c['category'] == 'skill']}
        live = [*static, '--receipt', 'latest']
        case('clean_valid_live_receipt', 'clean', live, 0, 'NATIVE_OBSERVED', ('controlled-skill', 'satisfied'))
        ambient = ws / '.agents/skills/ambient-held-out/SKILL.md'
        ambient.parent.mkdir(parents=True)
        ambient.write_text('---\nname: ambient-held-out\ndescription: benign ambient edit\n---\nNothing.\n')
        case('6_live_receipt_then_workspace_edit', 'fault', live, 4, 'RECEIPT_STALE', ('controlled-skill', 'unknown'))
        shutil.rmtree(ws / '.agents')
        case('clean_workspace_edit_reverted', 'clean', live, 0, 'NATIVE_OBSERVED', ('controlled-skill', 'satisfied'))
        Path(hits[0]['evidence_ref']).unlink()
        case('7_retained_diagnostic_removed', 'fault', live, 5, 'OBSERVATION_UNAVAILABLE',
             ('controlled-skill', 'unknown'), absent='RECEIPT_STALE')

    faults = [c for c in CASES if c['kind'] == 'fault']
    cleans = [c for c in CASES if c['kind'] == 'clean']
    summary = {'faults_run': len(faults), 'faults_passing_semantic_oracle': sum(c['pass'] for c in faults),
               'clean_controls_run': len(cleans), 'clean_controls_passing': sum(c['pass'] for c in cleans),
               'faults_signalled_by_lock_hash_control': sum(c['lock_hash_control_signals'] for c in faults),
               'live_cases_run': bool(CLAUDE)}
    args.output.write_text(json.dumps({
        'date': time.strftime('%Y-%m-%d'), 'platform': platform.platform(), 'machine': platform.machine(),
        'agentpack_version': subprocess.check_output([AP, '--version'], text=True).strip(), 'agentpack_sha256': sha(AP),
        'claude_version': CLAUDE_VERSION if CLAUDE else None, 'model_inference_requested': False,
        'summary': summary, 'probe_check': probe_check, 'cases': CASES}, indent=1) + '\n')
    for c in CASES:
        print(f"{'ok  ' if c['pass'] else 'FAIL'} {c['case']:44} exit {c['observed']['exit']}  "
              f"{[k for k, v in c['oracle'].items() if not v] or ''}")
    print(json.dumps({'summary': summary, 'probe_check': probe_check}, indent=1))
    sys.exit(0 if all(c['pass'] for c in CASES) and (not probe_check or probe_check['exact_scoped_skill_entries'] == 1) else 1)
