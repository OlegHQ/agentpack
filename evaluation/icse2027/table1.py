#!/usr/bin/env -S uv run
# /// script
# requires-python = ">=3.11"
# dependencies = []
# ///
"""Print the rows of the paper's Table I (what each tool delivers) from ecosystem-results.json, so that no number
in the table is typed by hand.

    uv run table1.py [--results ecosystem-results.json]
"""
import argparse, json
from pathlib import Path

HERE = Path(__file__).resolve().parent
KINDS = ['skill', 'command', 'agent', 'rule', 'hook', 'mcp']
ROWS = [  # tool key, label in the paper (citation keys as in the bibliography)
    ('agentpack', '\\tool{} 0.6.1'),
    ('apm', 'Microsoft APM 0.33.0~\\cite{apm}'),
    ('rulesync', 'rulesync 33.0.0~\\cite{rulesync}'),
    ('ruler', 'Ruler 0.3.44~\\cite{ruler}$^{a}$'),
    ('native', "Agents' own install commands$^{b}$"),
    ('openpackage', 'OpenPackage 0.11.3~\\cite{skilltools}$^{f}$'),
    ('vercel-skills', 'Vercel skills 1.7.2~\\cite{vercel}'),
    ('skills-lock', 'skills-lock 0.1.0~\\cite{skilltools}'),
    ('skillpm', 'skillpm 4.0.0~\\cite{skilltools}'),
    ('liqiongyu-agentpack', 'liqiongyu/agentpack$^{c}$ 0.9.1~\\cite{skilltools}'),
    ('openskills', 'openskills 1.5.0~\\cite{skilltools}'),
    ('dotagents', 'dotagents 3.3.0~\\cite{skilltools}'),
    ('skillshare', 'skillshare 0.25.10~\\cite{skilltools}'),
    ('retr0h-agentpack', 'retr0h/agentpack$^{c}$ 1.1.0~\\cite{skilltools}'),
]

ap = argparse.ArgumentParser()
ap.add_argument('--results', type=Path, default=HERE / 'ecosystem-results.json')
a = ap.parse_args()
d = json.loads(a.results.read_text())
per = {}
for r in d['runs'].values():
    t = per.setdefault(r['tool'], {k: 0 for k in KINDS})
    for cells in r['cells'].values():
        for k in KINDS:
            st = str(cells[k].get('status'))
            if st == 'native' or st.startswith('converted'):
                t[k] += 1
for key, label in ROWS:
    s = d['summary'][key]
    c, lock = s['cells'], s['lockfile']
    commit, digest = lock.get('inputs_where_it_records_the_pinned_commit', 0), lock.get('inputs_where_it_records_a_content_hash', 0)
    lk = 'none' if not lock.get('inputs_with_a_lockfile') else f'{commit} / {digest}'
    files = s['writes']['project_files_max']
    print(f"{label} & {s['inputs_accepted_as_published']} & " + ' & '.join(str(per[key][k]) for k in KINDS)
          + f" & {c['native']} & {c['converted']} & {lk} & {files:,}".replace(',', '{,}') + ' \\\\')
