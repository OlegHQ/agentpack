#!/usr/bin/env -S uv run
# /// script
# requires-python = ">=3.11"
# dependencies = []
# ///
"""Merge semantics.py result files into semantics-results.json and print the rows of the paper's Table III.

Each result file holds the rows of the tools it ran; for a tool present in several files, the last file given
wins (so a newer Agentpack run replaces an older one). A cell (tool, agent, probe) passes when at least 2 of
its 3 runs pass; the "none" control has one run per cell and is reported as runs.

    uv run semantics_summary.py RESULT.json [RESULT.json ...] [--output semantics-results.json]
"""
import argparse, json
from pathlib import Path

HERE = Path(__file__).resolve().parent
AGENTS = ['claude', 'codex', 'opencode', 'cursor', 'grok', 'agy']
PROBES = ['args', 'sign', 'block', 'readonly']
LABELS = {'agentpack': '\\tool{} {version}', 'apm': 'Microsoft APM 0.33.0', 'rulesync': 'rulesync 33.0.0',
          'native': "Agents' own commands", 'none': 'Nothing installed (control)'}
ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
ap.add_argument('results', nargs='+', type=Path)
ap.add_argument('--output', type=Path, default=HERE / 'semantics-results.json')
a = ap.parse_args()

by_tool, sources = {}, {}
for path in a.results:
    d = json.loads(path.read_text())
    for res in d['results']:
        by_tool[res['tool']] = res
        sources[res['tool']] = {'file': path.name, 'date': d['date'], 'agent_versions': d['agent_versions'],
                                'agentpack': (d['tools'].get('agentpack') or {}) if res['tool'] == 'agentpack' else None}

cells = {}
for tool, res in by_tool.items():
    for row in res['rows']:
        c = cells.setdefault(tool, {}).setdefault(row['agent'], {}).setdefault(row['probe'], {'passed': 0, 'runs': 0})
        c['runs'] += 1
        c['passed'] += bool(row['passed'])
not_run = {tool: {ag: v.get('status') for ag, v in res['agents'].items() if v.get('status') != 'signed in'} for tool, res in by_tool.items()}

summary = {}
for tool, agents in cells.items():
    summary[tool] = {}
    for probe in PROBES:
        measured = [ag for ag in AGENTS if probe in agents.get(ag, {})]
        ok = [ag for ag in measured if agents[ag][probe]['passed'] * 3 >= agents[ag][probe]['runs'] * 2 and agents[ag][probe]['passed']]
        summary[tool][probe] = {'agents_passing': len(ok), 'agents_measured': len(measured), 'passing': ok,
                                'runs_passed': sum(agents[ag][probe]['passed'] for ag in measured),
                                'runs': sum(agents[ag][probe]['runs'] for ag in measured)}

out = {'sources': sources, 'not_run': not_run, 'cells': cells, 'summary': summary, 'results': list(by_tool.values())}
a.output.write_text(json.dumps(out, indent=1) + '\n')

version = (sources.get('agentpack', {}).get('agentpack') or {}).get('version', 'agentpack ?').split()[-1]
for tool in ['agentpack', 'apm', 'rulesync', 'native', 'none']:
    if tool not in summary:
        continue
    s = summary[tool]
    if tool == 'none':
        cols = [f"{s[p]['runs_passed']}/{s[p]['runs']}" for p in ('args', 'sign', 'block')]
    else:
        cols = [f"{s[p]['agents_passing']} of {s[p]['agents_measured']}" for p in ('args', 'sign', 'block')]
    print(LABELS[tool].replace('{version}', version) + ' & ' + ' & '.join(cols) + ' \\\\')
for tool in summary:
    s = summary[tool]['readonly']
    print(f"% readonly {tool}: {s['agents_passing']} of {s['agents_measured']} agents, runs {s['runs_passed']}/{s['runs']}")
print('% not run:', {t: v for t, v in not_run.items() if v})
