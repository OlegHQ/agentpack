#!/usr/bin/env -S uv run
# /// script
# requires-python = ">=3.11"
# dependencies = []
# ///
"""Merge the per-tool results of competitors_model.py runs and count what reached the model.

A combination is one agent and one kind of content (skill, command, subagent, rule, hook, MCP server, language
server where the agent has a language-server setting). It works when the agent answered with the item's token in
at least 2 of its 3 runs (for the language server, the token its hover returns) (the hook: when the hook's line was written in at least 2 of 3 runs of any item). Runs
whose transcript names the package source do not count. A later file's rows for a tool and agent replace an
earlier file's (used to rerun OpenCode alone, which hung when five tools used one account at once).

    uv run competitors_summary.py RESULT.json [RESULT.json ...] [--output competitors-model-results.json]
"""
import argparse, json, types
from pathlib import Path

HERE = Path(__file__).resolve().parent
AGENTS = ['claude', 'codex', 'opencode', 'cursor', 'grok', 'agy']
KINDS = ['skill', 'command', 'agent', 'rule', 'hook', 'mcp', 'lsp']
LSP_AGENTS = {'claude', 'opencode', 'grok'}

parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
parser.add_argument('results', nargs='+', type=Path)
parser.add_argument('--rescore', action='store_true',
                    help='decide every run again from its saved transcript (next to each result file) with the current rule')
parser.add_argument('--output', type=Path, default=HERE / 'competitors-model-results.json')
args = parser.parse_args()

# the rule itself is competitors_model.py's, so a rescore can never use a different one
src = (HERE / 'competitors_model.py').read_text()
rule = {'json': json}
exec(src[src.index('def source_is_installed'):src.index('def restore(')] + src[src.index('def final_answer'):src.index('def agent_runs')], rule)
cm = types.SimpleNamespace(**rule)


def rescore(res, run_dir):
    for r in res['rows']:
        attempt = 1 if r['artifact'] == 'command' and r.get('invoked_as') == 'request' else 0
        f = run_dir / res['tool'] / 'transcripts' / f"{r['agent']}-{r['artifact']}-{r['run']}-{attempt}.jsonl"
        if not f.is_file():
            continue
        text = f.read_text()
        touched = cm.read_source(text, str(run_dir / res['tool'] / 'repo'), res['tool'], r['agent'])
        r['read_source'] = touched
        r['delivered'] = res['tokens'][r['artifact']] in cm.final_answer(text) and not touched


rows, status, meta = {}, {}, {}
for f in args.results:
    for res in (json.loads(f.read_text()).get('results') or [json.loads(f.read_text())]):
        tool = res['tool']
        if args.rescore and res.get('rows'):
            rescore(res, f.parent)
        meta.setdefault(tool, {})['tokens_' + f.stem] = res.get('tokens')
        for agent, st in res.get('agents', {}).items():
            if st.get('status') == 'signed in' or (tool, agent) not in status:
                status[(tool, agent)] = st.get('status')
            if st.get('status') == 'signed in':
                rows[(tool, agent)] = [r for r in res['rows'] if r['agent'] == agent]

table = {}
for (tool, agent), rs in rows.items():
    cells = table.setdefault(tool, {}).setdefault(agent, {})
    for kind in KINDS:
        if kind == 'lsp' and agent not in LSP_AGENTS:
            continue
        if kind == 'hook':
            # one hook line per run; runs of every item count
            by_run = {}
            for r in rs:
                by_run[(r['artifact'], r['run'])] = r['hook_ran']
            ok, n = sum(by_run.values()), len(by_run)
        else:
            mine = [r for r in rs if r['artifact'] == kind]
            ok, n = sum(1 for r in mine if r['delivered']), len(mine)
        cells[kind] = {'runs': n, 'delivered': ok, 'works': n > 0 and ok * 3 >= n * 2, 'all_runs': n > 0 and ok == n}

summary = {}
for tool, agents in table.items():
    s = summary.setdefault(tool, {'combinations': 0, 'works': 0, 'all_runs': 0, 'agents_not_run': []})
    for agent in AGENTS:
        if agent not in agents:
            s['agents_not_run'].append(f"{agent}: {status.get((tool, agent))}")
            continue
        for cell in agents[agent].values():
            s['combinations'] += 1
            s['works'] += cell['works']
            s['all_runs'] += cell['all_runs']
args.output.write_text(json.dumps({'rule': __doc__.split('\n\n')[1], 'sources': [f.name for f in args.results],
                                   'summary': summary, 'table': table}, indent=1) + '\n')
for tool, s in sorted(summary.items(), key=lambda kv: -kv[1]['works']):
    print(f"{tool:14} works {s['works']:>2} of {s['combinations']}  (all three runs: {s['all_runs']})  not run: {s['agents_not_run']}")
for tool in sorted(table):
    print('==', tool)
    for agent, cells in table[tool].items():
        print('  ', agent.ljust(9), ' '.join(f"{k}:{c['delivered']}/{c['runs']}" for k, c in cells.items()))

# Table III of the paper: works / combinations per tool and agent
NAMES = {'agentpack': '\\tool{}', 'native': "Agents' own commands", 'apm': 'APM', 'rulesync': 'rulesync', 'vercel-skills': 'Vercel skills'}
rows_tex = []
for tool, s in sorted(summary.items(), key=lambda kv: -kv[1]['works']):
    cells = []
    for agent in AGENTS:
        c = table.get(tool, {}).get(agent)
        cells.append(f"{sum(x['works'] for x in c.values())}/{len(c)}" if c else '--')
    rows_tex.append(f"{NAMES.get(tool, tool)} & " + ' & '.join(cells) + f" & {s['works']}/{s['combinations']} \\\\")
(args.output.with_suffix('.tex')).write_text('\n'.join(rows_tex) + '\n')
print('\n'.join(rows_tex))
