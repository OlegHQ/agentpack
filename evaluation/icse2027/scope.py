#!/usr/bin/env -S uv run
# /// script
# requires-python = ">=3.11"
# dependencies = []
# ///
"""Table II restricted to what each tool claims: skills, which every compared tool installs, and each
tool's own targets.

Reads the stored cells of ecosystem-results.json (or heldout-results.json); nothing is run again and no cell
is rescored. Two restrictions are reported for every tool:

  skills_all_agents     skill cells over the six agents
  skills_own_targets    skill cells over the agents the tool was given as targets (TARGETS, taken from the
                        install command each tool's documentation gives, as used by ecosystem.py)

A cell counts when its status is native or converted (every skill of the input present for that agent).

    uv run scope.py [--results ecosystem-results.json]
"""
import argparse, json
from pathlib import Path

HERE = Path(__file__).resolve().parent
ALL = ['claude', 'codex', 'opencode', 'cursor', 'grok', 'agy']
# Agents named in each tool's install command in ecosystem.py plan(); None means the tool writes for every agent it
# knows without being told (Agentpack, APM, the agents' own commands) or was given all six.
TARGETS = {
    'skills-lock': ['claude', 'cursor', 'opencode', 'codex', 'agy'],   # claude-code, cursor, opencode, universal (.agents/skills)
    'retr0h-agentpack': ['claude', 'codex', 'opencode', 'cursor', 'agy'],
    'ruler': ['claude', 'codex', 'opencode', 'cursor', 'agy'],
    'dotagents': ['claude', 'cursor', 'codex', 'grok', 'opencode'],
}

parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
parser.add_argument('--results', type=Path, default=HERE / 'ecosystem-results.json')
args = parser.parse_args()
data = json.loads(args.results.read_text())

out = {}
for run in data['runs'].values():
    tool = run['tool']
    if run.get('invalid'):
        continue
    t = out.setdefault(tool, {'skills_all_agents': [0, 0], 'skills_own_targets': [0, 0]})
    own = TARGETS.get(tool) or ALL
    for agent in ALL:
        cell = (run.get('cells') or {}).get(agent, {}).get('skill')
        if not cell or cell.get('status') in (None, 'n/a') or not cell.get('expected'):
            continue
        ok = cell['status'] in ('native', 'converted')
        t['skills_all_agents'][0] += ok
        t['skills_all_agents'][1] += 1
        if agent in own:
            t['skills_own_targets'][0] += ok
            t['skills_own_targets'][1] += 1

rows = sorted(out.items(), key=lambda kv: -kv[1]['skills_all_agents'][0])
print(f"{'tool':28} {'skills, 6 agents':>18} {'skills, own targets':>20}")
for tool, t in rows:
    a, b = t['skills_all_agents'], t['skills_own_targets']
    print(f"{tool:28} {a[0]:>8} of {a[1]:<7} {b[0]:>9} of {b[1]:<8}")
(HERE / (args.results.stem.replace('-results', '') + '-scope.json')).write_text(
    json.dumps({'source': args.results.name, 'targets': TARGETS, 'tools': out}, indent=1) + '\n')
