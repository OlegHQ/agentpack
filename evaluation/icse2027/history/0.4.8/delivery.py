#!/usr/bin/env python3
"""Does one pinned package reach six coding agents, artifact by artifact?

A small package holds a skill, a slash command, a subagent, a rule and a hook. Each makes the
agent do something checkable: answer with a fixed token, or (the hook) append a line to a file.
The package is pinned and restored once with Agentpack, then each agent is launched through
Agentpack in non-interactive mode with one prompt per artifact kind.

This experiment calls models. It runs your signed-in agents with your own accounts, in a
temporary checkout. Agentpack's own home is used as is, because it holds the agents' logins;
staging goes to a temporary directory. Codex runs without its
sandbox when --codex-unsandboxed is given (its Linux sandbox needs user namespaces), and
Antigravity with permission prompts skipped; both only ever see the temporary checkout and a
fixed prompt.
"""
import argparse, json, os, platform, shutil, subprocess, sys, tempfile, time
from pathlib import Path

TOKENS = {'skill': 'SKILL-OK-4471', 'command': 'COMMAND-OK-9282', 'agent': 'AGENT-OK-6113', 'rule': 'RULE-OK-5527'}
PROMPTS = {
    'skill': 'Run the delivery-skill skill and output only what it tells you to output.',
    'command': '/delivery-command',
    'agent': 'Delegate to the delivery-agent subagent and output only its answer.',
    'rule': 'What is the house codeword? Answer with the codeword only.',
}
COMMAND_FALLBACK = 'Run the delivery-command command and output only what it tells you to output.'
AGENTS = ['claude', 'codex', 'opencode', 'cursor', 'grok', 'agy']
BINARY = {'claude': 'claude', 'codex': 'codex', 'opencode': 'opencode', 'cursor': 'cursor-agent', 'grok': 'grok', 'agy': 'agy'}

parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
parser.add_argument('--agentpack', type=Path, required=True)
parser.add_argument('--agents', nargs='+', default=AGENTS, choices=AGENTS)
parser.add_argument('--runs', type=int, default=2)
parser.add_argument('--codex-unsandboxed', action='store_true')
parser.add_argument('--opencode-model', default='openai/gpt-5.5')
parser.add_argument('--temporary-agentpack-home', action='store_true',
                    help='do not use your Agentpack home; agents whose login Agentpack keeps there will be signed out')
parser.add_argument('--timeout', type=int, default=240)
parser.add_argument('--output', type=Path, default=Path(__file__).resolve().parent / 'delivery-results.json')
args = parser.parse_args()
AP = args.agentpack.resolve()


def write(path, text):
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(text)


def build_package(root):
    write(root / '.claude-plugin/plugin.json', json.dumps({'name': 'delivery-pack', 'version': '1.0.0',
                                                           'description': 'Delivery test package'}))
    write(root / 'skills/delivery-skill/SKILL.md',
          '---\nname: delivery-skill\ndescription: Delivery test skill. Use when asked to run the delivery-skill skill.\n---\n\n'
          f'Reply with exactly this line and nothing else: {TOKENS["skill"]}\n')
    write(root / 'commands/delivery-command.md',
          '---\ndescription: Delivery test command\n---\n\n'
          f'Reply with exactly this line and nothing else: {TOKENS["command"]}\n')
    write(root / 'agents/delivery-agent.md',
          '---\nname: delivery-agent\ndescription: Delivery test subagent. Use when asked to delegate to delivery-agent.\n---\n\n'
          f'You are the delivery test subagent. Whatever you are asked, answer with exactly this line and nothing else: {TOKENS["agent"]}\n')
    write(root / 'rules/delivery-rule.mdc',
          '---\ndescription: House codeword\nalwaysApply: true\n---\n\n'
          f'The house codeword is {TOKENS["rule"]}. When asked for the house codeword, answer with it.\n')
    write(root / 'hooks/hooks.json', json.dumps({'hooks': {'UserPromptSubmit': [{'hooks': [
        {'type': 'command', 'command': 'sh -c \'echo hook-ran >> "$DELIVERY_HOOK_MARK"\''}]}]}}, indent=1))


def launch(agent, prompt):
    if agent == 'claude':
        return ['claude', '-p', prompt]
    if agent == 'codex':
        sandbox = 'danger-full-access' if args.codex_unsandboxed else 'read-only'
        # Codex runs a hook only after a person has reviewed it; this flag skips that review for one run.
        return ['codex', 'exec', '--skip-git-repo-check', '--sandbox', sandbox, '--dangerously-bypass-hook-trust', prompt]
    if agent == 'opencode':
        if prompt == PROMPTS['command']:
            # OpenCode's non-interactive mode takes a command by option, not as a slash prompt.
            return ['opencode', 'run', '-m', args.opencode_model, '--command', 'delivery-command', '']
        return ['opencode', 'run', '-m', args.opencode_model, prompt]
    if agent == 'cursor':
        return ['agent', '-p', prompt, '--force']
    if agent == 'grok':
        return ['grok', '-p', prompt]
    return ['agy', '-p', prompt, '--dangerously-skip-permissions']


