#!/usr/bin/env -S uv run
# /// script
# requires-python = ">=3.11"
# dependencies = []
# ///
"""Does one pinned package reach six coding agents, artifact by artifact?

A small package holds a skill, a slash command, a subagent, a rule, a hook and an MCP server. Each
makes the agent do something checkable: answer with a fixed token, or (the hook) append a line to a
file. The MCP server is a local stdio server shipped inside the package (mcp/server.mjs, Node, no
dependency, no network), declared once in the package's .mcp.json. It has one tool that returns a
token which is nowhere in the package as text; the server also appends a line to a file next to
itself for every tools/call it receives. An MCP run succeeds when the token comes back in the
agent's answer; whether the server recorded the call is stored as well.

    uv run delivery.py --agentpack PATH --agentpack-sha256 HEX --runs 3 --codex-unsandboxed

The package is pinned and restored once with Agentpack, then each agent is launched through
Agentpack in non-interactive mode with one prompt per artifact kind.

This experiment calls models. It runs your signed-in agents with your own accounts, in a
temporary checkout. Agentpack's own home is used as is, because it holds the agents' logins;
staging goes to a temporary directory. Codex runs without its
sandbox when --codex-unsandboxed is given (its Linux sandbox needs user namespaces), and
Antigravity with permission prompts skipped; both only ever see the temporary checkout and a
fixed prompt.
"""
import argparse, hashlib, json, os, platform, shutil, subprocess, sys, tempfile, time
from pathlib import Path

TOKENS = {'skill': 'SKILL-OK-4471', 'command': 'COMMAND-OK-9282', 'agent': 'AGENT-OK-6113', 'rule': 'RULE-OK-5527',
          'mcp': 'MCP-OK-' + str(7000 + 735), 'lsp': 'LSP-OK-' + str(8000 + 246)}
MCP_SERVER = r"""// Minimal MCP server over stdio (newline-delimited JSON-RPC 2.0). One tool, no dependency, no network.
import { appendFileSync } from 'node:fs';
import { createInterface } from 'node:readline';
const log = new URL('./calls.log', import.meta.url);
const send = (m) => process.stdout.write(JSON.stringify(m) + '\n');
const token = () => ['MCP', 'OK', String(7000 + 735)].join('-');
createInterface({ input: process.stdin }).on('line', (line) => {
  let m; try { m = JSON.parse(line); } catch { return; }
  if (m.id === undefined) return;                       // notifications need no answer
  const ok = (result) => send({ jsonrpc: '2.0', id: m.id, result });
  if (m.method === 'initialize') return ok({ protocolVersion: m.params?.protocolVersion ?? '2024-11-05',
    capabilities: { tools: {} }, serverInfo: { name: 'delivery-mcp', version: '1.0.0' } });
  if (m.method === 'ping') return ok({});
  if (m.method === 'tools/list') return ok({ tools: [{ name: 'delivery_token',
    description: 'Returns the delivery test token.', inputSchema: { type: 'object', properties: {} } }] });
  if (m.method === 'tools/call' && m.params?.name === 'delivery_token') {
    try { appendFileSync(log, new Date().toISOString() + ' tools/call delivery_token\n'); } catch {}
    return ok({ content: [{ type: 'text', text: token() }], isError: false });
  }
  send({ jsonrpc: '2.0', id: m.id, error: { code: -32601, message: 'method not found: ' + m.method } });
});
"""
LSP_SERVER = r"""// Minimal language server over stdio (LSP base protocol: Content-Length framing). No dependency, no network.
// It records every request and notification it receives in lsp-calls.log next to itself and answers a hover
// with a token that is nowhere in the package as text.
import { appendFileSync } from 'node:fs';
const log = new URL('./lsp-calls.log', import.meta.url);
const note = (s) => { try { appendFileSync(log, new Date().toISOString() + ' ' + s + '\n'); } catch {} };
const token = () => ['LSP', 'OK', String(8000 + 246)].join('-');
const send = (m) => { const b = Buffer.from(JSON.stringify(m)); process.stdout.write('Content-Length: ' + b.length + '\r\n\r\n'); process.stdout.write(b); };
let buf = Buffer.alloc(0);
process.stdin.on('data', (chunk) => {
  buf = Buffer.concat([buf, chunk]);
  for (;;) {
    const end = buf.indexOf('\r\n\r\n'); if (end < 0) return;
    const len = Number(/Content-Length: *(\d+)/i.exec(buf.subarray(0, end).toString())?.[1] ?? -1);
    if (len < 0 || buf.length < end + 4 + len) return;
    const m = JSON.parse(buf.subarray(end + 4, end + 4 + len).toString()); buf = buf.subarray(end + 4 + len);
    note(m.method + (m.params?.textDocument?.uri ? ' ' + m.params.textDocument.uri : ''));
    if (m.id === undefined) { if (m.method === 'exit') process.exit(0); continue; }
    const ok = (result) => send({ jsonrpc: '2.0', id: m.id, result });
    if (m.method === 'initialize') ok({ capabilities: { textDocumentSync: 1, hoverProvider: true }, serverInfo: { name: 'delivery-lsp' } });
    else if (m.method === 'textDocument/hover') ok({ contents: { kind: 'plaintext', value: token() } });
    else if (m.method === 'shutdown') ok(null);
    else ok(null);
  }
});
"""
PROMPTS = {
    'skill': 'Run the delivery-skill skill and output only what it tells you to output.',
    'command': '/delivery-command',
    'agent': 'Delegate to the delivery-agent subagent and output only its answer.',
    'rule': 'What is the house codeword? Answer with the codeword only.',
    'lsp': 'Use the language server (LSP) hover on the word "probe" on line 1 of sample.dlv and output only the hover text it returns. '
           'If no language server is available for that file, say so.',
    'mcp': 'Call the delivery_token tool of the delivery-mcp MCP server and output only the text it returns. '
           'Do not read any file. If the tool is not available, say so.',
}
COMMAND_FALLBACK = 'Run the delivery-command command and output only what it tells you to output.'
AGENTS = ['claude', 'codex', 'opencode', 'cursor', 'grok', 'agy']
# Codex has no language server setting; Cursor and Antigravity run only their built-in servers
LSP_AGENTS = ['claude', 'opencode', 'grok']
BINARY = {'claude': 'claude', 'codex': 'codex', 'opencode': 'opencode', 'cursor': 'cursor-agent', 'grok': 'grok', 'agy': 'agy'}

parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
parser.add_argument('--agentpack', type=Path, required=True)
parser.add_argument('--agentpack-sha256', required=True, help='sha256 the binary must have; recorded in the results')
parser.add_argument('--kinds', nargs='+', default=list(PROMPTS), choices=list(PROMPTS))
parser.add_argument('--agents', nargs='+', default=AGENTS, choices=AGENTS)
parser.add_argument('--runs', type=int, default=2)
parser.add_argument('--codex-unsandboxed', action='store_true')
# On 2026-10-14 the ChatGPT account refused gpt-5.4, gpt-5.5 and gpt-5.6 through OpenCode ("not supported when using
# Codex with a ChatGPT account"); gpt-6.1-sol answered.
parser.add_argument('--opencode-model', default='openai/gpt-6.1-sol')
parser.add_argument('--temporary-agentpack-home', action='store_true',
                    help='do not use your Agentpack home; agents whose login Agentpack keeps there will be signed out')
parser.add_argument('--timeout', type=int, default=240)
parser.add_argument('--output', type=Path, default=Path(__file__).resolve().parent / 'delivery-results.json')
args = parser.parse_args()
AP = args.agentpack.resolve()
AP_SHA = hashlib.sha256(AP.read_bytes()).hexdigest()
if AP_SHA != args.agentpack_sha256.lower():
    sys.exit(f'--agentpack has sha256 {AP_SHA}, which is not --agentpack-sha256')


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
    write(root / 'mcp/server.mjs', MCP_SERVER)
    write(root / '.mcp.json', json.dumps({'mcpServers': {'delivery-mcp': {
        'command': 'node', 'args': ['${CLAUDE_PLUGIN_ROOT}/mcp/server.mjs']}}}, indent=1))
    write(root / 'lsp/server.mjs', LSP_SERVER)
    write(root / '.lsp.json', json.dumps({'delivery-lsp': {
        'command': 'node', 'args': ['${CLAUDE_PLUGIN_ROOT}/lsp/server.mjs'], 'extensionToLanguage': {'.dlv': 'delivery'}}}, indent=1))
    write(root / 'hooks/hooks.json', json.dumps({'hooks': {'UserPromptSubmit': [{'hooks': [
        {'type': 'command', 'command': 'sh -c \'echo hook-ran >> "$DELIVERY_HOOK_MARK"\''}]}]}}, indent=1))


