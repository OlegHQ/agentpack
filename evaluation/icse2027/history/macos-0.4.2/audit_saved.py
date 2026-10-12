#!/usr/bin/env python3
"""Audit the frozen 2026-10-11 macOS record after the fact.

That run asserted exit status only. This script checks that each saved fault row
also carries the named finding and requirement result that faults.py now asserts,
and recomputes the lock-hash control and the seven timing medians from the rows.
It reads comparative-results.json; it executes nothing.
"""
import json, statistics, sys
from pathlib import Path

d = json.loads((Path(__file__).resolve().parent / 'comparative-results.json').read_text())
rows = {r['case']: r for r in d['rows'] if r.get('tool') == 'agentpack'}
control = {r['case']: r for r in d['rows'] if r.get('tool') == 'package_hash_control'}
EXPECTED = {  # case: (exit, finding present, finding absent, requirement result)
    'missing_native_receipt': (4, 'OBSERVATION_UNAVAILABLE', None, ('native-sentinel', 'unknown')),
    'unsupported_workspace_overlay': (3, 'WORKSPACE_WRITE_REQUIRED', None, ('no-checkout-config', 'violated')),
    'inherited_user_config': (3, 'INHERITED_CONFIG', None, None),
    'named_artifact_mismatch': (3, 'CONTRACT_MISMATCH', None, ('named', 'violated')),
    'malformed_complete_receipt': (5, 'RECEIPT_STALE', None, ('native-sentinel', 'unknown')),
    'stale_receipt_workspace_edit': (4, 'RECEIPT_STALE', None, ('controlled-skill', 'unknown')),
    'missing_diagnostic_evidence': (5, 'OBSERVATION_UNAVAILABLE', 'RECEIPT_STALE', ('controlled-skill', 'unknown')),
    'clean_no_checkout_write': (0, None, None, ('no-checkout-config', 'satisfied')),
    'valid_native_receipt_clean': (0, 'NATIVE_OBSERVED', None, ('controlled-skill', 'satisfied')),
    'restored_receipt_clean': (0, 'NATIVE_OBSERVED', None, ('controlled-skill', 'satisfied')),
    'observed_contract_clean': (0, 'NATIVE_OBSERVED', None, ('controlled-skill', 'satisfied')),
}
ok = True
for case, (code, present, absent, req) in EXPECTED.items():
    r = rows[case]
    codes = r.get('finding_codes', [])
    results = {c['id']: c['result'] for c in r.get('contract_results', [])}
    good = (r['exit'] == code and (present is None or present in codes) and (absent is None or absent not in codes)
            and (req is None or results.get(req[0]) == req[1]) and not r['checkout_changed']
            and control[case]['lock_changed'] is False)
    ok &= good
    print(f"{'ok  ' if good else 'FAIL'} {case:32} exit {r['exit']} lock_changed={control[case]['lock_changed']}")
for s in d['summary']:
    ts = [r['elapsed_ms'] for r in d['rows'] if r.get('tool') == s['tool'] and r.get('case', '').startswith(s['task'] + '_')]
    good = len(ts) == 7 and abs(statistics.median(ts) - s['median_ms']) < 1e-9
    ok &= good
    print(f"{'ok  ' if good else 'FAIL'} median {s['tool']}:{s['task']} n={len(ts)} {statistics.median(ts):.1f} ms")
sys.exit(0 if ok else 1)
