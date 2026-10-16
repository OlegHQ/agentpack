#!/usr/bin/env -S uv run
# /// script
# requires-python = ">=3.11"
# dependencies = []
# ///
"""Draw a held-out sample of published plugins for the comparison of ecosystem.py.

The nine inputs of ecosystem.py were chosen by the authors, and the 40 packages of plugins.py were
used during development. This draw is made after the code was frozen (release 0.5.2, 2026-10-16) by
a fixed rule, and excludes every package used before:

  - sources: the three official plugin repositories at the commits pinned in ecosystem.py
    (Claude Code: plugins/ and external_plugins/; Cursor: top-level plugin directories and
    third_party/; Codex: plugins/);
  - eligible: a directory with the vendor's plugin manifest that holds at least one item of the six
    kinds ecosystem.py counts (skill, command, subagent, rule, hook, MCP server), as found by
    ecosystem.expected(); plugins with only a language server give no cell and are not eligible;
  - excluded: the nine inputs of ecosystem.py and the 40 packages of plugins-corpus.json;
  - order: sha256(SEED + repository + path), ascending; the first N per source are taken.

    uv run heldout.py --src DIR [--per-source 4] [--out heldout-inputs.json]

The output holds one ecosystem.py input per package (same fields as INPUTS) and the rule above.
Run the comparison with:  uv run ecosystem.py run --inputs-file heldout-inputs.json --output heldout-results.json ...
"""
import argparse, hashlib, json, sys
from pathlib import Path

HERE = Path(__file__).resolve().parent
sys.path.insert(0, str(HERE))
import ecosystem as eco  # noqa: E402

SEED = 'icse2027-heldout-2026-10-16'
SOURCES = [
    dict(name='claude', repo='anthropics/claude-plugins-official', commit=eco.OFFICIAL[1], dirs=['plugins', 'external_plugins'],
         manifest='.claude-plugin/plugin.json', format='claude-plugin', marketplace='claude-plugins-official'),
    dict(name='cursor', repo='cursor/plugins', commit='1dcc2bacde73ddd284803b7af28164d0436198fc', dirs=['', 'third_party'],
         manifest='.cursor-plugin/plugin.json', format='cursor-plugin', marketplace='cursor-plugins'),
    dict(name='codex', repo='openai/plugins', commit='0722921d5542fc593105c27bd52630babd8b8c2a', dirs=['plugins'],
         manifest='.codex-plugin/plugin.json', format='codex-plugin', marketplace='openai-curated'),
]


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument('--src', type=Path, required=True, help='directory for the pinned source checkouts (as ecosystem.py --work/src)')
    ap.add_argument('--per-source', type=int, default=4)
    ap.add_argument('--out', type=Path, default=HERE / 'heldout-inputs.json')
    a = ap.parse_args()
    used = {(i.get('content', i)['repo'], i.get('content', i)['path']) for i in eco.INPUTS.values()}
    used |= {(p['repo'], p['path']) for p in json.loads((HERE / 'plugins-corpus.json').read_text())['packages']}
    inputs, log = {}, []
    for s in SOURCES:
        root = eco.checkout(a.src, s['repo'], s['commit'])
        cands = []
        for d in s['dirs']:
            base = root / d if d else root
            for p in sorted(x for x in base.iterdir() if x.is_dir() and (x / s['manifest']).exists()):
                rel = str(p.relative_to(root))
                if (s['repo'], rel) in used:
                    continue
                cands.append(rel)
        cands.sort(key=lambda rel: hashlib.sha256(f"{SEED}|{s['repo']}|{rel}".encode()).hexdigest())
        taken = 0
        for rel in cands:
            if taken >= a.per_source:
                break
            name = json.loads((root / rel / s['manifest']).read_text()).get('name') or Path(rel).name
            inp = dict(what=f"held-out {s['format']} {rel}", repo=s['repo'], commit=s['commit'], path=rel, format=s['format'],
                       plugin=name, marketplace=dict(repo=s['repo'], name=s['marketplace']))
            exp = eco.expected(a.src, inp)
            n = {k: len(v) for k, v in exp.items() if v}
            log.append({'source': s['name'], 'path': rel, 'items': n, 'taken': bool(n)})
            if not n:
                continue
            inputs[f"heldout-{s['name']}-{eco.norm(Path(rel).name)}"] = inp
            taken += 1
        print(f"{s['name']}: {len(cands)} eligible directories after exclusions, {taken} taken", file=sys.stderr)
    a.out.write_text(json.dumps({'rule': __doc__.split('    uv run')[0].strip(), 'seed': SEED, 'per_source': a.per_source,
                                 'drawn_in_order': log, 'inputs': inputs}, indent=1))
    for k, v in inputs.items():
        print(k, v['path'], file=sys.stderr)
    return 0


if __name__ == '__main__':
    sys.exit(main())
