package claude

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/OlegHQ/agentpack/internal/paths"
)

func TestSettingsAttributionAndAllowlist(t *testing.T) {
	t.Setenv("AGENTPACK_HOME", t.TempDir())
	t.Setenv("AGENTPACK_KEEP_ATTRIBUTION", "")
	if err := MaterializeSettings(); err != nil {
		t.Fatal(err)
	}
	if err := SetMCPAllowlist([]string{"linear"}); err != nil {
		t.Fatal(err)
	}
	path, _ := paths.AgentpackClaudeSettingsPath()
	data, _ := os.ReadFile(path)
	var value map[string]any
	if err := json.Unmarshal(data, &value); err != nil {
		t.Fatal(err)
	}
	if value["includeCoAuthoredBy"] != false || value["enabledMcpjsonServers"].([]any)[0] != "linear" {
		t.Fatalf("settings = %#v", value)
	}
	t.Setenv("AGENTPACK_KEEP_ATTRIBUTION", "1")
	if err := MaterializeSettings(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("overlay retained")
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
