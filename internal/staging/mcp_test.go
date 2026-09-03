package staging

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/OlegHQ/agentpack/internal/lockfile"
	"github.com/OlegHQ/agentpack/internal/manifest"
	"github.com/OlegHQ/agentpack/internal/mcp"
)

func TestCollectMCPPrecedenceAndJSONC(t *testing.T) {
	project := t.TempDir()
	if err := os.Mkdir(filepath.Join(project, ".agents"), 0o755); err != nil {
		t.Fatal(err)
	}
	dot := `{"mcpServers":{"shared":{"command":"dot"},"url":{"url":"https://example.test/mcp"}}} // comment`
	if err := os.WriteFile(filepath.Join(project, ".agents", "mcp.json"), []byte(dot), 0o644); err != nil {
		t.Fatal(err)
	}
	manifestCommand := "manifest"
	projectManifest := &manifest.Manifest{MCP: manifest.MCPSection{Servers: map[string]mcp.Server{"shared": {Command: &manifestCommand}}}}
	entries, err := CollectMCP(project, lockfile.PackLock{}, projectManifest, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := *entries["shared"].Server.Command; got != "dot" {
		t.Fatalf("precedence command = %q", got)
	}
	if entries["shared"].Source != mcp.DotAgents || entries["url"].Source != mcp.DotAgents {
		t.Fatalf("wrong provenance: %#v", entries)
	}
}

func TestCollectMCPSkipsMalformedDotAgentsFile(t *testing.T) {
	project := t.TempDir()
	if err := os.Mkdir(filepath.Join(project, ".agents"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, ".agents", "mcp.json"), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	entries, err := CollectMCP(project, lockfile.PackLock{}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("entries = %#v", entries)
	}
}
