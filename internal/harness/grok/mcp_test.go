package grok

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/OlegHQ/agentpack/internal/mcp"
)

func TestMergeMCPWritesRemoteURL(t *testing.T) {
	url := "https://example.test/mcp"
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := MergeMCP(path, mcp.Entries{"remote": {Server: mcp.Server{URL: &url}}}); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), url) {
		t.Fatalf("output = %s", data)
	}
}
