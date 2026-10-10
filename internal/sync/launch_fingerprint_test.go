package sync

import (
	base "github.com/OlegHQ/agentpack/internal/harness"
	"github.com/OlegHQ/agentpack/internal/mode"
	"os"
	"path/filepath"
	"testing"
)

func TestLaunchDigestTracksInputsModeAndTarget(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "agentpack.toml"), []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	effective := mode.ImplicitEffective()
	claude, cursor := base.Claude, base.Cursor
	first, err := ComputeLaunchDigest(root, root, effective, &claude)
	if err != nil {
		t.Fatal(err)
	}
	second, _ := ComputeLaunchDigest(root, root, effective, &claude)
	if first != second {
		t.Fatal("digest unstable")
	}
	if err := os.WriteFile(filepath.Join(root, "agentpack.toml"), []byte("b"), 0o644); err != nil {
		t.Fatal(err)
	}
	changed, _ := ComputeLaunchDigest(root, root, effective, &claude)
	if changed == first {
		t.Fatal("manifest change ignored")
	}
	other, _ := ComputeLaunchDigest(root, root, effective, &cursor)
	if other == changed {
		t.Fatal("target ignored")
	}
}
func TestLaunchStateRoundTrip(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AGENTPACK_HOME", t.TempDir())
	if err := WriteLaunchDigest(root, "default", "abc"); err != nil {
		t.Fatal(err)
	}
	got, found, err := ReadLaunchDigest(root, "default")
	if err != nil || !found || got != "abc" {
		t.Fatalf("got=%q found=%v err=%v", got, found, err)
	}
}

func TestLaunchDigestTracksExternalWorkspace(t *testing.T) {
	definition, first, second := t.TempDir(), t.TempDir(), t.TempDir()
	target := base.Claude
	effective := mode.ImplicitEffective()
	a, err := ComputeLaunchDigest(definition, first, effective, &target)
	if err != nil {
		t.Fatal(err)
	}
	b, err := ComputeLaunchDigest(definition, second, effective, &target)
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Fatal("two workspaces share launch inputs")
	}
	skill := filepath.Join(first, ".agents", "skills", "local", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(skill), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(skill, []byte("workspace skill"), 0o644); err != nil {
		t.Fatal(err)
	}
	changed, err := ComputeLaunchDigest(definition, first, effective, &target)
	if err != nil {
		t.Fatal(err)
	}
	if changed == a {
		t.Fatal("workspace .agents change ignored")
	}
}
