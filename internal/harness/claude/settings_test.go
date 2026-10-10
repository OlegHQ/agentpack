package claude

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	base "github.com/OlegHQ/agentpack/internal/harness"
	"github.com/OlegHQ/agentpack/internal/mode"
)

func TestSettingsAttributionAndAllowlist(t *testing.T) {
	t.Setenv("AGENTPACK_HOME", t.TempDir())
	t.Setenv("AGENTPACK_KEEP_ATTRIBUTION", "")
	ctx := base.StageContext{ProjectRoot: t.TempDir(), Mode: mode.ImplicitEffective()}
	if err := MaterializeSettings(ctx); err != nil {
		t.Fatal(err)
	}
	if err := SetMCPAllowlist(ctx, []string{"linear"}); err != nil {
		t.Fatal(err)
	}
	path, _ := SettingsPath(ctx)
	data, _ := os.ReadFile(path)
	var value map[string]any
	if err := json.Unmarshal(data, &value); err != nil {
		t.Fatal(err)
	}
	if value["includeCoAuthoredBy"] != false || value["enabledMcpjsonServers"].([]any)[0] != "linear" {
		t.Fatalf("settings = %#v", value)
	}
	t.Setenv("AGENTPACK_KEEP_ATTRIBUTION", "1")
	if err := MaterializeSettings(ctx); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(path)
	if err := json.Unmarshal(data, &value); err != nil {
		t.Fatal(err)
	}
	var fresh map[string]any
	json.Unmarshal(data, &fresh)
	if len(fresh) != 0 {
		t.Fatalf("attribution override retained: %s", data)
	}
}

func TestInjectGuidanceIsIdempotent(t *testing.T) {
	bundle := t.TempDir()
	hooks := filepath.Join(bundle, "hooks")
	if err := os.Mkdir(hooks, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(hooks, "hooks.json"), []byte(`{"hooks":{"PreToolUse":[{"hooks":[{"command":"echo"}]}]}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := InjectGuidance(bundle, "hello"); err != nil {
		t.Fatal(err)
	}
	if err := InjectGuidance(bundle, "hello"); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(hooks, "hooks.json"))
	var root map[string]any
	if err := json.Unmarshal(data, &root); err != nil {
		t.Fatal(err)
	}
	events := root["hooks"].(map[string]any)
	if len(events["SessionStart"].([]any)) != 1 || len(events["PreToolUse"].([]any)) != 1 {
		t.Fatalf("events = %#v", events)
	}
}

func TestClaudeSettingsIsolatedAcrossProjectsAndModes(t *testing.T) {
	t.Setenv("AGENTPACK_HOME", t.TempDir())
	t.Setenv("AGENTPACK_STAGING_ROOT", t.TempDir())
	t.Setenv("AGENTPACK_KEEP_ATTRIBUTION", "")
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAUDE_CODE_PATH", binary)
	project := t.TempDir()
	otherMode, err := mode.NewEffective("review", mode.ImplicitDefault(), nil)
	if err != nil {
		t.Fatal(err)
	}
	contexts := []base.StageContext{{ProjectRoot: project, Mode: mode.ImplicitEffective()}, {ProjectRoot: project, Mode: otherMode}, {ProjectRoot: t.TempDir(), Mode: mode.ImplicitEffective()}}
	var group sync.WaitGroup
	failures := make(chan error, len(contexts))
	for index, ctx := range contexts {
		group.Add(1)
		go func(index int, ctx base.StageContext) {
			defer group.Done()
			if err := MaterializeSettings(ctx); err != nil {
				failures <- err
				return
			}
			failures <- SetMCPAllowlist(ctx, []string{fmt.Sprintf("server-%d", index)})
		}(index, ctx)
	}
	group.Wait()
	close(failures)
	for err := range failures {
		if err != nil {
			t.Fatal(err)
		}
	}
	var previous *exec.Cmd
	for index, ctx := range contexts {
		command, err := launch(base.LaunchContext{ProjectRoot: ctx.ProjectRoot, Mode: ctx.Mode})
		if err != nil {
			t.Fatal(err)
		}
		if len(command.Args) < 3 || command.Args[1] != "--settings" {
			t.Fatalf("missing inline settings: %v", command.Args)
		}
		var settings map[string]any
		if err := json.Unmarshal([]byte(command.Args[2]), &settings); err != nil {
			t.Fatalf("settings is path instead of JSON: %v", err)
		}
		servers := settings["enabledMcpjsonServers"].([]any)
		if len(servers) != 1 || servers[0] != fmt.Sprintf("server-%d", index) {
			t.Fatalf("cross-project/mode settings contamination: %v", servers)
		}
		if index == 0 {
			previous = command
		}
	}
	if err := SetMCPAllowlist(contexts[0], nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(previous.Args[2], "server-0") {
		t.Fatal("constructed launch changed after settings rebuild")
	}
	path, _ := SettingsPath(contexts[0])
	data, _ := os.ReadFile(path)
	if strings.Contains(string(data), "server-0") {
		t.Fatal("empty pack retained previous MCP allowlist")
	}
}
