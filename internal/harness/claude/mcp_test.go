package claude

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/OlegHQ/agentpack/internal/mcp"
)

func TestWriteMCPFillsRemoteTypeAndDropsDisabled(t *testing.T) {
	url, off := "https://example.test/mcp", true
	path := filepath.Join(t.TempDir(), ".mcp.json")
	err := WriteMCP(path, mcp.Entries{"remote": {Server: mcp.Server{URL: &url}}, "off": {Server: mcp.Server{Disabled: &off}}})
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	var config mcp.Config
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
	if *config.Servers["remote"].Type != "http" {
		t.Fatalf("remote = %#v", config.Servers["remote"])
	}
	if _, exists := config.Servers["off"]; exists {
		t.Fatal("disabled server was written")
	}
}
