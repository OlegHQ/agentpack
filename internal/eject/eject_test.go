package eject

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	base "github.com/olegpustovit/agentpack/internal/harness"
	"github.com/olegpustovit/agentpack/internal/paths"
)

func put(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// staged builds a small staged tree like the one sync writes, with absolute
// paths into it, and returns the project and its staging directory.
func staged(t *testing.T) (string, string) {
	t.Helper()
	project := t.TempDir()
	t.Setenv("AGENTPACK_HOME", t.TempDir())
	t.Setenv("AGENTPACK_STAGING_ROOT", t.TempDir())
	stage, err := paths.StagingRootForMode(project, "default")
	if err != nil {
		t.Fatal(err)
	}
	server := filepath.Join(stage, "plugin-roots", "abc", "mcp", "server.mjs")
	put(t, server, "// server\n")
	mcpJSON := `{"mcpServers": {"demo": {"command": "node", "args": ["` + server + `"]}}}`
	bundle := filepath.Join(stage, "plugins", "agentpack-bundle")
	put(t, filepath.Join(bundle, ".claude-plugin", "plugin.json"), `{"name": "agentpack-bundle"}`)
	put(t, filepath.Join(bundle, "skills", "demo", "SKILL.md"), "---\nname: demo\n---\nBody\n")
	put(t, filepath.Join(bundle, ".mcp.json"), mcpJSON)
	cursor := filepath.Join(stage, "cursor", "agentpack-bundle")
	put(t, filepath.Join(cursor, "skills", "demo", "SKILL.md"), "---\nname: demo\n---\nBody\n")
	put(t, filepath.Join(cursor, "commands", "go.md"), "Go\n")
	put(t, filepath.Join(cursor, "mcp.json"), mcpJSON)
	put(t, filepath.Join(cursor, "cli-config.json"), `{"private": true}`)
	put(t, filepath.Join(stage, "cursor-home", ".cursor", "hooks.json"),
		`{"version": 1, "hooks": {"beforeSubmitPrompt": [{"command": "agentpack hook-exec --specs-dir `+cursor+`/hooks"}]}}`)
	agy := filepath.Join(stage, "agy", "agentpack-bundle")
	put(t, filepath.Join(agy, "plugin.json"), `{"name": "agentpack-bundle"}`)
	put(t, filepath.Join(agy, "mcp_config.json"), mcpJSON)
	codexHome := filepath.Join(stage, "codex-home")
	put(t, filepath.Join(codexHome, "skills", "demo", "SKILL.md"), "---\nname: demo\n---\nBody\n")
	put(t, filepath.Join(codexHome, "skills", ".system", "builtin", "SKILL.md"), "builtin\n")
	put(t, filepath.Join(codexHome, "config.toml"), "[mcp_servers.demo]\ncommand = 'node'\nargs = ['"+server+"']\n")
	put(t, filepath.Join(codexHome, "AGENTS.md"), "# AGENTS.md\n\n"+guidanceBegin+"\nThe codeword is X.\n"+guidanceEnd+"\n")
	put(t, filepath.Join(codexHome, "auth.json"), `{"secret": true}`)
	return project, stage
}

func all(project string) Options {
	return Options{ProjectRoot: project, WorkspaceRoot: project, Mode: "default", Targets: Supported()}
}

func TestEjectCopiesRewritesAndRegisters(t *testing.T) {
	project, stage := staged(t)
	if _, err := Eject(all(project)); err != nil {
		t.Fatal(err)
	}
	copy := filepath.Join(project, Dir, "stage")
	settings := map[string]map[string]any{}
	if err := json.Unmarshal([]byte(read(t, filepath.Join(project, ".claude", "settings.json"))), &settings); err != nil {
		t.Fatal(err)
	}
	if settings["enabledPlugins"][pluginKey] != true {
		t.Fatalf("plugin not enabled: %v", settings)
	}
	source := settings["extraKnownMarketplaces"][marketplace].(map[string]any)["source"].(map[string]any)
	if source["path"] != filepath.Join(copy, "plugins") {
		t.Fatalf("marketplace path %v", source["path"])
	}
	if _, err := os.Stat(filepath.Join(copy, "plugins", ".claude-plugin", "marketplace.json")); err != nil {
		t.Fatal(err)
	}
	cursorMCP := read(t, filepath.Join(project, ".cursor", "mcp.json"))
	if strings.Contains(cursorMCP, stage) || !strings.Contains(cursorMCP, filepath.Join(copy, "plugin-roots", "abc", "mcp", "server.mjs")) {
		t.Fatalf("cursor mcp.json not rewritten: %s", cursorMCP)
	}
	if !strings.Contains(read(t, filepath.Join(project, ".cursor", "hooks.json")), filepath.Join(copy, "cursor")) {
		t.Fatal("cursor hooks not rewritten")
	}
	if read(t, filepath.Join(project, ".cursor", "commands", "go.md")) != "Go\n" {
		t.Fatal("cursor command missing")
	}
	if _, err := os.Stat(filepath.Join(copy, "cursor", "agentpack-bundle", "cli-config.json")); !os.IsNotExist(err) {
		t.Fatal("Cursor's settings file was copied into the project")
	}
	if !strings.Contains(read(t, filepath.Join(project, ".codex", "config.toml")), "plugin-roots") {
		t.Fatal("codex MCP server missing")
	}
	if _, err := os.Stat(filepath.Join(project, ".agents", "skills", "demo", "SKILL.md")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(project, ".agents", "skills", ".system")); !os.IsNotExist(err) {
		t.Fatal("Codex built-in skills were ejected")
	}
	if _, err := os.Stat(filepath.Join(project, Dir, "codex", "auth.json")); !os.IsNotExist(err) {
		t.Fatal("Codex login was copied into the project")
	}
	if !strings.Contains(read(t, filepath.Join(project, "AGENTS.md")), "The codeword is X.") {
		t.Fatal("rules block missing")
	}
	info, err := os.Lstat(filepath.Join(project, ".agents", "plugins", "agentpack-bundle"))
	if err != nil || info.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("Antigravity bundle is not a copy: %v %v", info, err)
	}
	// running it again replaces the last eject
	if _, err := Eject(all(project)); err != nil {
		t.Fatalf("second eject: %v", err)
	}
}

