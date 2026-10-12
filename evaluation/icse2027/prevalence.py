#!/usr/bin/env python3
"""How often do public repositories carry a project skill named like a widely pinned one?

For every skill in anthropics/skills at the pinned commit, GitHub code search is asked for
SKILL.md files at <root>/<name>/SKILL.md in other repositories, for three project skill roots.
Each hit's blob is compared with every version of the upstream SKILL.md in the upstream
history: equal to the pinned version, equal to an older upstream version, or different from
all of them. Needs `gh` (authenticated) and `git`. Code search is rate limited; the script
sleeps between requests. Counts are what GitHub's index returned on the day of the run and
cover at most the first 100 hits per query.
"""
import json, subprocess, sys, tempfile, time
from pathlib import Path

UPSTREAM, PIN = 'anthropics/skills', 'dbd4588f9e1033efb41dad4bef2f7947c8993d44'
ROOTS = ['.claude/skills', '.agents/skills', '.codex/skills']
out = Path(sys.argv[1]) if len(sys.argv) > 1 else Path(__file__).resolve().parent / 'prevalence-results.json'


def git(repo, *args):
    return subprocess.check_output(['git', '-C', str(repo), *args], text=True)


with tempfile.TemporaryDirectory() as td:
    repo = Path(td) / 'upstream'
    subprocess.run(['git', 'clone', '-q', f'https://github.com/{UPSTREAM}.git', str(repo)], check=True)
    git(repo, 'checkout', '-q', PIN)
    names = sorted(p.parent.name for p in (repo / 'skills').glob('*/SKILL.md'))
    versions = {}
    for name in names:
        path = f'skills/{name}/SKILL.md'
        commits = git(repo, 'log', '--follow', '--format=%H', PIN, '--', path).split()
        # Every SKILL.md blob these commits touched, at any earlier path of the skill.
        blobs = set()
        for c in commits:
            for line in git(repo, 'ls-tree', '-r', c).splitlines():
                meta, file = line.split('\t', 1)
                if file.endswith(f'/{name}/SKILL.md') or file == f'{name}/SKILL.md':
                    blobs.add(meta.split()[2])
        versions[name] = {'pinned': git(repo, 'rev-parse', f'{PIN}:{path}').strip(), 'all': sorted(blobs)}

rows = []
for root in ROOTS:
    for name in names:
        query = f'path:{root}/{name} filename:SKILL.md'
        for attempt in range(6):
            p = subprocess.run(['gh', 'api', '-X', 'GET', 'search/code', '-f', f'q={query}', '-f', 'per_page=100'],
                               capture_output=True, text=True)
            if p.returncode == 0:
                break
            time.sleep(30)
        else:
            rows.append({'root': root, 'skill': name, 'error': p.stderr.strip()[:200]})
            continue
        data = json.loads(p.stdout)
        hits = [i for i in data['items'] if i['path'] == f'{root}/{name}/SKILL.md'
                and i['repository']['full_name'].lower() != UPSTREAM]
        repos = {i['repository']['full_name'] for i in hits}
        kinds = {'equal_to_pinned': 0, 'equal_to_older_upstream': 0, 'differs_from_every_upstream_version': 0}
        files = []
        for i in hits:
            kind = ('equal_to_pinned' if i['sha'] == versions[name]['pinned'] else
                    'equal_to_older_upstream' if i['sha'] in versions[name]['all'] else
                    'differs_from_every_upstream_version')
            kinds[kind] += 1
            files.append({'repository': i['repository']['full_name'], 'blob': i['sha'], 'kind': kind})
        rows.append({'root': root, 'skill': name, 'github_total_count': data['total_count'],
                     'exact_path_hits_in_first_100': len(hits), 'repositories': len(repos), **kinds,
                     'capped': data['total_count'] > 100, 'files': files})
        print({k: v for k, v in rows[-1].items() if k != 'files'}, flush=True)
        time.sleep(7)

totals = {}
for root in ROOTS:
    rs = [r for r in rows if r['root'] == root and 'error' not in r]
    totals[root] = {k: sum(r[k] for r in rs) for k in ('exact_path_hits_in_first_100', 'equal_to_pinned',
                    'equal_to_older_upstream', 'differs_from_every_upstream_version')}
    totals[root]['skills_with_at_least_one_hit'] = sum(r['exact_path_hits_in_first_100'] > 0 for r in rs)
all_files = [f for r in rows for f in r.get('files', [])]
totals['all_roots'] = {'files': len(all_files), 'distinct_repositories': len({f['repository'] for f in all_files}),
                       'capped_queries': sum(r.get('capped', False) for r in rows), 'queries': len(rows),
                       **{k: sum(f['kind'] == k for f in all_files) for k in
                          ('equal_to_pinned', 'equal_to_older_upstream', 'differs_from_every_upstream_version')}}
out.write_text(json.dumps({'date': time.strftime('%Y-%m-%d'), 'upstream': UPSTREAM, 'pin': PIN, 'skills': names,
                           'upstream_versions_per_skill': {n: len(v['all']) for n, v in versions.items()},
                           'totals': totals, 'rows': rows}, indent=1) + '\n')
print(json.dumps(totals, indent=1))
