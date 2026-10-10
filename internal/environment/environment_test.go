package environment

import (
	"os"
	"path/filepath"
	"testing"

	base "github.com/OlegHQ/agentpack/internal/harness"
	"github.com/OlegHQ/agentpack/internal/lockfile"
	"github.com/OlegHQ/agentpack/internal/manifest"
	"github.com/OlegHQ/agentpack/internal/paths"
)

func TestInitUseExportImportRoundTrip(t *testing.T) {
	home := t.TempDir()
	t.Setenv("AGENTPACK_HOME", home)
	if _, err := paths.EnsureUserAgentpackLayout(); err != nil {
		t.Fatal(err)
	}

	project := t.TempDir()
	if err := manifest.WriteStub(project, "demo", "1.0.0"); err != nil {
		t.Fatal(err)
	}
	if err := lockfile.Init(project, "demo", "1.0.0"); err != nil {
		t.Fatal(err)
	}

	created, err := Init("team", "", project)
	if err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	if err := SaveBinding(Binding{Environment: "team", DefinitionRoot: created, WorkspaceRoot: workspace}); err != nil {
		t.Fatal(err)
	}
	binding, found, err := LoadBinding(workspace)
	if err != nil || !found || binding.DefinitionRoot != created {
		t.Fatalf("binding = %#v found=%v err=%v", binding, found, err)
	}

	bundle := filepath.Join(t.TempDir(), "team.bundle")
	if err := ExportBundle(created, bundle); err != nil {
		t.Fatal(err)
	}
	imported, err := ImportBundle(bundle, filepath.Join(t.TempDir(), "imported"))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{paths.ManifestName, paths.LockfileName} {
		if _, err := os.Stat(filepath.Join(imported, name)); err != nil {
			t.Fatalf("missing %s: %v", name, err)
		}
	}
}

func TestExternalCapabilityMarksOverlayTargets(t *testing.T) {
	t.Parallel()
	status, _ := ExternalCapability(base.Cursor)
	if status != "workspace-write" {
		t.Fatalf("cursor status = %q", status)
	}
	status, _ = ExternalCapability(base.Claude)
	if status != "external" {
		t.Fatalf("claude status = %q", status)
	}
}

func TestInitDoesNotWriteWorkspaceCheckout(t *testing.T) {
	home := t.TempDir()
	t.Setenv("AGENTPACK_HOME", home)
	if _, err := paths.EnsureUserAgentpackLayout(); err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	created, err := Init("portable", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := SaveBinding(Binding{Environment: "portable", DefinitionRoot: created, WorkspaceRoot: workspace}); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(workspace)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("checkout gained files: %#v", entries)
	}
	if _, err := os.Stat(filepath.Join(workspace, paths.ManifestName)); !os.IsNotExist(err) {
		t.Fatal("manifest must not appear in workspace")
	}
}