func TestEjectRefusesToReplaceAUserFile(t *testing.T) {
	project, _ := staged(t)
	put(t, filepath.Join(project, ".cursor", "commands", "go.md"), "mine\n")
	_, err := Eject(all(project))
	if err == nil || !strings.Contains(err.Error(), ".cursor/commands/go.md exists") {
		t.Fatalf("err=%v", err)
	}
	if read(t, filepath.Join(project, ".cursor", "commands", "go.md")) != "mine\n" {
		t.Fatal("user file changed")
	}
	for _, path := range []string{filepath.Join(project, ".claude", "settings.json"), filepath.Join(project, "AGENTS.md"), filepath.Join(project, Dir)} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("%s written although eject was refused", path)
		}
	}
}

func TestEjectRefusesAConflictingSetting(t *testing.T) {
	project, _ := staged(t)
	put(t, filepath.Join(project, ".claude", "settings.json"), `{"enabledPlugins": {"agentpack-bundle@agentpack": false}}`)
	_, err := Eject(Options{ProjectRoot: project, WorkspaceRoot: project, Mode: "default", Targets: []base.Target{base.Claude}})
	if err == nil || !strings.Contains(err.Error(), "already sets enabledPlugins.agentpack-bundle@agentpack") {
		t.Fatalf("err=%v", err)
	}
}

func TestRemoveTakesOutExactlyWhatEjectAdded(t *testing.T) {
	project, _ := staged(t)
	userSettings := "{\n  // mine\n  \"model\": \"opus\",\n}\n"
	put(t, filepath.Join(project, ".claude", "settings.json"), userSettings)
	put(t, filepath.Join(project, "AGENTS.md"), "# Mine\n")
	put(t, filepath.Join(project, ".cursor", "hooks.json"), `{"version": 1, "hooks": {"stop": [{"command": "mine"}]}}`+"\n")
	if _, err := Eject(all(project)); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(read(t, filepath.Join(project, ".claude", "settings.json")), "// mine") {
		t.Fatal("comment lost in user settings")
	}
	if _, err := Remove(project); err != nil {
		t.Fatal(err)
	}
	settings := read(t, filepath.Join(project, ".claude", "settings.json"))
	if strings.Contains(settings, "agentpack") || !strings.Contains(settings, `"opus"`) || !strings.Contains(settings, "// mine") {
		t.Fatalf("settings after remove: %s", settings)
	}
	if got := read(t, filepath.Join(project, "AGENTS.md")); got != "# Mine\n" {
		t.Fatalf("AGENTS.md after remove: %q", got)
	}
	hooks := map[string]any{}
	if err := json.Unmarshal([]byte(read(t, filepath.Join(project, ".cursor", "hooks.json"))), &hooks); err != nil {
		t.Fatal(err)
	}
	if events := hooks["hooks"].(map[string]any); len(events) != 1 || hooks["version"] == nil {
		t.Fatalf("cursor hooks after remove: %v", hooks)
	}
	for _, path := range []string{".cursor/mcp.json", ".cursor/commands", ".codex", ".agents", Dir} {
		if _, err := os.Stat(filepath.Join(project, path)); !os.IsNotExist(err) {
			t.Fatalf("%s left after remove", path)
		}
	}
	if _, err := Remove(project); err == nil {
		t.Fatal("a second remove found something to remove")
	}
}
