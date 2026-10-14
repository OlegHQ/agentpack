package sync

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
	// Antigravity reads hooks.json at the plugin root, in its own shape.
	antigravity := readJSONFile(t, fixture.stagedDir("agy/agentpack-bundle/hooks.json"))["agentpack"].(map[string]any)
	if antigravity["Stop"] == nil || len(antigravity) != 1 {
		t.Fatalf("antigravity hooks = %#v", antigravity)
	}
	if !fixtureMissing(fixture.stagedDir("agy/agentpack-bundle/hooks/hooks.json")) {
		t.Fatal("a Claude Code hooks file was copied into the Antigravity plugin, where nothing reads it")
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

// stripe/ai providers/claude/plugin failed in sync on a hook event, and the
// manifest and lock already named it. Whatever makes an add fail, the project
// files stay as they were.
func TestFailedAddLeavesManifestAndLockUnchanged(t *testing.T) {
	fixture := newIntegrityFixture(t)
	fixture.manifest(demoKey, commitOne)
	fixture.lock()
	manifestBefore, err := os.ReadFile(paths.ManifestPath(fixture.project))
	if err != nil {
		t.Fatal(err)
	}
	lockBefore := fixture.lockText()
	fixture.upstream.tarballs[commitTwo] = map[string]string{
		"browser/.claude-plugin/plugin.json": `{"name":"browser","version":"1.0.0"}`,
		"browser/commands/open.md":           "Open.\n",
	}
	// Staging cannot be written, so the add fails after it has locked.
	blocked := filepath.Join(t.TempDir(), "not-a-directory")
	writeIntegrityFile(t, blocked, "")
	t.Setenv("AGENTPACK_STAGING_ROOT", blocked)
	if _, err := fixture.service.Add(context.Background(), fixture.project, "acme/skills/browser@"+commitTwo, false); err == nil {
		t.Fatal("add succeeded without a staging directory")
	}
	manifestAfter, _ := os.ReadFile(paths.ManifestPath(fixture.project))
	if string(manifestAfter) != string(manifestBefore) || fixture.lockText() != lockBefore {
		t.Fatalf("a failed add changed the project:\n%s\n%s", manifestAfter, fixture.lockText())
	}

	// In a directory with no project yet, a failed add creates none.
	empty := t.TempDir()
	if _, err := fixture.service.Add(context.Background(), empty, "acme/skills/browser@"+commitTwo, false); err == nil {
		t.Fatal("add succeeded without a staging directory")
	}
	for _, path := range []string{paths.ManifestPath(empty), paths.LockPath(empty)} {
		if !fixtureMissing(path) {
			t.Fatalf("a failed add left %s behind", path)
		}
	}
}

// anthropics/claude-plugins-official lists 315 plugins; "stripe" is a
// git-subdir entry that points at stripe/ai. `add --plugin` adds one of them.
func TestAddPluginResolvesAMarketplaceEntryToItsOwnRepositoryAndCommit(t *testing.T) {
	fixture := newIntegrityFixture(t)
	fixture.upstream.tarballs[commitOne] = map[string]string{
		".claude-plugin/marketplace.json": `{"name":"official","renames":{"old-stripe":"stripe"},"plugins":[
			{"name":"hookify","source":"./plugins/hookify"},
			{"name":"stripe","source":{"source":"git-subdir","url":"https://github.com/stripe/ai.git","path":"providers/claude/plugin","ref":"main","sha":"` + commitTwo + `"}},
			{"name":"whole","source":{"source":"url","url":"https://github.com/acme/whole.git","sha":"` + commitTwo + `"}},
			{"name":"short","source":{"source":"github","repo":"acme/short","ref":"v1"}},
			{"name":"elsewhere","source":{"source":"url","url":"https://gitlab.com/acme/elsewhere.git"}},
			{"name":"registry","source":{"source":"npm","package":"acme-plugin"}}]}`,
		"plugins/hookify/.claude-plugin/plugin.json": `{"name":"hookify","version":"1.0.0"}`,
		"plugins/hookify/commands/hookify.md":        "---\ndescription: Hookify\n---\nMake a hook.\n",
	}
	fixture.upstream.tarballs[commitTwo] = map[string]string{
		".claude-plugin/plugin.json":                          `{"name":"whole","version":"1.0.0"}`,
		"skills/whole/SKILL.md":                               "---\nname: whole\ndescription: Whole\n---\nWhole.\n",
		"providers/claude/plugin/.claude-plugin/plugin.json":  `{"name":"stripe","version":"0.1.0"}`,
		"providers/claude/plugin/.mcp.json":                   `{"mcpServers":{"stripe":{"type":"http","url":"https://mcp.stripe.com"}}}`,
		"providers/claude/plugin/skills/stripe-docs/SKILL.md": "---\nname: stripe-docs\ndescription: Docs\n---\nDocs.\n",
		"providers/claude/plugin/hooks/hooks.json":            `{"hooks":{"PostToolBatch":[{"hooks":[{"type":"command","command":"echo batch"}]}],"PostToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":"echo use"}]}]}}`,
	}
	fixture.upstream.refs["v1"] = commitTwo
	fixture.writeManifest("\n[dependencies]\n")
	add := func(marketplace, plugin string) (string, error) {
		pkg, err := fixture.service.AddPlugin(context.Background(), fixture.project, marketplace, plugin, false)
		return pkg.Module + "@" + pkg.Commit, err
	}
	for plugin, want := range map[string]string{
		"stripe":     "github.com/stripe/ai/providers/claude/plugin@" + commitTwo,
		"old-stripe": "github.com/stripe/ai/providers/claude/plugin@" + commitTwo,
		"whole":      "github.com/acme/whole@" + commitTwo,
		"short":      "github.com/acme/short@" + commitTwo,
		"hookify":    "github.com/anthropics/claude-plugins-official/plugins/hookify@" + commitOne,
	} {
		got, err := add("anthropics/claude-plugins-official@"+commitOne, plugin)
		if err != nil || got != want {
			t.Fatalf("--plugin %s = %s, %v; want %s", plugin, got, err, want)
		}
	}
	lock := fixture.loadLock()
	if len(lock.Packages) != 4 {
		t.Fatalf("packages = %#v", lock.Packages)
	}
	for _, pkg := range lock.Packages {
		if pkg.ContentHash == "" || len(pkg.Commit) != 40 {
			t.Fatalf("unpinned package %#v", pkg)
		}
	}
	if server, found := lock.MCPServer("stripe"); !found || server.Host != "mcp.stripe.com" {
		t.Fatalf("mcp_servers = %#v", lock.MCPServers)
	}
	// The stripe plugin's PostToolBatch hook is a Claude Code event and no other agent's.
	if got := hookEvents(t, filepath.Join(fixture.bundle(), "hooks", "hooks.json")); got != "PostToolBatch PostToolUse" {
		t.Fatalf("claude hook events = %q", got)
	}
	if _, err := os.Stat(filepath.Join(fixture.bundle(), "skills", "stripe-docs", "SKILL.md")); err != nil {
		t.Fatal(err)
	}
	before := fixture.lockText()
	for plugin, part := range map[string]string{
		"missing":   `lists no plugin named "missing"; it lists 6, among them elsewhere, hookify`,
		"elsewhere": "github.com only",
		"registry":  `"npm" source`,
	} {
		if _, err := add("anthropics/claude-plugins-official@"+commitOne, plugin); err == nil || !strings.Contains(err.Error(), part) {
			t.Fatalf("--plugin %s error = %v, want %q", plugin, err, part)
		}
	}
	if fixture.lockText() != before {
		t.Fatal("a rejected --plugin changed pack.lock")
	}
}

// phuryn/pm-skills is a marketplace of nine plugins. `add` without --plugin
// adds every plugin the catalog lists, or none of them.
func TestAddAllAddsEveryPluginOfAMarketplace(t *testing.T) {
	fixture := newIntegrityFixture(t)
	fixture.upstream.tarballs[commitOne] = map[string]string{
		".claude-plugin/marketplace.json": `{"name":"pm","plugins":[{"name":"one","source":"./one"},{"name":"two","source":"./two"}]}`,
		"one/.claude-plugin/plugin.json":  `{"name":"one","version":"1.0.0"}`,
		"one/skills/first/SKILL.md":       "---\nname: first\ndescription: First\n---\nFirst.\n",
		"two/.claude-plugin/plugin.json":  `{"name":"two","version":"1.0.0"}`,
		"two/commands/second.md":          "---\ndescription: Second\n---\nSecond.\n",
	}
	fixture.upstream.tarballs[commitTwo] = map[string]string{
		".claude-plugin/marketplace.json": `{"name":"pm","plugins":[{"name":"one","source":"./one"},{"name":"registry","source":{"source":"npm","package":"acme-plugin"}}]}`,
		"one/.claude-plugin/plugin.json":  `{"name":"one","version":"1.0.0"}`,
		"one/skills/first/SKILL.md":       "---\nname: first\ndescription: First\n---\nFirst.\n",
	}
	fixture.writeManifest("\n[dependencies]\n")
	if _, err := fixture.service.AddAll(context.Background(), fixture.project, "acme/pm@"+commitTwo, false); err == nil || !strings.Contains(err.Error(), `"npm" source`) {
		t.Fatalf("add of a marketplace with an npm entry: %v", err)
	}
	if !fixtureMissing(paths.LockPath(fixture.project)) {
		t.Fatal("a failed add of a marketplace left pack.lock behind")
	}
	added, err := fixture.service.AddAll(context.Background(), fixture.project, "acme/pm@"+commitOne, false)
	if err != nil {
		t.Fatal(err)
	}
	var modules []string
	for _, pkg := range added {
		modules = append(modules, pkg.Module)
	}
	if got := strings.Join(modules, " "); got != "github.com/acme/pm/one github.com/acme/pm/two" {
		t.Fatalf("added = %s", got)
	}
	if lock := fixture.loadLock(); len(lock.Packages) != 2 {
		t.Fatalf("packages = %#v", lock.Packages)
	}
	if _, err := os.Stat(filepath.Join(fixture.bundle(), "skills", "first", "SKILL.md")); err != nil {
		t.Fatal(err)
	}
}

// Deleted projects left their /tmp/agentpack-<hash> staging and their Codex
// homes behind for good. A sync removes the ones agentpack marked whose
// project is gone, and nothing else.
func TestSyncRemovesTheTempDirectoriesOfProjectsThatAreGone(t *testing.T) {
	fixture := newIntegrityFixture(t)
	temporary := t.TempDir()
	t.Setenv("TMPDIR", temporary)
	t.Setenv("AGENTPACK_STAGING_ROOT", "")
	os.Unsetenv("AGENTPACK_STAGING_ROOT")
	fixture.manifest(demoKey, commitOne)
	fixture.lock()
	fixture.sync(SyncOptions{})

	staging, parent, err := paths.StagingProjectDir(fixture.project)
	if err != nil || parent != temporary {
		t.Fatalf("staging = %s under %s, %v", staging, parent, err)
	}
	homes, err := codex.ProjectHomes(fixture.project)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(homes) })
	for _, directory := range []string{staging, homes} {
		if data, err := os.ReadFile(filepath.Join(directory, ".agentpack-project")); err != nil || !strings.Contains(string(data), filepath.Base(fixture.project)) {
			t.Fatalf("%s is not marked with its project: %q, %v", directory, data, err)
		}
	}
	gone, alive := filepath.Join(t.TempDir(), "deleted-project"), t.TempDir()
	make := func(parent, name, project string) string {
		directory := filepath.Join(parent, name)
		writeIntegrityFile(t, filepath.Join(directory, "modes", "default", "file"), "x")
		if project != "" {
			writeIntegrityFile(t, filepath.Join(directory, ".agentpack-project"), project+"\n")
		}
		t.Cleanup(func() { _ = os.RemoveAll(directory) })
		return directory
	}
	homesBase := filepath.Dir(homes)
	orphans := []string{make(temporary, "agentpack-0000000000000001", gone), make(homesBase, "0000000000000001", gone)}
	kept := []string{
		make(temporary, "agentpack-0000000000000002", alive), make(temporary, "agentpack-0000000000000003", ""),
		make(homesBase, "0000000000000002", alive), make(homesBase, "0000000000000003", ""),
	}
	// A guard directory from before the marker: old and unheld goes, recent stays.
	stale, recent := filepath.Join(temporary, "agentpack-00000000000000aa"), filepath.Join(temporary, "agentpack-00000000000000bb")
	for _, directory := range []string{stale, recent} {
		writeIntegrityFile(t, filepath.Join(directory, "pack.lock.lock"), "")
	}
	old := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(filepath.Join(stale, "pack.lock.lock"), old, old); err != nil {
		t.Fatal(err)
	}
	orphans, kept = append(orphans, stale), append(kept, recent)
	fixture.sync(SyncOptions{})
	for _, directory := range orphans {
		if !fixtureMissing(directory) {
			t.Errorf("left over: %s", directory)
		}
	}
	for _, directory := range append(kept, staging, homes) {
		if fixtureMissing(directory) {
			t.Errorf("removed: %s", directory)
		}
	}
}

