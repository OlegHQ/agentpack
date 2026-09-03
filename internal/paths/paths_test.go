package paths

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFindProjectRootWalksAncestors(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ManifestName), []byte("name = \"test\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(root, "deep", "nested")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := FindProjectRoot(nested)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := filepath.EvalSymlinks(root)
	if got != want {
		t.Fatalf("FindProjectRoot() = %q, want %q", got, want)
	}
}

func TestStagingRootsAreModeSpecific(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AGENTPACK_STAGING_ROOT", filepath.Join(root, "stage"))
	defaultRoot, err := StagingRootForMode(root, "default")
	if err != nil {
		t.Fatal(err)
	}
	designRoot, err := StagingRootForMode(root, "design")
	if err != nil {
		t.Fatal(err)
	}
	if defaultRoot == designRoot || !strings.Contains(designRoot, "design") {
		t.Fatalf("mode roots not distinct: default=%q design=%q", defaultRoot, designRoot)
	}
}

func TestResolveProjectRootOrCWDAcceptsExplicitEmptyProject(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	got, err := ResolveProjectRootOrCWD(root)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := filepath.EvalSymlinks(root)
	if got != want {
		t.Fatalf("ResolveProjectRootOrCWD() = %q, want %q", got, want)
	}
}

func TestModePathComponentIsStable(t *testing.T) {
	t.Parallel()
	if got, want := ModePathComponent("default"), "default"; got != want {
		t.Fatalf("ModePathComponent(default) = %q, want %q", got, want)
	}
	if got, want := ModePathComponent("Design/UI"), "design-ui-ce3f9bab"; got != want {
		t.Fatalf("ModePathComponent() = %q, want %q", got, want)
	}
}
