# Cursor hook in an interactive session (2026-10-15)

In the functional test (`delivery.py`), Cursor's CLI never ran the package's prompt-submit hook in non-interactive mode (`agent -p`): 0 of 15 runs. Cursor's forum staff describe this as a known gap of the CLI: in non-interactive mode `beforeSubmitPrompt`, `afterAgentResponse` and `stop` are not emitted (https://forum.cursor.com/t/cursor-cli-omits-beforesubmitprompt-afteragentresponse-and-stop-hooks-loses-token-usage-and-emits-inconsistent-generation-id-values/169059).

This check runs the same hook in an interactive Cursor session.

- Agentpack 0.5.1 release binary (linux arm64, sha256 `5c764a8fe3174c863d6107460b181d8a5c3733a5362b14ca4247bc865c76a943`; stages exactly what 0.5.0 stages), Cursor Agent 2026.08.25-3e8eec8, Linux 6.8 aarch64.
- Package: a Claude Code plugin with `hooks/hooks.json` holding the functional test's hook unchanged (`UserPromptSubmit`, command `sh -c 'echo hook-ran >> "$DELIVERY_HOOK_MARK"'`) and one skill.
- Steps: a fresh git repository; `agentpack init`; `agentpack add <package dir>`; `DELIVERY_HOOK_MARK=<file> agentpack agent` in a terminal (tmux); answer the workspace-trust prompt with "Trust this workspace"; submit three prompts ("Reply with the single word PONG", "...PONG1", "...PONG2").
- Agentpack reported at add time: `hook for cursor: ... routed via agentpack hook-exec dispatch`.
- Result: Cursor answered PONG, PONG1, PONG2, and the mark file held three lines `hook-ran`: the hook ran once per submitted prompt, 3 of 3.