// The official github plugin's server needs an Authorization header, Codex
// plugins declare an OAuth client, and context-mode's Codex definition a cwd.
// These used to be left out of every agent's configuration.
func TestRemoteServerHeadersOAuthAndCwdReachEveryAgentThatHasThem(t *testing.T) {
	fixture := newIntegrityFixture(t)
	fixture.upstream.tarballs[commitTwo] = map[string]string{
		"remote/.claude-plugin/plugin.json": `{"name":"remote","version":"1.0.0"}`,
		"remote/.mcp.json": `{"mcpServers":{
			"github":{"type":"http","url":"https://api.githubcopilot.com/mcp/","headers":{"Authorization":"Bearer header-token"}},
			"gmail":{"type":"http","url":"https://gmailmcp.googleapis.com/mcp/v1","scopes":["https://mail.google.com/"],"oauth":{"client_id":"client-id","client_secret":"client-secret","callback_port":12798}},
			"local":{"command":"node","args":["./start.mjs"],"cwd":"."}}}`,
		"remote/start.mjs": "// start\n",
	}
	fixture.manifest("github.com/acme/skills/remote", commitTwo)
	var logged bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&logged)
	t.Cleanup(func() { log.SetOutput(previous) })
	lock := fixture.lock()
	fixture.sync(SyncOptions{})

	codexHome, err := codex.CurrentHome(fixture.project, "default")
	if err != nil {
		t.Fatal(err)
	}
	grokHome, err := paths.StagingGrokHomeDirForMode(fixture.project, "default")
	if err != nil {
		t.Fatal(err)
	}
	pluginRoot := filepath.Join(fixture.stagedDir("plugin-roots"), lock.Packages[0].CacheKey[:16])
	read := func(path string) string {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return strings.Join(strings.Fields(string(data)), " ")
	}
	for agent, check := range map[string]struct {
		path string
		want []string
		not  []string
	}{
		"claude":   {filepath.Join(fixture.bundle(), ".mcp.json"), []string{`"headers": { "Authorization": "Bearer header-token" }`, `"clientId": "client-id"`, `"callbackPort": 12798`, `"scopes": "https://mail.google.com/"`}, []string{"client-secret", `"cwd"`}},
		"cursor":   {fixture.stagedDir("cursor/agentpack-bundle/mcp.json"), []string{`"Authorization": "Bearer header-token"`, `"CLIENT_ID": "client-id"`, `"CLIENT_SECRET": "client-secret"`, `"scopes": [ "https://mail.google.com/" ]`}, []string{`"cwd"`, "12798"}},
		"opencode": {fixture.stagedDir("opencode/opencode.json"), []string{`"Authorization": "Bearer header-token"`, `"clientId": "client-id"`, `"clientSecret": "client-secret"`, `"scope": "https://mail.google.com/"`, `"cwd": "` + pluginRoot + `"`}, nil},
		"agy":      {fixture.stagedDir("agy/agentpack-bundle/mcp_config.json"), []string{`"Authorization": "Bearer header-token"`, `"clientId": "client-id"`, `"clientSecret": "client-secret"`, `"cwd": "` + pluginRoot + `"`, `"serverUrl"`}, nil},
		"codex":    {filepath.Join(codexHome, "config.toml"), []string{"http_headers", "Bearer header-token", "client_id = 'client-id'", "callback_port = 12798", "scopes = ['https://mail.google.com/']", "cwd = '" + pluginRoot + "'"}, []string{"client-secret"}},
		"grok":     {filepath.Join(grokHome, "config.toml"), []string{"headers", "Bearer header-token"}, []string{"client-id", "cwd"}},
	} {
		text := read(check.path)
		for _, part := range check.want {
			if !strings.Contains(text, part) {
				t.Errorf("%s configuration lacks %s:\n%s", agent, part, text)
			}
		}
		for _, part := range check.not {
			if strings.Contains(text, part) {
				t.Errorf("%s configuration holds %s, which it has no setting for:\n%s", agent, part, text)
			}
		}
	}
	// An agent without an equivalent says so, by server and setting; the others say nothing.
	for _, part := range []string{
		"MCP server local: Claude Code has no setting for a working directory (cwd)",
		"MCP server local: Cursor has no setting for a working directory (cwd)",
		"MCP server local: Grok has no setting for a working directory (cwd)",
		"MCP server gmail: Grok has no setting for OAuth client settings",
		"MCP server gmail: Codex has no setting for an OAuth client secret",
	} {
		if !strings.Contains(logged.String(), part) {
			t.Errorf("no warning %q in:\n%s", part, logged.String())
		}
	}
	if strings.Contains(logged.String(), "MCP server github:") || strings.Contains(logged.String(), "OpenCode has no setting for a working directory") {
		t.Errorf("a warning for a setting that was carried over:\n%s", logged.String())
	}
	// pack.lock holds no header value and no client secret.
	if text := fixture.lockText(); strings.Contains(text, "header-token") || strings.Contains(text, "client-secret") || strings.Contains(text, "client-id") {
		t.Fatalf("pack.lock holds credentials:\n%s", text)
	}
}

