package cursor

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/OlegHQ/agentpack/internal/mcp"
)

func TestWriteMCPPreservesNativeFields(t *testing.T) {
	command, kind, off := "server", "stdio", true
	path := filepath.Join(t.TempDir(), "mcp.json")
	if err := WriteMCP(path, mcp.Entries{"server": {Server: mcp.Server{Type: &kind, Command: &command, Disabled: &off}}}); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	var config mcp.Config
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
	server := config.Servers["server"]
	if *server.Type != "stdio" || server.Disabled == nil || !*server.Disabled {
		t.Fatalf("server = %#v", server)
	}
}
