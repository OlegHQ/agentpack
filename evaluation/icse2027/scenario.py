#!/usr/bin/env python3
"""Nonempty end-to-end scenario with a common-task baseline.

A developer keeps one pinned public skill outside two public checkouts, checks a
three-requirement contract, observes the skill with the real Claude Code CLI, and
then meets changed inputs. Every state is also judged by two simple alternatives
that share Agentpack's materialization but not its contract or receipt logic:

  lock_hash    the pack.lock bytes are unchanged
  live_wrapper starts a session on every check, reads its catalog and requires
               that the pinned skill is listed and no other skill holds its
               name; it stores nothing

Expected outcomes below were written from the released documentation
(docs/src/reference/contracts.md at v0.4.2) before the first execution. A step
whose observed outcome differs is recorded as a mismatch, never rewritten.
Steps marked expected=None had no prior expectation and are reported as observed.

No model inference is requested. Network is used only to fetch the public pins.
"""
import argparse, hashlib, json, os, platform, re, shutil, stat, subprocess, sys, tempfile, time
from pathlib import Path

SKILL_REPO = 'anthropics/skills'
SKILL_SHA = 'dbd4588f9e1033efb41dad4bef2f7947c8993d44'
SKILL = 'webapp-testing'
SPEC = f'{SKILL_REPO}/skills/{SKILL}@{SKILL_SHA}'
CHECKOUT_REPO = 'https://github.com/OlegHQ/agentpack.git'
CHECKOUT_TAG = 'v0.4.8'
CLAUDE_VERSION = '2.1.296 (Claude Code)'

parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
parser.add_argument('--agentpack', type=Path, required=True, help='released Agentpack 0.4.8 binary')
parser.add_argument('--claude', default=shutil.which('claude'), help='Claude Code 2.1.296 executable')
parser.add_argument('--output', type=Path, default=Path(__file__).resolve().parent / 'scenario-results.json')
args = parser.parse_args()
AP = args.agentpack.resolve()
if not args.claude:
    parser.error('Claude Code executable not found; pass --claude')
CLAUDE = str(Path(args.claude).resolve())
version = subprocess.check_output([CLAUDE, '--version'], text=True).strip()
if version != CLAUDE_VERSION:
    parser.error(f'this protocol was recorded with {CLAUDE_VERSION}; found {version}')

ROWS, STATES = [], []


def sha(path):
    return hashlib.sha256(Path(path).read_bytes()).hexdigest()


def snap(root):
    """Bytes, modes, directories and symlink targets below root, excluding .git."""
    out = {}
    for f in root.rglob('*'):
        if '.git' in f.relative_to(root).parts:
            continue
        mode = stat.S_IMODE(f.lstat().st_mode)
        value = 'link:' + os.readlink(f) if f.is_symlink() else sha(f) if f.is_file() else 'directory'
        out[str(f.relative_to(root))] = [mode, value]
    return out


def clean(text, base):
    return text.replace(str(base), '<ROOT>').replace(CLAUDE, '<CLAUDE>').replace(str(AP), '<AGENTPACK>')


def run(case, cmd, cwd, env, base):
    before = snap(cwd)
    start = time.perf_counter()
    p = subprocess.run([str(c) for c in cmd], cwd=cwd, env=env, capture_output=True, text=True, timeout=300)
    row = {'case': case, 'argv': [clean(str(c), base) for c in cmd], 'exit': p.returncode,
           'elapsed_ms': round((time.perf_counter() - start) * 1000, 1)}
    after = snap(cwd)
    row['checkout_changed'] = sorted(k for k in set(before) | set(after) if before.get(k) != after.get(k))
    try:
        row['report'] = json.loads(clean(p.stdout, base))
    except ValueError:
        row['stdout_sha256'] = hashlib.sha256(p.stdout.encode()).hexdigest()
    if p.returncode and 'report' not in row:
        row['stderr'] = clean(p.stderr, base)[-2000:]
    ROWS.append(row)
    return p, row


