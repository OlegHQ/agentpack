package sync

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/OlegHQ/agentpack/internal/paths"
)

// Each test here adds a package in a form that is published in the official
// Claude Code, Cursor and Codex plugin repositories, and checks that it is
// locked and staged whole.

const (
	modernizationKey = "github.com/acme/skills/modern"
	advisorKey       = "github.com/acme/skills/advisor"
)

func (fixture *integrityFixture) stagedDir(segment string) string {
	fixture.t.Helper()
	root, err := paths.StagingRootForMode(fixture.project, "default")
	if err != nil {
		fixture.t.Fatal(err)
	}
	return filepath.Join(root, filepath.FromSlash(segment))
}

func readJSONFile(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var value map[string]any
	if err := json.Unmarshal(data, &value); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	return value
}

func hookEvents(t *testing.T, path string) string {
	t.Helper()
	events, _ := readJSONFile(t, path)["hooks"].(map[string]any)
	var names []string
	for name := range events {
		names = append(names, name)
	}
	return strings.Join(sortedStrings(names), " ")
}

func sortedStrings(values []string) []string {
	for i := range values {
		for j := i + 1; j < len(values); j++ {
			if values[j] < values[i] {
				values[i], values[j] = values[j], values[i]
			}
		}
	}
	return values
}

// anthropics/claude-plugins-official plugins/code-modernization: a hook event
// newer than the list agentpack knew, and a command whose frontmatter is not
// strict YAML. Both used to stop the package.
func TestPluginWithNewHookEventsAndLooseFrontmatterStagesWhole(t *testing.T) {
	fixture := newIntegrityFixture(t)
	fixture.upstream.tarballs[commitTwo] = map[string]string{
		"modern/.claude-plugin/plugin.json": `{"name":"modern","version":"1.0.0"}`,
		"modern/hooks/hooks.json": `{"hooks":{
			"Stop":[{"hooks":[{"type":"command","command":"sh \"${CLAUDE_PLUGIN_ROOT}/scripts/telemetry.sh\" state","timeout":10}]}],
			"PostToolUseFailure":[{"hooks":[{"type":"command","command":"sh \"${CLAUDE_PLUGIN_ROOT}/scripts/telemetry.sh\" failure"}]}],
			"StopFailure":[{"hooks":[{"type":"command","command":"sh \"${CLAUDE_PLUGIN_ROOT}/scripts/telemetry.sh\" run"}]}],
			"FutureEvent":[{"hooks":[{"type":"command","command":"never"}]}]}}`,
		"modern/scripts/telemetry.sh":  "exit 0\n",
		"modern/commands/modernize.md": "---\ndescription: Start here\nargument-hint: [system] [--source <path>]\narguments: system\n---\n\nYou are the front door.\n",
	}
	fixture.manifest(modernizationKey, commitTwo)
	fixture.lock()
	fixture.sync(SyncOptions{})

	if got := hookEvents(t, filepath.Join(fixture.bundle(), "hooks", "hooks.json")); got != "PostToolUseFailure Stop StopFailure" {
		t.Fatalf("claude hook events = %q", got)
	}
	if got := hookEvents(t, fixture.stagedDir("cursor/agentpack-bundle/hooks/hooks.json")); got != "postToolUseFailure stop" {
		t.Fatalf("cursor hook events = %q", got)
	}
	command, err := os.ReadFile(filepath.Join(fixture.bundle(), "commands", "modernize.md"))
	if err != nil || !strings.Contains(string(command), "[system] [--source <path>]") || !strings.Contains(string(command), "You are the front door.") {
		t.Fatalf("staged command = %q, %v", command, err)
	}
	if _, err := os.Stat(fixture.stagedDir("opencode/commands/modernize.md")); err != nil {
		t.Fatal(err)
	}
}

// cursor/plugins advisor: a Cursor hooks file, with events only Cursor has.
func TestCursorPluginHooksAreStagedForCursorAndSkippedElsewhereByName(t *testing.T) {
	fixture := newIntegrityFixture(t)
	fixture.upstream.tarballs[commitTwo] = map[string]string{
		"advisor/.cursor-plugin/plugin.json": `{"name":"advisor","version":"1.0.0","hooks":"./hooks/hooks.json","skills":"./skills/"}`,
		"advisor/hooks/hooks.json": `{"version":1,"hooks":{
			"afterFileEdit":[{"command":"bash \"${CURSOR_PLUGIN_ROOT}/hooks/mark-pending.sh\""}],
			"afterAgentResponse":[{"command":"bash \"${CURSOR_PLUGIN_ROOT}/hooks/capture-response.sh\""}],
			"subagentStop":[{"command":"bash \"${CURSOR_PLUGIN_ROOT}/hooks/record-consult.sh\"","matcher":"^advisor-subagent$"}],
			"stop":[{"command":"bash \"${CURSOR_PLUGIN_ROOT}/hooks/stop-hook.sh\"","loop_limit":3}]}}`,
		"advisor/hooks/stop-hook.sh":     "exit 0\n",
		"advisor/skills/advise/SKILL.md": "---\nname: advise\ndescription: Advise\n---\n\nAdvise.\n",
	}
	fixture.manifest(advisorKey, commitTwo)
	fixture.lock()
	fixture.sync(SyncOptions{})

	cursorHooks := fixture.stagedDir("cursor/agentpack-bundle/hooks/hooks.json")
	if got := hookEvents(t, cursorHooks); got != "afterAgentResponse afterFileEdit stop subagentStop" {
		t.Fatalf("cursor hook events = %q", got)
	}
	stop := readJSONFile(t, cursorHooks)["hooks"].(map[string]any)["stop"].([]any)[0].(map[string]any)
	if stop["loop_limit"] != float64(3) || !strings.Contains(stop["command"].(string), "hook-exec command --target cursor") {
		t.Fatalf("stop = %#v", stop)
	}
	// Claude Code has Stop and SubagentStop; the two Cursor-only events are left out.
	if got := hookEvents(t, filepath.Join(fixture.bundle(), "hooks", "hooks.json")); got != "Stop SubagentStop" {
		t.Fatalf("claude hook events = %q", got)
	}
	for _, root := range []string{fixture.bundle(), fixture.stagedDir("cursor/agentpack-bundle"), fixture.stagedDir("opencode")} {
		if _, err := os.Stat(filepath.Join(root, "skills", "advise", "SKILL.md")); err != nil {
			t.Fatal(err)
		}
	}
	// The staged copy the hooks run in holds the scripts they name.
	if matches, _ := filepath.Glob(fixture.stagedDir("cursor/agentpack-bundle/hooks/_packages/*/package/hooks/stop-hook.sh")); len(matches) != 1 {
		t.Fatalf("staged hook package = %v", matches)
	}
	_ = context.Background
}
