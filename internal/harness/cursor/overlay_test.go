package cursor

import (
	"os"
	"path/filepath"
	"testing"

	base "github.com/olegpustovit/agentpack/internal/harness"
	"github.com/olegpustovit/agentpack/internal/mode"
)

// Cursor applies rules and subagents only from the project, so both staged
// folders are linked into the workspace, and a folder the project owns is kept.
func TestWorkspaceOverlayLinksRulesAndAgentsButKeepsProjectFolders(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	t.Setenv("AGENTPACK_HOME", t.TempDir())
	t.Setenv("AGENTPACK_STAGING_ROOT", t.TempDir())
	definition, workspace := t.TempDir(), t.TempDir()
	ctx := base.StageContext{ProjectRoot: definition, WorkspaceRoot: workspace, Mode: mode.ImplicitEffective()}
	pack, err := stagedRoot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range []string{"rules/always.mdc", "agents/reviewer.md"} {
		path := filepath.Join(pack, file)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := materializeAgentsOverlay(ctx); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"rules/always.mdc", "agents/reviewer.md"} {
		if _, err := os.Stat(filepath.Join(workspace, ".cursor", name)); err != nil {
			t.Fatalf("%s not visible in the workspace: %v", name, err)
		}
	}
	if entries, err := readOverlayManifest(definition); err != nil || len(entries) != 2 {
		t.Fatalf("overlay manifest = %v %v", entries, err)
	}
	if err := cleanupOverlay(definition); err != nil {
		t.Fatal(err)
	}
	owned := filepath.Join(workspace, ".cursor", "rules")
	if err := os.MkdirAll(owned, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(owned, "mine.mdc"), []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := materializeAgentsOverlay(ctx); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Lstat(owned); err != nil || info.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("project rules folder replaced: %v %v", info, err)
	}
	if entries, _ := readOverlayManifest(definition); len(entries) != 1 {
		t.Fatalf("overlay manifest = %v", entries)
	}
}
