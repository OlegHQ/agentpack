package staging

import (
	"os"
	"path/filepath"
	"testing"

	base "github.com/OlegHQ/agentpack/internal/harness"
	"github.com/OlegHQ/agentpack/internal/harness/codex"
	"github.com/OlegHQ/agentpack/internal/lockfile"
	"github.com/OlegHQ/agentpack/internal/mode"
	"github.com/gofrs/flock"
)

func TestPipelineRebuildAndVerifyEmptyPack(t *testing.T) {
	project, home := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("AGENTPACK_HOME", t.TempDir())
	t.Setenv("AGENTPACK_STAGING_ROOT", t.TempDir())
	for _, path := range []string{filepath.Join(home, ".codex", "auth.json"), filepath.Join(home, ".grok", "auth.json")} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	pipeline := Pipeline{ProjectRoot: project, Lock: lockfile.EmptyForProject(project), Mode: mode.ImplicitEffective()}
	bundles, err := pipeline.Rebuild()
	if err != nil {
		t.Fatal(err)
	}
	if len(bundles) != 1 {
		t.Fatalf("bundles=%#v", bundles)
	}
	if err := pipeline.Verify(); err != nil {
		t.Fatal(err)
	}
}

func TestCodexGenerationSwitchPreservesRunningHome(t *testing.T) {
	project, userHome := t.TempDir(), t.TempDir()
	t.Setenv("HOME", userHome)
	t.Setenv("USERPROFILE", userHome)
	t.Setenv("AGENTPACK_HOME", t.TempDir())
	t.Setenv("AGENTPACK_STAGING_ROOT", t.TempDir())
	for _, path := range []string{filepath.Join(userHome, ".codex", "auth.json"), filepath.Join(userHome, ".grok", "auth.json")} {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	pipeline := Pipeline{ProjectRoot: project, Lock: lockfile.EmptyForProject(project), Mode: mode.ImplicitEffective()}
	if _, err := pipeline.Rebuild(); err != nil {
		t.Fatal(err)
	}
	first, err := codex.CurrentHome(project, "default")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(filepath.Dir(first)) })
	if err := os.WriteFile(filepath.Join(first, "running-session.marker"), []byte("still here"), 0o600); err != nil {
		t.Fatal(err)
	}
	lease := flock.New(first + ".lease")
	if err := lease.RLock(); err != nil {
		t.Fatal(err)
	}
	defer lease.Unlock()
	if _, err := pipeline.Rebuild(); err != nil {
		t.Fatal(err)
	}
	second, err := codex.CurrentHome(project, "default")
	if err != nil {
		t.Fatal(err)
	}
	if second == first {
		t.Fatal("Codex home did not rotate")
	}
	if data, err := os.ReadFile(filepath.Join(first, "running-session.marker")); err != nil || string(data) != "still here" {
		t.Fatalf("old generation was modified: %q, %v", data, err)
	}
	if _, err := os.Stat(filepath.Join(second, "config.toml")); err != nil {
		t.Fatalf("new generation is incomplete: %v", err)
	}
	if err := pipeline.Verify(); err != nil {
		t.Fatal(err)
	}
	if err := lease.Unlock(); err != nil {
		t.Fatal(err)
	}
	if _, err := pipeline.Rebuild(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(first); !os.IsNotExist(err) {
		t.Fatalf("unused retired generation was not removed: %v", err)
	}

	otherMode, err := mode.NewEffective("design", mode.ImplicitDefault(), nil)
	if err != nil {
		t.Fatal(err)
	}
	pipeline.Mode = otherMode
	if _, err := pipeline.Rebuild(); err != nil {
		t.Fatal(err)
	}
	other, err := codex.CurrentHome(project, "design")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(filepath.Dir(other)) })
	if other == first || other == second {
		t.Fatalf("modes share a Codex generation: %s", other)
	}
}

func TestFailedCodexRebuildKeepsPublishedGeneration(t *testing.T) {
	project, userHome := t.TempDir(), t.TempDir()
	t.Setenv("HOME", userHome)
	t.Setenv("USERPROFILE", userHome)
	t.Setenv("AGENTPACK_HOME", t.TempDir())
	t.Setenv("AGENTPACK_STAGING_ROOT", t.TempDir())
	nativeConfig := filepath.Join(userHome, ".codex", "config.toml")
	if err := os.MkdirAll(filepath.Dir(nativeConfig), 0o700); err != nil {
		t.Fatal(err)
	}
	pipeline := Pipeline{ProjectRoot: project, Lock: lockfile.EmptyForProject(project), Mode: mode.ImplicitEffective()}
	if _, err := pipeline.Rebuild(); err != nil {
		t.Fatal(err)
	}
	first, err := codex.CurrentHome(project, "default")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(filepath.Dir(first)) })
	if err := os.WriteFile(nativeConfig, []byte("[invalid\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := pipeline.Rebuild(); err == nil {
		t.Fatal("invalid native Codex config did not fail the rebuild")
	}
	current, err := codex.CurrentHome(project, "default")
	if err != nil {
		t.Fatal(err)
	}
	if current != first {
		t.Fatalf("failed rebuild replaced %s with %s", first, current)
	}
	if err := codex.New().Verify(base.StageContext{ProjectRoot: project, Mode: mode.ImplicitEffective()}); err != nil {
		t.Fatalf("published home was damaged by failed rebuild: %v", err)
	}
}
