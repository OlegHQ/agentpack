package codex

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/OlegHQ/agentpack/internal/mcp"
)

func TestMergeMCPPreservesUserAndWritesDisabled(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("[mcp_servers.user]\ncommand = \"user\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	command, off := "pack", true
	if err := MergeMCP(path, mcp.Entries{"user": {Server: mcp.Server{Command: &command}}, "pack": {Server: mcp.Server{Command: &command, Disabled: &off}}}); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	output := string(data)
	if !strings.Contains(output, `command = 'user'`) && !strings.Contains(output, `command = "user"`) {
		t.Fatalf("user missing: %s", output)
	}
	if !strings.Contains(output, "enabled = false") {
		t.Fatalf("disabled missing: %s", output)
	}
}
