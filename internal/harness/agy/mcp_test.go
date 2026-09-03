package agy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/OlegHQ/agentpack/internal/mcp"
)

func TestWriteMCPUsesServerURL(t *testing.T) {
	url := "https://example.test/mcp"
	path := filepath.Join(t.TempDir(), "mcp_config.json")
	if err := WriteMCP(path, mcp.Entries{"remote": {Server: mcp.Server{URL: &url}}}); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), `"serverUrl": "https://example.test/mcp"`) {
		t.Fatalf("output = %s", data)
	}
}
