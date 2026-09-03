package opencode

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/OlegHQ/agentpack/internal/mcp"
)

func TestMergeMCPPreservesUserAndConvertsStdio(t *testing.T) {
	path := filepath.Join(t.TempDir(), "opencode.json")
	if err := os.WriteFile(path, []byte(`{"mcp":{"user":{"type":"local","command":["user"]}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	command := "pack"
	entries := mcp.Entries{"user": {Server: mcp.Server{Command: &command}}, "new": {Server: mcp.Server{Command: &command, Args: []string{"serve"}, Env: map[string]string{"A": "B"}}}}
	if err := MergeMCP(path, entries); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	var root map[string]any
	if err := json.Unmarshal(data, &root); err != nil {
		t.Fatal(err)
	}
	servers := root["mcp"].(map[string]any)
	if servers["user"].(map[string]any)["command"].([]any)[0] != "user" {
		t.Fatal("pack replaced user entry")
	}
	entry := servers["new"].(map[string]any)
	if entry["type"] != "local" || entry["environment"].(map[string]any)["A"] != "B" {
		t.Fatalf("new = %#v", entry)
	}
}
