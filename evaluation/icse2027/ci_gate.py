#!/usr/bin/env python3
"""Preconfigure coding agents in CI and gate on what they will actually list.

Runs on a clean CI runner with no account and no API key. It pins one public skill with
Agentpack, restores it, asks each installed agent what a session in the checkout lists
(`agentpack probe`, no model call), and gates with `agentpack preflight`. It then adds a project
skill with the same name and checks that the gate turns red, first because the earlier
observation no longer applies and then, after a new probe, because the pinned skill no longer
holds its name alone.

Exit status 0 means every step gave the expected exit code. A Markdown table is appended to
$GITHUB_STEP_SUMMARY when that variable is set.
"""
import json, os, platform, shutil, subprocess, sys, tempfile
from pathlib import Path

SKILL = 'webapp-testing'
SPEC = f'anthropics/skills/skills/{SKILL}@dbd4588f9e1033efb41dad4bef2f7947c8993d44'
# The project root each agent reads on its own.
AGENTS = {'claude': '.claude/skills', 'codex': '.agents/skills', 'opencode': '.opencode/skills'}

ap = shutil.which('agentpack')
if not ap:
    sys.exit('agentpack is not on PATH')
agents = [a for a in AGENTS if shutil.which(a)]
if not agents:
    sys.exit('no supported agent CLI on PATH')

rows, ok = [], True
with tempfile.TemporaryDirectory(prefix='agentpack-ci-') as td:
    base = Path(td).resolve()
    env = {**os.environ, 'AGENTPACK_HOME': str(base / 'managed'), 'AGENTPACK_STAGING_ROOT': str(base / 'staging')}
    definition, ws = base / 'definition', base / 'checkout'
    ws.mkdir()
    (ws / 'README.md').write_text('CI checkout\n')

    def run(*cmd):
        p = subprocess.run([ap, *map(str, cmd)], cwd=ws, env=env, capture_output=True, text=True, timeout=900)
        return p.returncode, p.stdout, p.stderr

    sel = ['--env', definition, '--workspace', ws]
    for step in (['env', 'init', '--dir', definition, '--name', 'ci'], [*sel, 'add', SPEC, '--no-sync']):
        code, out, err = run(*step)
        if code:
            sys.exit(f'setup failed: {step}\n{err}')
    (definition / 'contract.json').write_text(json.dumps({'schema_version': 1, 'unknown_required': 'fail', 'requirements': [
        {'id': 'planned-winner', 'selector': {'category': 'skill', 'name': SKILL, 'property': 'winner'},
         'predicate': 'source_allowed', 'expected': 'package', 'minimum_evidence': 'generated'},
        {'id': 'observed-winner', 'selector': {'category': 'skill', 'name': SKILL, 'property': 'winner',
                                               'scope': 'workspace_session'},
         'predicate': 'source_allowed', 'expected': 'package', 'minimum_evidence': 'observed'}]}))

    def gate(agent, label, expected):
        global ok
        code, out, err = run(*sel, 'preflight', '--agent', agent, '--receipt', 'latest', '--json')
        try:
            report = json.loads(out)
            results = {r['requirement_id']: r['result'] for r in report.get('contract_results', [])}
            results['findings'] = sorted({f['code'] for f in report['findings'] if f['severity'] in ('violation', 'error')})
        except ValueError:
            results = {'error': (err or out).strip()[-200:]}
        good = code == expected
        ok = ok and good
        rows.append({'agent': agent, 'step': label, 'exit': code, 'expected_exit': expected, 'as_expected': good, **results})
        print(f"{agent:9} {label:38} exit {code} (expected {expected}) {results}", flush=True)

    for agent in agents:
        version = subprocess.run([agent, '--version'], capture_output=True, text=True).stdout.strip().splitlines()[:1]
        print(f'== {agent} {version[0] if version else ""}', flush=True)
        for d in ('.claude', '.agents', '.codex', '.opencode'):
            shutil.rmtree(ws / d, ignore_errors=True)
        code, out, err = run(*sel, 'env', 'restore')
        gate(agent, 'restored, not observed', 4)
        code, out, err = run(*sel, 'probe', '--agent', agent, '--json')
        if code:
            print(f'probe failed ({code}): {(err or out).strip()[-300:]}', flush=True)
        gate(agent, 'observed', 0)
        copy = ws / AGENTS[agent] / SKILL / 'SKILL.md'
        copy.parent.mkdir(parents=True)
        copy.write_text(f'---\nname: {SKILL}\ndescription: Project copy.\n---\n\nProject version.\n')
        gate(agent, 'same-named project skill appears', 4)
        run(*sel, 'env', 'restore')
        run(*sel, 'probe', '--agent', agent, '--json')
        gate(agent, 'observed again with the project skill', 3)

summary = os.environ.get('GITHUB_STEP_SUMMARY')
table = ['| Agent | Step | Exit | Expected | Planned winner | Observed winner |', '|---|---|---|---|---|---|']
table += [f"| {r['agent']} | {r['step']} | {r['exit']} | {r['expected_exit']} | {r.get('planned-winner', '')} | {r.get('observed-winner', '')} |"
          for r in rows]
if summary:
    with open(summary, 'a') as handle:
        handle.write(f'### Agent configuration gate on {platform.platform()}\n\n' + '\n'.join(table) + '\n')
Path(os.environ.get('CI_GATE_OUTPUT', 'ci-gate-results.json')).write_text(json.dumps(
    {'platform': platform.platform(), 'agents': agents, 'rows': rows, 'all_as_expected': ok}, indent=1) + '\n')
sys.exit(0 if ok else 1)
