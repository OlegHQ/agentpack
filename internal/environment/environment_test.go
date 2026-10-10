package environment

import (
	"fmt"
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

func TestInitFromMissingLockDoesNotPublishPartialDefinition(t *testing.T) {
	t.Setenv("AGENTPACK_HOME", t.TempDir())
	source := t.TempDir()
	if err := manifest.WriteStub(source, "source", "1"); err != nil {
		t.Fatal(err)
	}
	for _, existing := range []bool{false, true} {
		t.Run(fmt.Sprint(existing), func(t *testing.T) {
			destination := filepath.Join(t.TempDir(), "destination")
			if existing {
				if err := os.Mkdir(destination, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := Init("copy", destination, source); err == nil {
				t.Fatal("accepted missing lock")
			}
			if existing {
				entries, err := os.ReadDir(destination)
				if err != nil || len(entries) != 0 {
					t.Fatalf("existing empty directory changed: %v %v", entries, err)
				}
			} else if _, err := os.Stat(destination); !os.IsNotExist(err) {
				t.Fatal("partial destination published")
			}
		})
	}
}

func TestInitCopiesContractAndPreservesRelativeDependencySource(t *testing.T) {
	t.Setenv("AGENTPACK_HOME", t.TempDir())
	source := portableFixture(t)
	body := "name=\"source\"\n[dependencies]\nhelper={path=\"../helper\"}\n[modes.review]\nbase=\"none\"\n"
	if err := os.WriteFile(filepath.Join(source, "agentpack.toml"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(t.TempDir(), "copy")
	created, err := Init("copy", destination, source)
	if err != nil {
		t.Fatal(err)
	}
	copied, err := manifest.Load(created)
	if err != nil {
		t.Fatal(err)
	}
	local, ok := copied.Dependencies["helper"].PathValue()
	if !ok || local != filepath.Clean(filepath.Join(source, "../helper")) {
		t.Fatalf("relative dependency source changed: %q", local)
	}
	if _, ok := copied.Modes["review"]; !ok {
		t.Fatal("mode lost during rebase")
	}
	for _, name := range []string{"pack.lock", "contract.json"} {
		before, _ := os.ReadFile(filepath.Join(source, name))
		after, _ := os.ReadFile(filepath.Join(created, name))
		if string(before) != string(after) {
			t.Fatalf("%s changed", name)
		}
	}
	if err := ExportBundle(created, filepath.Join(t.TempDir(), "export")); err == nil {
		t.Fatal("rebased host-local definition exported")
	}
}

func TestInitPreservesExistingDirectoryAndNeverOverwrites(t *testing.T) {
	t.Setenv("AGENTPACK_HOME", t.TempDir())
	destination := t.TempDir()
	before, err := os.Stat(destination)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Init("stub", destination, ""); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(destination)
	if err != nil || !os.SameFile(before, after) {
		t.Fatal("initialization replaced user directory")
	}
	lockBefore, _ := os.ReadFile(filepath.Join(destination, "pack.lock"))
	if _, err := Init("replacement", destination, ""); err == nil {
		t.Fatal("existing definition overwritten")
	}
	lockAfter, _ := os.ReadFile(filepath.Join(destination, "pack.lock"))
	if string(lockBefore) != string(lockAfter) {
		t.Fatal("lock overwritten")
	}
}