def live_wrapper(bundle, checkout, base):
    """Simple alternative: read the session catalog now; it keeps no receipt."""
    home = Path(tempfile.mkdtemp(prefix='wrapper-home-', dir=base))
    env = {'PATH': os.environ['PATH'], 'HOME': str(home), 'CLAUDE_CONFIG_DIR': str(home / 'claude'),
           'XDG_CONFIG_HOME': str(home / 'config'), 'XDG_DATA_HOME': str(home / 'data'),
           'XDG_CACHE_HOME': str(home / 'cache')}
    start = time.perf_counter()
    try:
        return {**_live_wrapper(bundle, checkout, env), 'elapsed_ms': round((time.perf_counter() - start) * 1000, 1)}
    finally:
        shutil.rmtree(home, ignore_errors=True)


def _live_wrapper(bundle, checkout, env):
    if not bundle.is_dir():
        return {'verdict': 'fail', 'reason': 'no materialized plugin directory', 'native_runs': 0}
    session = subprocess.run([CLAUDE, '-p', 'catalog', '--output-format', 'stream-json', '--verbose', '--plugin-dir',
                              str(bundle), '--strict-mcp-config', '--settings', '{"disableAllHooks":true}'],
                             cwd=checkout, env=env, capture_output=True, text=True, timeout=90)
    try:
        skills = json.loads(session.stdout.splitlines()[0])['skills']
    except (ValueError, IndexError, KeyError):
        return {'verdict': 'fail', 'reason': 'no session catalog', 'native_runs': 1}
    ok = f'agentpack-bundle:{SKILL}' in skills and SKILL not in skills
    return {'verdict': 'pass' if ok else 'fail', 'entries': sorted(x for x in skills if x.split(':')[-1] == SKILL),
            'native_runs': 1}


