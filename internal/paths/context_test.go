package paths

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveContextSplitsDefinitionAndWorkspace(t *testing.T) {
	t.Parallel()
	definition := t.TempDir()
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(definition, ManifestName), []byte("name = \"x\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, err := ResolveContext("", definition, workspace, "")
	if err != nil {
		t.Fatal(err)
	}
	wantDef, _ := filepath.EvalSymlinks(definition)
	wantWS, _ := filepath.EvalSymlinks(workspace)
	if ctx.DefinitionRoot != wantDef || ctx.WorkspaceRoot != wantWS {
		t.Fatalf("context = %#v", ctx)
	}
}

func TestResolveContextLegacyProjectRoot(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ManifestName), []byte("name = \"x\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, err := ResolveContext(root, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	want, _ := filepath.EvalSymlinks(root)
	if ctx.DefinitionRoot != want || ctx.WorkspaceRoot != want {
		t.Fatalf("context = %#v", ctx)
	}
}