# What each agent needs, beyond the launch used for the other kinds, to call an MCP tool without a
# person at the keyboard. Used for the MCP runs only. Each is the agent's own documented option.
MCP_OPTIONS = {
    'claude': ['--allowedTools', 'mcp__plugin_agentpack-bundle_delivery-mcp__delivery_token'],
    'codex': [],
    'opencode': [],
    'cursor': ['--approve-mcps'],
    'grok': ['--always-approve'],
    'agy': [],
}


# Subagent runs that are made twice: with the invocation every agent gets ("original"), and with the one the
# agent's own documentation gives for use without a person at the keyboard. Both results are kept; the matrix
# cell "agent" is the documented invocation, "agent (original invocation)" the other.
AGENT_INVOCATIONS = {
    'grok': [
        {'name': 'original', 'options': [], 'prompt': PROMPTS['agent']},
        # Grok's user guide (22-permissions-and-safety.md) gives always-approve as the mode for "Scripts, SDKs, CI,
        # agent servers" (`grok -p "Run the tests" --always-approve`); without it a -p run announces the subagent
        # and ends before the subagent's answer. The prompt is unchanged. Naming the agent as Grok lists it
        # (agentpack-bundle:delivery-agent) or asking for a foreground result was tried in two runs each on
        # 2026-10-13 and was not needed: it timed out twice and failed once of four.
        {'name': 'documented', 'options': ['--always-approve'], 'prompt': PROMPTS['agent'],
         'reason': 'Grok user guide, 22-permissions-and-safety.md: always-approve is the mode for scripts and CI '
                   '(grok -p "..." --always-approve); same prompt as the original invocation'},
    ],
}


def launch(agent, prompt, kind=None, options=()):
    argv = launch_plain(agent, prompt)
    if kind == 'mcp' and MCP_OPTIONS[agent]:
        argv = [argv[0], *MCP_OPTIONS[agent], *argv[1:]]
    return [argv[0], *options, *argv[1:]]