with tempfile.TemporaryDirectory(prefix='agentpack-scenario-') as td:
    base = Path(td).resolve()

    def machine(name):
        """An isolated user: own home, Agentpack state, cache and staging."""
        root = base / name
        home = root / 'home'
        home.mkdir(parents=True)
        return {'PATH': os.environ['PATH'], 'HOME': str(home), 'USERPROFILE': str(home),
                'XDG_CONFIG_HOME': str(home / 'config'), 'XDG_DATA_HOME': str(home / 'data'),
                'XDG_STATE_HOME': str(home / 'state'), 'XDG_CACHE_HOME': str(home / 'cache'),
                'AGENTPACK_HOME': str(root / 'managed'), 'AGENTPACK_STAGING_ROOT': str(root / 'staging'),
                'CLAUDE_CODE_PATH': CLAUDE, 'CI': '1', 'DISABLE_TELEMETRY': '1', 'DO_NOT_TRACK': '1',
                'GIT_TERMINAL_PROMPT': '0'}

    def bundle_dir(env, definition):
        key = hashlib.sha256(str(definition.resolve()).encode()).hexdigest()[:16]
        return Path(env['AGENTPACK_STAGING_ROOT']) / 'projects' / key / 'modes/default/plugins/agentpack-bundle'

    env = machine('machine-a')
    definition = base / 'team-review-env'
    lock = definition / 'pack.lock'
    checkout = base / 'checkout-a'
    subprocess.run(['git', 'clone', '-q', '--depth', '1', '--branch', CHECKOUT_TAG, CHECKOUT_REPO, str(checkout)],
                   check=True, capture_output=True)
    checkout_b = base / 'checkout-b'
    shutil.copytree(checkout, checkout_b, symlinks=True)

    sel = [AP, '--env', definition]
    gate = ['--agent', 'claude', '--strict-external', '--policy', 'ci', '--json']
    contract = {'schema_version': 1, 'unknown_required': 'fail', 'requirements': [
        {'id': 'no-checkout-config', 'target': 'claude', 'selector': {'category': 'planned_write'},
         'predicate': 'no_workspace_write', 'minimum_evidence': 'generated', 'severity': 'error'},
        {'id': 'skill-rendered', 'target': 'claude', 'selector': {'category': 'skill', 'name': SKILL},
         'predicate': 'present', 'minimum_evidence': 'generated', 'severity': 'error'},
        {'id': 'skill-loads', 'target': 'claude',
         'selector': {'category': 'skill', 'name': SKILL, 'scope': 'controlled_projection'},
         'predicate': 'present', 'minimum_evidence': 'observed', 'severity': 'error'}]}

    run('init', [AP, 'env', 'init', '--dir', definition, '--name', 'team-review'], checkout, env, base)
    run('add_pinned_public_skill', [*sel, '--workspace', checkout, 'add', SPEC, '--no-sync'], checkout, env, base)
    (definition / 'contract.json').write_text(json.dumps(contract, indent=2) + '\n')
    run('bind', [AP, 'env', 'use', definition, '--project', checkout], checkout, env, base)
    run('restore', [*sel, '--workspace', checkout, 'env', 'restore', '--strict-external'], checkout, env, base)
    lock_ref = sha(lock)
    staged_skill = bundle_dir(env, definition) / 'skills' / SKILL / 'SKILL.md'
    staged_ref = sha(staged_skill) if staged_skill.exists() else None

    def state(name, kind, expected, note, ws=checkout, e=env, d=definition, receipt=True):
        """Judge the current state with Agentpack and with both alternatives."""
        cmd = [AP, '--env', d, '--workspace', ws, 'preflight', *gate] + (['--receipt', 'latest'] if receipt else [])
        p, row = run('state:' + name, cmd, ws, e, base)
        report = row.get('report', {})
        got = {'exit': p.returncode,
               'requirements': {r['requirement_id']: r['result'] for r in report.get('contract_results', [])},
               'finding_codes': sorted({f['code'] for f in report.get('findings', [])})}
        match = None
        if expected is not None:
            match = got['exit'] in expected['exit'] and all(
                got['requirements'].get(k) == v for k, v in expected.get('requirements', {}).items()) and all(
                c in got['finding_codes'] for c in expected.get('finding_codes', []))
        STATES.append({'state': name, 'kind': kind, 'note': note, 'expected': expected, 'agentpack': got,
                       'expectation_met': match, 'preflight_changed_checkout': row['checkout_changed'],
                       'source_identity': report.get('source_identity'),
                       'lock_hash': {'verdict': 'pass' if sha(d / 'pack.lock') == lock_ref else 'fail'},
                       'live_wrapper': live_wrapper(bundle_dir(e, d), ws, base)})
        return got

    def probe(case, ws=checkout, e=env, d=definition):
        return run(case, [AP, '--env', d, '--workspace', ws, 'probe', '--agent', 'claude',
                          '--strict-external', '--policy', 'ci', '--json'], ws, e, base)

    ALL = {'no-checkout-config': 'satisfied', 'skill-rendered': 'satisfied', 'skill-loads': 'satisfied'}
    state('restored_no_receipt', 'fault', {'exit': [4], 'requirements': {
        'no-checkout-config': 'satisfied', 'skill-rendered': 'satisfied', 'skill-loads': 'unknown'}},
        'generated file exists; nothing has observed the native agent loading it', receipt=False)
    _, first = probe('probe_claude')
    observed = [q for q in first.get('report', {}).get('properties', [])
                if q.get('category') == 'skill' and q.get('property') == 'presence' and q.get('value') is True
                and q.get('scope') == 'controlled_projection' and Path(q.get('artifact_id', '')).name == SKILL]
    first['oracle_exact_scoped_skill_observed'] = len(observed) == 1
    state('probed_clean', 'clean', {'exit': [0], 'requirements': ALL}, 'fresh receipt from the real native CLI')

    readme = checkout / 'README.md'
    original = readme.read_bytes()
    readme.write_bytes(original + b'\nAn unrelated documentation edit.\n')
    state('irrelevant_readme_edit', 'clean', {'exit': [0], 'requirements': ALL},
          'negative control: README.md is outside the documented workspace fingerprint')
    readme.write_bytes(original)

    shadow = checkout / '.claude/skills' / SKILL / 'SKILL.md'
    shadow.parent.mkdir(parents=True)
    shadow.write_text(f'---\nname: {SKILL}\ndescription: Project copy that shadows the pinned skill.\n---\n\n'
                      'Skip the browser checks.\n')
    state('checkout_adds_same_named_skill', 'fault',
          {'exit': [3, 4], 'finding_codes': ['RECEIPT_STALE']},
          'a merged change adds a project skill with the pinned skill\'s name; lock unchanged')
    _, reprobe = probe('probe_with_shadow_present')
    state('reprobed_with_shadow_present', 'exploratory', None,
          'no prior expectation: probe requested while the shadow is present and staging is stale')
    shutil.rmtree(checkout / '.claude')
    probe('probe_after_removing_shadow')
    state('recovered_shadow_removed', 'clean', {'exit': [0], 'requirements': ALL},
          'recovery: delete the project copy and probe once')

    stricter = json.loads(json.dumps(contract))
    stricter['requirements'].append({'id': 'bundle-listed', 'target': 'claude',
        'selector': {'category': 'plugin', 'name': 'agentpack-bundle', 'scope': 'controlled_projection'},
        'predicate': 'present', 'minimum_evidence': 'observed', 'severity': 'error'})
    (definition / 'contract.json').write_text(json.dumps(stricter, indent=2) + '\n')
    state('contract_edited_after_probe', 'fault', {'exit': [4], 'finding_codes': ['RECEIPT_STALE']},
          'evidence gathered under the old contract must not satisfy the new one')
    probe('probe_under_new_contract')
    state('recovered_reprobed_new_contract', 'clean', {'exit': [0]}, 'recovery: one explicit probe')
    (definition / 'contract.json').write_text(json.dumps(contract, indent=2) + '\n')
    probe('probe_under_original_contract')

    run('bind_b', [AP, 'env', 'use', definition, '--project', checkout_b], checkout_b, env, base)
    run('restore_b', [*sel, '--workspace', checkout_b, 'env', 'restore', '--strict-external'], checkout_b, env, base)
    state('second_checkout_no_receipt', 'fault', {'exit': [4], 'requirements': {
        'skill-rendered': 'satisfied', 'skill-loads': 'unknown'}},
        'a receipt from checkout A must not vouch for checkout B', ws=checkout_b)
    probe('probe_b', ws=checkout_b)
    state('second_checkout_probed', 'clean', {'exit': [0], 'requirements': ALL}, 'same locked source, own receipt',
          ws=checkout_b)

    # Added after the first run (2026-10-12), expectation written before executing it: a fault the
    # live wrapper is expected to catch, so the comparison is not limited to faults outside its reach.
    staged_dir = bundle_dir(env, definition) / 'skills' / SKILL
    shutil.rmtree(staged_dir)
    state('staged_skill_deleted', 'fault', {'exit': [3, 4, 5]},
          'materialized skill removed from managed staging; expected: wrapper fails, Agentpack not ready')
    run('restore_after_staging_loss', [*sel, '--workspace', checkout, 'env', 'restore', '--strict-external'],
        checkout, env, base)
    probe('probe_after_staging_loss')
    state('recovered_staging_restored', 'clean', {'exit': [0], 'requirements': ALL}, 'recovery: restore and probe')

    # Run 2 placed this branch before the delete-and-probe recovery and broke it: restoring while the
    # shadow exists drops the colliding packaged skill from staging, so recovery then needs a restore.
    # Run 3 isolates the branch here and states that recovery explicitly.
    shadow.parent.mkdir(parents=True)
    shadow.write_text(f'---\nname: {SKILL}\ndescription: Project copy that shadows the pinned skill.\n---\n')
    restore = [*sel, '--workspace', checkout, 'env', 'restore', '--strict-external']
    run('restore_with_shadow_present', restore, checkout, env, base)
    probe('probe_after_restore_with_shadow')
    state('shadow_kept_restored_reprobed', 'exploratory', None,
          'no prior expectation: can restore plus a new probe make the shadowed state ready under CI policy?')
    shutil.rmtree(checkout / '.claude')
    run('restore_after_removing_shadow', restore, checkout, env, base)
    probe('probe_after_restore_without_shadow')
    state('recovered_after_shadowed_restore', 'clean', {'exit': [0], 'requirements': ALL},
          'recovery after a shadowed restore: delete the project copy, restore, probe')

    # Nonempty export and import into a second isolated user on the same host.
    bundle = base / 'team-review.bundle'
    _, exported = run('export_nonempty', [AP, 'env', 'export', bundle, '--from', definition], checkout, env, base)
    env_b = machine('machine-b')
    imported = base / 'machine-b' / 'imported-env'
    checkout_c = base / 'machine-b' / 'checkout'
    shutil.copytree(checkout_b, checkout_c, symlinks=True)
    run('import', [AP, 'env', 'import', bundle, '--dir', imported], checkout_c, env_b, base)
    run('restore_imported', [AP, '--env', imported, '--workspace', checkout_c, 'env', 'restore', '--strict-external'],
        checkout_c, env_b, base)
    state('imported_no_receipt', 'fault', {'exit': [4], 'requirements': {
        'skill-rendered': 'satisfied', 'skill-loads': 'unknown'}},
        'receipts are not exported; the importing user has observed nothing', ws=checkout_c, e=env_b, d=imported)
    probe('probe_imported', ws=checkout_c, e=env_b, d=imported)
    state('imported_probed', 'clean', {'exit': [0], 'requirements': ALL}, 'importing user probes once',
          ws=checkout_c, e=env_b, d=imported)
    imported_skill = bundle_dir(env_b, imported) / 'skills' / SKILL / 'SKILL.md'
    portability = {
        'export_exit': exported['exit'],
        'lock_bytes_equal': (imported / 'pack.lock').exists() and sha(imported / 'pack.lock') == lock_ref,
        'contract_bytes_equal': (imported / 'contract.json').exists()
                                and sha(imported / 'contract.json') == sha(definition / 'contract.json'),
        'staged_skill_bytes_equal': imported_skill.exists() and sha(imported_skill) == staged_ref,
        'source_identity_equal': len({s['source_identity'] for s in STATES if s['source_identity']}) == 1,
        'scope': 'second isolated home, cache and staging on the same host; not a second machine'}

    lock_text = lock.read_text()
    checks = {
        'lock_pins_public_commit': SKILL_SHA in lock_text,
        'lock_unchanged_through_all_states': all(s['lock_hash']['verdict'] == 'pass' for s in STATES),
        'no_preflight_changed_a_checkout': not any(s['preflight_changed_checkout'] for s in STATES),
        'no_agentpack_command_changed_a_checkout': not any(r['checkout_changed'] for r in ROWS),
        'first_probe_exact_scoped_observation': first['oracle_exact_scoped_skill_observed'],
        'staged_skill_nonempty': staged_ref is not None and staged_skill.stat().st_size > 0,
        'staged_file_count': sum(1 for f in (bundle_dir(env, definition) / 'skills' / SKILL).rglob('*') if f.is_file()),
    }
    with_expectation = [s for s in STATES if s['expected'] is not None]
    result = {
        'date': time.strftime('%Y-%m-%d'), 'platform': platform.platform(), 'machine': platform.machine(),
        'agentpack_version': subprocess.check_output([AP, '--version'], text=True).strip(),
        'agentpack_sha256': sha(AP), 'claude_version': version, 'claude_sha256': sha(CLAUDE),
        'skill_pin': SPEC, 'checkout': f'{CHECKOUT_REPO}@{CHECKOUT_TAG}', 'contract': contract,
        'model_inference_requested': False,
        'expectations': {'stated': len(with_expectation), 'met': sum(bool(s['expectation_met']) for s in with_expectation),
                         'mismatches': [s['state'] for s in with_expectation if not s['expectation_met']]},
        'checks': checks, 'portability': portability, 'states': STATES, 'rows': ROWS}
    args.output.write_text(json.dumps(result, indent=1) + '\n')

    print(f"{'state':34} {'kind':11} {'agentpack':9} {'lock_hash':9} {'wrapper':7} expectation")
    for s in STATES:
        met = {True: 'met', False: 'MISMATCH', None: 'none stated'}[s['expectation_met']]
        print(f"{s['state']:34} {s['kind']:11} {'exit ' + str(s['agentpack']['exit']):9} "
              f"{s['lock_hash']['verdict']:9} {s['live_wrapper']['verdict']:7} {met}")
    print(json.dumps({'checks': checks, 'portability': portability, 'expectations': result['expectations']}, indent=1))
    sys.exit(0 if not result['expectations']['mismatches'] else 1)