rows = []
with tempfile.TemporaryDirectory(prefix='agentpack-delivery-') as td:
    base = Path(td).resolve()
    package, definition, ws = base / 'delivery-pack', base / 'definition', base / 'checkout'
    build_package(package)
    ws.mkdir()
    subprocess.run(['git', 'init', '-q', str(ws)], check=True)
    (ws / 'README.md').write_text('Benign project\n')
    mark = base / 'hook-mark.txt'
    # Staged hooks call `agentpack hook-exec`, so the binary under test must be the one on PATH.
    (base / 'bin').mkdir()
    (base / 'bin/agentpack').symlink_to(AP)
    env = {**os.environ, 'PATH': f"{base / 'bin'}{os.pathsep}{os.environ['PATH']}",
           'AGENTPACK_STAGING_ROOT': str(base / 'staging'), 'DELIVERY_HOOK_MARK': str(mark)}
    if args.temporary_agentpack_home:
        env['AGENTPACK_HOME'] = str(base / 'managed')
    sel = [str(AP), '--env', str(definition), '--workspace', str(ws)]

    def ap(*cmd, timeout=300):
        return subprocess.run([*sel, *cmd] if cmd[0] != 'env-init' else [str(AP), 'env', 'init', '--dir', str(definition), '--name', 'delivery'],
                              cwd=ws, env=env, capture_output=True, text=True, timeout=timeout, stdin=subprocess.DEVNULL)

    assert ap('env-init').returncode == 0
    added = ap('add', str(package), '--no-sync')
    assert added.returncode == 0, added.stderr
    restored = ap('env', 'restore')
    assert restored.returncode == 0, restored.stderr
    lock = (definition / 'pack.lock').read_bytes()

    for agent in args.agents:
        if not shutil.which(BINARY[agent]):
            rows.append({'agent': agent, 'status': 'not installed'})
            continue
        check = ap(*launch(agent, 'Reply with the single word PONG'), timeout=args.timeout)
        if 'PONG' not in check.stdout + check.stderr:
            rows.append({'agent': agent, 'status': 'could not answer a plain prompt',
                         'detail': (check.stdout + check.stderr).strip()[-160:]})
            print(f'{agent}: could not answer a plain prompt, skipped', flush=True)
            continue
        version = subprocess.run([BINARY[agent], '--version'], capture_output=True, text=True, timeout=30).stdout.strip().splitlines()[:1]
        for kind, prompt in PROMPTS.items():
            for run in range(args.runs):
                mark.unlink(missing_ok=True)
                attempts = [prompt] + ([COMMAND_FALLBACK] if kind == 'command' else [])
                outcome = {'agent': agent, 'version': version[0] if version else '', 'artifact': kind, 'run': run}
                for attempt, text in enumerate(attempts):
                    start = time.perf_counter()
                    try:
                        p = ap(*launch(agent, text), timeout=args.timeout)
                        out, code = p.stdout + '\n' + p.stderr, p.returncode
                    except subprocess.TimeoutExpired:
                        out, code = 'TIMEOUT', -1
                    delivered = TOKENS[kind] in out
                    outcome.update(delivered=delivered, exit=code, seconds=round(time.perf_counter() - start, 1),
                                   invoked_as='slash' if kind == 'command' and attempt == 0 else 'request',
                                   hook_ran=mark.exists(), answer_tail=out.strip()[-160:])
                    if delivered:
                        break
                rows.append(outcome)
                print(agent, kind, run, 'delivered' if outcome['delivered'] else 'NOT delivered',
                      'hook' if outcome['hook_ran'] else 'no-hook', outcome['seconds'], flush=True)
    lock_unchanged = (definition / 'pack.lock').read_bytes() == lock
    checkout_files = sorted(str(p.relative_to(ws)) for p in ws.rglob('*') if '.git' not in p.parts and p.is_file())

matrix = {}
for row in rows:
    if 'artifact' not in row:
        continue
    cell = matrix.setdefault(row['agent'], {}).setdefault(row['artifact'], {'delivered': 0, 'runs': 0})
    cell['runs'] += 1
    cell['delivered'] += row['delivered']
    hook = matrix[row['agent']].setdefault('hook', {'delivered': 0, 'runs': 0})
    hook['runs'] += 1
    hook['delivered'] += row['hook_ran']
args.output.write_text(json.dumps({
    'date': time.strftime('%Y-%m-%d'), 'platform': platform.platform(),
    'agentpack_version': subprocess.check_output([str(AP), '--version'], text=True).strip(),
    'model_inference_requested': True, 'runs_per_cell': args.runs, 'lock_unchanged': lock_unchanged,
    'files_left_in_checkout': checkout_files, 'matrix': matrix, 'rows': rows}, indent=1) + '\n')
print(json.dumps(matrix, indent=1))
print('files left in checkout:', checkout_files)