def launch_plain(agent, prompt):
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
with tempfile.TemporaryDirectory(prefix='agentpack-delivery-', ignore_cleanup_errors=True) as td:
    base = Path(td).resolve()
    package, definition, ws = base / 'delivery-pack', base / 'definition', base / 'checkout'
    build_package(package)
    ws.mkdir()
    subprocess.run(['git', 'init', '-q', str(ws)], check=True)
    (ws / 'README.md').write_text('Benign project\n')
    (ws / 'sample.dlv').write_text('probe value\n')
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

    def lsp_log():
        """What the package's language server has recorded, over every staged copy of it."""
        return [l for f in base.rglob('lsp-calls.log') for l in f.read_text().splitlines()]

    def mcp_calls():
        """tools/call requests the package's MCP server has recorded, over every staged copy of it."""
        return sum(len(f.read_text().splitlines()) for f in base.rglob('calls.log'))

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
        answer = ''
        for limit in (args.timeout, 600):   # a first launch may install the agent's plugin dependencies; once more with longer
            try:
                check = ap(*launch(agent, 'Reply with the single word PONG'), timeout=limit)
                answer = check.stdout + check.stderr
                break
            except subprocess.TimeoutExpired:
                answer = f'TIMEOUT after {limit} s'
        if 'PONG' not in answer:
            rows.append({'agent': agent, 'status': 'could not answer a plain prompt',
                         'detail': answer.strip()[-160:]})
            print(f'{agent}: could not answer a plain prompt, skipped', flush=True)
            continue
        version = subprocess.run([BINARY[agent], '--version'], capture_output=True, text=True, timeout=30).stdout.strip().splitlines()[:1]
        for kind, inv in [(k, i) for k in args.kinds for i in (AGENT_INVOCATIONS.get(agent, [None]) if k == 'agent' else [None])
                          if k != 'lsp' or agent in LSP_AGENTS]:
            prompt = inv['prompt'] if inv else PROMPTS[kind]
            for run in range(args.runs):
                mark.unlink(missing_ok=True)
                calls_before = mcp_calls()
                lsp_before = len(lsp_log())
                attempts = [prompt] + ([COMMAND_FALLBACK] if kind == 'command' else [])
                outcome = {'agent': agent, 'version': version[0] if version else '', 'artifact': kind, 'run': run}
                if inv:
                    outcome['invocation'] = inv['name']
                for attempt, text in enumerate(attempts):
                    start = time.perf_counter()
                    try:
                        p = ap(*launch(agent, text, kind, inv['options'] if inv else ()), timeout=args.timeout)
                        out, code = p.stdout + '\n' + p.stderr, p.returncode
                    except subprocess.TimeoutExpired:
                        out, code = 'TIMEOUT', -1
                    delivered = TOKENS[kind] in out
                    if kind == 'mcp':
                        outcome['mcp_server_recorded_call'] = mcp_calls() > calls_before
                    if kind == 'lsp':
                        # the agent started the package's server and spoke LSP to it; the hover token in the answer is recorded too
                        seen = [l.split(' ', 1)[1] for l in lsp_log()[lsp_before:]]
                        outcome['lsp_server_log'] = seen[:20]
                        outcome['lsp_hover_token_in_answer'] = delivered
                        delivered = 'initialize' in [x.split(' ')[0] for x in seen]
                    outcome.update(delivered=delivered, exit=code, seconds=round(time.perf_counter() - start, 1),
                                   invoked_as='slash' if kind == 'command' and attempt == 0 else 'request',
                                   hook_ran=mark.exists(), answer_tail=out.strip()[-160:])
                    if delivered:
                        break
                rows.append(outcome)
                print(agent, kind + (f" [{inv['name']}]" if inv else ''), run, 'delivered' if outcome['delivered'] else 'NOT delivered',
                      'hook' if outcome['hook_ran'] else 'no-hook', outcome['seconds'], flush=True)
    lock_unchanged = (definition / 'pack.lock').read_bytes() == lock
    # What Antigravity's own plugin check says about the staged bundle (no model call), and the hooks file staged for it.
    agy_bundle = next(iter((base / 'staging').glob('projects/*/modes/default/agy/agentpack-bundle')), None)
    agy_report = None
    if agy_bundle and shutil.which('agy'):
        v = ap('agy', 'plugin', 'validate', str(agy_bundle), timeout=120)
        agy_report = {'command': 'agentpack agy plugin validate <staged bundle>', 'exit': v.returncode,
                      'output': (v.stdout + v.stderr).replace(str(base), '<tmp>').strip()[-1500:],
                      'staged_hooks_json': (agy_bundle / 'hooks.json').read_text().replace(str(base), '<tmp>')[:3000]
                      if (agy_bundle / 'hooks.json').is_file() else None,
                      'hook_files_in_bundle': sorted(str(f.relative_to(agy_bundle)) for f in agy_bundle.rglob('hooks.json'))}
    staged_mcp = sorted({line.strip().replace(str(base), '<tmp>')[:300] for f in (base / 'staging').rglob('*')
                         if f.is_file() and f.name in ('.mcp.json', 'mcp.json', 'mcp_config.json', 'opencode.json', 'config.toml')
                         for line in f.read_text(errors='replace').splitlines() if 'server.mjs' in line})
    checkout_files = sorted(str(p.relative_to(ws)) for p in ws.rglob('*') if '.git' not in p.parts and p.is_file())

matrix = {}
for row in rows:
    if 'artifact' not in row:
        continue
    name = row['artifact'] + (' (original invocation)' if row.get('invocation') == 'original' else '')
    cell = matrix.setdefault(row['agent'], {}).setdefault(name, {'delivered': 0, 'runs': 0})
    cell['runs'] += 1
    cell['delivered'] += row['delivered']
    hook = matrix[row['agent']].setdefault('hook', {'delivered': 0, 'runs': 0})
    hook['runs'] += 1
    hook['delivered'] += row['hook_ran']
args.output.write_text(json.dumps({
    'date': time.strftime('%Y-%m-%d'), 'platform': platform.platform(),
    'agentpack_version': subprocess.check_output([str(AP), '--version'], text=True).strip(), 'agentpack_sha256': AP_SHA,
    'mcp_launch_options': MCP_OPTIONS, 'staged_mcp_definitions': staged_mcp,
    'agent_invocations': AGENT_INVOCATIONS, 'antigravity_plugin_check': agy_report,
    'model_inference_requested': True, 'runs_per_cell': args.runs, 'lock_unchanged': lock_unchanged,
    'files_left_in_checkout': checkout_files, 'matrix': matrix, 'rows': rows}, indent=1) + '\n')
print(json.dumps(matrix, indent=1))
print('files left in checkout:', checkout_files)
