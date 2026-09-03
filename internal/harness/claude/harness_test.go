package claude

import (
	"os"
	"path/filepath"
	"testing"

	base "github.com/OlegHQ/agentpack/internal/harness"
	"github.com/OlegHQ/agentpack/internal/mcp"
	"github.com/OlegHQ/agentpack/internal/mode"
)

func TestHarnessPrepareFinalizeAndVerify(t *testing.T) {
	project := t.TempDir()
	t.Setenv("AGENTPACK_STAGING_ROOT", t.TempDir())
	t.Setenv("AGENTPACK_HOME", t.TempDir())
	t.Setenv("AGENTPACK_KEEP_ATTRIBUTION", "")
	ctx := base.StageContext{ProjectRoot: project, Mode: mode.ImplicitEffective()}
	harness := New()
	if err := harness.Prepare(ctx); err != nil {
		t.Fatal(err)
	}
	command := "server"
	if err := harness.Finalize(mcp.Entries{"demo": {Server: mcp.Server{Command: &command}}}, ctx); err != nil {
		t.Fatal(err)
	}
	if err := harness.Verify(ctx); err != nil {
		t.Fatal(err)
	}
	root, _ := harness.StagedRoot(ctx)
	if _, err := os.Stat(filepath.Join(root, ".claude-plugin", "plugin.json")); err != nil {
		t.Fatal(err)
	}
}
