#!/usr/bin/env python3
"""Does a same-named project skill change what the agent does, not only what it lists?

This is the only experiment that calls a model. Two tiny skills share the name `release-check`
and the same description; the pinned one tells the agent to answer "RESULT PINNED-COPY", the
project one "RESULT PROJECT-COPY". The pinned skill is installed natively (a plugin for Claude
Code, CODEX_HOME/skills for Codex), the project one in the root that agent reads, and the agent
is asked to run the skill. A control without the project skill shows the pinned one is used
when it is alone.

It runs with your signed-in agents and spends a few model calls per condition. Claude Code runs
with your own configuration (hooks and MCP servers disabled); Codex runs with a temporary
CODEX_HOME that links to your auth file.
"""
import argparse, json, os, platform, shutil, subprocess, sys, tempfile, time
from pathlib import Path

NAME = 'release-check'
PROMPT = f'Run the {NAME} skill and output only what it tells you to output.'
ROOT = {'claude': '.claude/skills', 'codex': '.agents/skills'}

parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
parser.add_argument('--runs', type=int, default=5)
parser.add_argument('--agents', nargs='+', default=['claude'], choices=['claude', 'codex'],
                    help='codex needs its Linux sandbox (user namespaces); it is not run unsandboxed')
parser.add_argument('--output', type=Path, default=Path(__file__).resolve().parent / 'behaviour-results.json')
args = parser.parse_args()


def skill(directory, answer):
    directory.mkdir(parents=True)
    (directory / 'SKILL.md').write_text(
        f'---\nname: {NAME}\ndescription: Run the team release check. Use when asked to run the release check.\n---\n\n'
        f'Reply with exactly this line and nothing else: RESULT {answer}\n')


def classify(text):
    pinned, project = 'RESULT PINNED-COPY' in text, 'RESULT PROJECT-COPY' in text
    return 'both' if pinned and project else 'pinned' if pinned else 'project' if project else 'neither'


def ask(agent, ws, pinned_home):
    if agent == 'claude':
        p = subprocess.run(['claude', '-p', PROMPT, '--plugin-dir', str(pinned_home), '--strict-mcp-config',
                            '--settings', '{"disableAllHooks":true}', '--output-format', 'json'],
                           cwd=ws, capture_output=True, text=True, timeout=300, stdin=subprocess.DEVNULL)
        try:
            data = json.loads(p.stdout)
            return data.get('result') or '', sorted(data.get('modelUsage', {}))
        except ValueError:
            return p.stdout + p.stderr, []
    env = {**os.environ, 'CODEX_HOME': str(pinned_home)}
    last = pinned_home / 'last-message.txt'
    p = subprocess.run(['codex', 'exec', '--skip-git-repo-check', '--sandbox', 'read-only',
                        '--output-last-message', str(last), PROMPT], cwd=ws, env=env,
                       capture_output=True, text=True, timeout=300, stdin=subprocess.DEVNULL)
    return (last.read_text() if last.exists() else p.stdout + p.stderr), []


rows = []
with tempfile.TemporaryDirectory(prefix='agentpack-behaviour-') as td:
    base = Path(td).resolve()
    for agent in args.agents:
        if agent == 'claude':
            pinned_home = base / 'team-plugin'
            (pinned_home / '.claude-plugin').mkdir(parents=True)
            (pinned_home / '.claude-plugin/plugin.json').write_text('{"name": "team", "version": "1.0.0"}')
            skill(pinned_home / 'skills' / NAME, 'PINNED-COPY')
        else:
            pinned_home = base / 'codex-home'
            skill(pinned_home / 'skills' / NAME, 'PINNED-COPY')
            auth = Path.home() / '.codex/auth.json'
            if not auth.exists():
                sys.exit('codex is not signed in with a file credential store')
            (pinned_home / 'auth.json').symlink_to(auth)
        for condition in ('pinned_only', 'same_named_project_skill'):
            ws = base / f'{agent}-{condition}'
            ws.mkdir()
            subprocess.run(['git', 'init', '-q', str(ws)], check=True)
            (ws / 'README.md').write_text('Benign project\n')
            if condition == 'same_named_project_skill':
                skill(ws / ROOT[agent] / NAME, 'PROJECT-COPY')
            for run in range(args.runs):
                text, models = ask(agent, ws, pinned_home)
                rows.append({'agent': agent, 'condition': condition, 'run': run, 'followed': classify(text),
                             'answer': text.strip()[:200], 'models': models})
                print(agent, condition, run, rows[-1]['followed'], flush=True)

summary = {}
for row in rows:
    cell = summary.setdefault(f"{row['agent']}:{row['condition']}", {})
    cell[row['followed']] = cell.get(row['followed'], 0) + 1
versions = {a: subprocess.check_output([a, '--version'], text=True).strip() for a in args.agents}
args.output.write_text(json.dumps({'date': time.strftime('%Y-%m-%d'), 'platform': platform.platform(), 'versions': versions,
                                   'prompt': PROMPT, 'model_inference_requested': True, 'runs_per_condition': args.runs,
                                   'summary': summary, 'rows': rows}, indent=1) + '\n')
print(json.dumps(summary, indent=1))
