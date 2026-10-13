package sync

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/OlegHQ/agentpack/internal/harness/codex"
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
}

// `agentpack add` of a plugin that brings MCP servers used to leave them
// unrecorded and tell the user to run `agentpack lock`.
func TestAddRecordsThePackagesMCPServersInTheLock(t *testing.T) {
	fixture := newIntegrityFixture(t)
	fixture.upstream.tarballs[commitTwo] = map[string]string{
		"browser/.claude-plugin/plugin.json": `{"name":"browser","version":"1.0.0"}`,
		// The bare map of the official playwright and gitlab plugins.
		"browser/.mcp.json": `{"playwright":{"command":"npx","args":["@playwright/mcp@latest"]},"gitlab":{"type":"http","url":"https://gitlab.com/api/v4/mcp"}}`,
	}
	fixture.writeManifest("\n[dependencies]\n")
	if _, err := fixture.service.Add(context.Background(), fixture.project, "acme/skills/browser@"+commitTwo, false); err != nil {
		t.Fatal(err)
	}
	lock := fixture.loadLock()
	playwright, _ := lock.MCPServer("playwright")
	gitlab, _ := lock.MCPServer("gitlab")
	if playwright.Status != "pinned" || playwright.Version != "0.0.41" || playwright.Source != "plugin" || gitlab.Status != "unpinnable" || gitlab.Host != "gitlab.com" {
		t.Fatalf("mcp_servers = %#v", lock.MCPServers)
	}
	if fixture.noticed("no current record in pack.lock") {
		t.Fatalf("notices = %v", fixture.notices)
	}
	if staged := fixture.stagedMCP(); !strings.Contains(staged, `"@playwright/mcp@0.0.41"`) || !strings.Contains(staged, "https://gitlab.com/api/v4/mcp") {
		t.Fatalf("staged MCP = %s", staged)
	}
}

// A registry that cannot be reached does not undo the add.
func TestAddKeepsThePackageWhenItsMCPServerCannotBePinned(t *testing.T) {
	fixture := newIntegrityFixture(t)
	fixture.upstream.tarballs[commitTwo] = map[string]string{
		"browser/.claude-plugin/plugin.json": `{"name":"browser","version":"1.0.0"}`,
		"browser/.mcp.json":                  `{"mcpServers":{"playwright":{"command":"npx","args":["@playwright/mcp@latest"]}}}`,
	}
	fixture.writeManifest("\n[dependencies]\n")
	fixture.upstream.registryDown = true
	pkg, err := fixture.service.Add(context.Background(), fixture.project, "acme/skills/browser@"+commitTwo, false)
	if err != nil || pkg.ContentHash == "" {
		t.Fatalf("add = %#v, %v", pkg, err)
	}
	if _, recorded := fixture.loadLock().MCPServer("playwright"); recorded {
		t.Fatal("an unreachable registry produced a record")
	}
	if !fixture.noticed(`MCP server "playwright" (npm package @playwright/mcp@latest) could not be pinned`) || !strings.Contains(fixture.stagedMCP(), `"@playwright/mcp@latest"`) {
		t.Fatalf("notices = %v\nstaged = %s", fixture.notices, fixture.stagedMCP())
	}
	fixture.upstream.registryDown = false
	if pinned, _ := fixture.lock().MCPServer("playwright"); pinned.Version != "0.0.41" {
		t.Fatalf("lock after the registry returned = %#v", pinned)
	}
}

// The official discord and telegram plugins start their server with
// `bun run --cwd ${CLAUDE_PLUGIN_ROOT} start`, and context-mode defines its
// server inline in plugin.json as `node ${CLAUDE_PLUGIN_ROOT}/start.mjs`. The
// variable used to reach five agents as literal text.
func TestPluginRootVariableBecomesAStagedCopyOfThePluginForEveryAgent(t *testing.T) {
	fixture := newIntegrityFixture(t)
	fixture.upstream.tarballs[commitTwo] = map[string]string{
		"discord/.claude-plugin/plugin.json": `{"name":"discord","version":"0.0.1","mcpServers":{"inline":{"command":"node","args":["${CLAUDE_PLUGIN_ROOT}/start.mjs"],"env":{"STATE":"${CLAUDE_PLUGIN_DATA}/state"}}}}`,
		"discord/.mcp.json":                  `{"mcpServers":{"discord":{"command":"bun","args":["run","--cwd","${CLAUDE_PLUGIN_ROOT}","--shell=bun","--silent","start"]}}}`,
		"discord/package.json":               `{"scripts":{"start":"bun install --no-summary && bun server.ts"}}`,
		"discord/server.ts":                  "// server\n",
		"discord/start.mjs":                  "// start\n",
	}
	fixture.manifest("github.com/acme/skills/discord", commitTwo)
	lock := fixture.lock()
	fixture.sync(SyncOptions{})

	root := filepath.Join(fixture.stagedDir("plugin-roots"), lock.Packages[0].CacheKey[:16])
	for _, name := range []string{"package.json", "server.ts", "start.mjs", ".mcp.json"} {
		if _, err := os.Stat(filepath.Join(root, name)); err != nil {
			t.Fatalf("staged plugin directory: %v", err)
		}
	}
	if cached := fixture.cacheEntry(lock); cached == root || strings.HasPrefix(root, cached) {
		t.Fatalf("the server would run inside the verified cache entry %s", cached)
	}
	codexHome, err := codex.CurrentHome(fixture.project, "default")
	if err != nil {
		t.Fatal(err)
	}
	grokHome, err := paths.StagingGrokHomeDirForMode(fixture.project, "default")
	if err != nil {
		t.Fatal(err)
	}
	for agent, path := range map[string]string{
		"claude":   filepath.Join(fixture.bundle(), ".mcp.json"),
		"cursor":   fixture.stagedDir("cursor/agentpack-bundle/mcp.json"),
		"opencode": fixture.stagedDir("opencode/opencode.json"),
		"agy":      fixture.stagedDir("agy/agentpack-bundle/mcp_config.json"),
		"codex":    filepath.Join(codexHome, "config.toml"),
		"grok":     filepath.Join(grokHome, "config.toml"),
	} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("%s: %v", agent, err)
		}
		text := string(data)
		if strings.Contains(text, "PLUGIN_ROOT") || strings.Contains(text, "PLUGIN_DATA") {
			t.Errorf("%s configuration still holds a plugin variable:\n%s", agent, text)
		}
		if !strings.Contains(text, root) || !strings.Contains(text, filepath.Join(root, "start.mjs")) {
			t.Errorf("%s configuration does not name the staged plugin directory %s:\n%s", agent, root, text)
		}
	}
	// The lock records the definition as published, not this machine's path.
	if text := fixture.lockText(); strings.Contains(text, root) {
		t.Fatalf("pack.lock holds a staging path:\n%s", text)
	}
	// A second sync finds the same paths and reports no drift.
	fixture.notices = nil
	fixture.sync(SyncOptions{})
	if len(fixture.notices) != 0 {
		t.Fatalf("notices = %v", fixture.notices)
	}
	fixture.sync(SyncOptions{VerifyOnly: true})
}
