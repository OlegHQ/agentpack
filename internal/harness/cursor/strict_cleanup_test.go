package cursor

import (
	base "github.com/OlegHQ/agentpack/internal/harness"
	"github.com/OlegHQ/agentpack/internal/mode"
	"os"
	"path/filepath"
	"testing"
)

func TestStrictPreparationPreservesExistingWorkspaceOverlay(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	t.Setenv("AGENTPACK_HOME", t.TempDir())
	t.Setenv("AGENTPACK_STAGING_ROOT", t.TempDir())
	definition, workspace := t.TempDir(), t.TempDir()
	overlay := filepath.Join(workspace, "owned-overlay")
	if err := os.WriteFile(overlay, []byte("existing"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writeOverlayManifest(definition, []string{overlay}); err != nil {
		t.Fatal(err)
	}
	ctx := base.StageContext{ProjectRoot: definition, WorkspaceRoot: workspace, Mode: mode.ImplicitEffective(), StrictExternal: true}
	if err := New().Prepare(ctx); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(overlay)
	if err != nil || string(data) != "existing" {
		t.Fatalf("strict preparation modified checkout: %q %v", data, err)
	}
	entries, err := readOverlayManifest(definition)
	if err != nil || len(entries) != 1 {
		t.Fatalf("cleanup tracking lost: %v %v", entries, err)
	}
}