// A plugin's language servers (.lsp.json, or lspServers in plugin.json, as
// Claude Code's language server plugins declare them) reach the three agents
// that have a language server setting; the other three are named in a warning.
func TestLanguageServersReachEveryAgentThatHasASetting(t *testing.T) {
	fixture := newIntegrityFixture(t)
	fixture.upstream.tarballs[commitTwo] = map[string]string{
		"lsp/.claude-plugin/plugin.json": `{"name":"lsp","version":"1.0.0","lspServers":{"gopls":{"command":"gopls","extensionToLanguage":{".go":"go"},"restartOnCrash":true}}}`,
		"lsp/.lsp.json":                  `{"pyright":{"command":"pyright-langserver","args":["--stdio"],"extensionToLanguage":{".py":"python",".pyi":"python"},"env":{"A":"1"}}}`,
	}
	fixture.manifest("github.com/acme/skills/lsp", commitTwo)
	var logged bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&logged)
	t.Cleanup(func() { log.SetOutput(previous) })
	fixture.lock()
	fixture.sync(SyncOptions{})

	grokHome, err := paths.StagingGrokHomeDirForMode(fixture.project, "default")
	if err != nil {
		t.Fatal(err)
	}
	read := func(path string) string {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return strings.Join(strings.Fields(string(data)), " ")
	}
	for agent, check := range map[string]struct {
		path string
		want []string
	}{
		"claude":   {filepath.Join(fixture.bundle(), ".lsp.json"), []string{`"pyright": {`, `"gopls": {`, `"extensionToLanguage": { ".py": "python"`, `"restartOnCrash": true`}},
		"grok":     {filepath.Join(grokHome, "lsp.json"), []string{`"pyright": {`, `"gopls": {`}},
		"opencode": {fixture.stagedDir("opencode/opencode.json"), []string{`"command": [ "pyright-langserver", "--stdio" ]`, `"extensions": [ ".py", ".pyi" ]`, `"env": { "A": "1" }`, `"command": [ "gopls" ]`}},
	} {
		text := read(check.path)
		for _, part := range check.want {
			if !strings.Contains(text, part) {
				t.Errorf("%s configuration lacks %s:\n%s", agent, part, text)
			}
		}
	}
	for _, part := range []string{
		"skipping language server(s) gopls, pyright for Codex: it has no language server setting",
		"skipping language server(s) gopls, pyright for Cursor: it has no language server setting",
		"skipping language server(s) gopls, pyright for Antigravity: it has no language server setting",
		`OpenCode: language server "gopls": leaving out [restartOnCrash]`,
	} {
		if !strings.Contains(logged.String(), part) {
			t.Errorf("log lacks %q:\n%s", part, logged.String())
		}
	}
}
