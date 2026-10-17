#!/usr/bin/env -S uv run
# /// script
# requires-python = ">=3.11"
# dependencies = []
# ///
"""How much of an npx server's dependency tree a top-level pin leaves open, for the real servers of drift.py.

For each npm package that an official plugin starts through npx (drift-results.json), the version `latest`
names today is resolved to its full dependency tree with npm (`install --package-lock-only --ignore-scripts`,
as `agentpack lock` does). For every package in that tree, the registry's publish time of the resolved
version is compared with the publish time of the server version itself. A package whose resolved version was
published later is one that an install on the server's release day could not have received: it is what a
top-level pin leaves to the day of installation. The script also counts the packages whose version is a
range in their parent's dependencies (any spec other than an exact version), the ones npm may move.

    uv run treedrift.py [--output treedrift-results.json]

Needs npm and network access to registry.npmjs.org; no package is installed and no package script runs.
"""
import argparse, concurrent.futures, json, subprocess, tempfile, time, urllib.request
from pathlib import Path

HERE = Path(__file__).resolve().parent
parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
parser.add_argument('--drift', type=Path, default=HERE / 'drift-results.json')
parser.add_argument('--output', type=Path, default=HERE / 'treedrift-results.json')
args = parser.parse_args()
REGISTRY = 'https://registry.npmjs.org'


def packument(name):
    url = f"{REGISTRY}/{name.replace('/', '%2F')}"
    for attempt in range(4):
        try:
            with urllib.request.urlopen(urllib.request.Request(url, headers={'Accept': 'application/json'}), timeout=60) as r:
                return json.load(r)
        except Exception:
            time.sleep(2 * (attempt + 1))
    raise RuntimeError(f'could not read {url}')


def resolve(name, version):
    with tempfile.TemporaryDirectory() as d:
        Path(d, 'package.json').write_text(json.dumps({'name': 'tree', 'private': True, 'dependencies': {name: version}}))
        subprocess.run(['npm', 'install', '--package-lock-only', '--ignore-scripts', '--no-audit', '--no-fund', '--registry', REGISTRY],
                       cwd=d, check=True, capture_output=True)
        return json.loads(Path(d, 'package-lock.json').read_text())['packages']


def exact(spec):
    parts = spec.split('.')
    return len(parts) == 3 and all(p.split('-')[0].split('+')[0].isdigit() for p in parts)


rows = []
names = sorted({s['package'] for s in json.loads(args.drift.read_text())['servers']})
for name in names:
    root_doc = packument(name)
    version = root_doc['dist-tags']['latest']
    released = root_doc['time'][version]
    lock = resolve(name, version)
    entries = {k: v for k, v in lock.items() if k.startswith('node_modules/') and not v.get('dev')}
    pkg_names = sorted({k.rsplit('node_modules/', 1)[1] for k in entries})
    with concurrent.futures.ThreadPoolExecutor(max_workers=16) as pool:
        times = dict(zip(pkg_names, pool.map(lambda n: packument(n).get('time', {}), pkg_names)))
    later, ranged = [], 0
    for key, entry in entries.items():
        pkg = key.rsplit('node_modules/', 1)[1]
        if pkg == name:
            continue
        published = times.get(pkg, {}).get(entry['version'])
        if published and published > released:
            later.append({'package': pkg, 'version': entry['version'], 'published': published})
    for key, entry in lock.items():
        for dep, spec in (entry.get('dependencies') or {}).items():
            if not exact(spec):
                ranged += 1
    rows.append({'package': name, 'version': version, 'released': released, 'tree_packages': len(entries),
                 'dependency_specs_that_are_ranges': ranged,
                 'published_after_the_server_version': len(later), 'examples': sorted(later, key=lambda x: x['published'])[-5:]})
    print(f"{name}@{version} (released {released[:10]}): {len(entries)} packages, {len(later)} published after it, "
          f"{ranged} range specs", flush=True)

out = {'date': time.strftime('%Y-%m-%d'), 'registry': REGISTRY,
       'npm': subprocess.run(['npm', '--version'], capture_output=True, text=True).stdout.strip(), 'servers': rows}
args.output.write_text(json.dumps(out, indent=1) + '\n')
