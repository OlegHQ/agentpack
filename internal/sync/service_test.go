package sync

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	base "github.com/olegpustovit/agentpack/internal/harness"
	"github.com/olegpustovit/agentpack/internal/lockfile"
	"github.com/olegpustovit/agentpack/internal/manifest"
	"github.com/olegpustovit/agentpack/internal/paths"
	"github.com/olegpustovit/agentpack/internal/staging"
)

func TestServiceAddsPathDependencyAndStagesIt(t *testing.T) {
	project, home := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("AGENTPACK_HOME", t.TempDir())
	t.Setenv("AGENTPACK_STAGING_ROOT", t.TempDir())
	source := filepath.Join(project, "demo")
	if err := os.Mkdir(source, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "SKILL.md"), []byte("---\nname: demo\ndescription: Demo\n---\nBody\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{filepath.Join(home, ".codex", "auth.json"), filepath.Join(home, ".grok", "auth.json")} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	pkg, err := NewService().Add(context.Background(), project, source, false)
	if err != nil {
		t.Fatal(err)
	}
	if pkg.Module != "demo" {
		t.Fatalf("package=%#v", pkg)
	}
	projectManifest, err := manifest.Load(project)
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := projectManifest.Dependencies["demo"]; !exists {
		t.Fatal("dependency not recorded")
	}
	plugins, _ := paths.StagingPluginsDirForMode(project, "default")
	if _, err := os.Stat(filepath.Join(plugins, paths.StagedAgentpackBundleName, "skills", "demo", "SKILL.md")); err != nil {
		t.Fatal(err)
	}
}

func TestSyncForLaunchUsesVerifiedFastPath(t *testing.T) {
	project, home := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("AGENTPACK_HOME", t.TempDir())
	t.Setenv("AGENTPACK_STAGING_ROOT", t.TempDir())
	if err := manifest.WriteStub(project, "demo", "0.0.1"); err != nil {
		t.Fatal(err)
	}
	if err := lockfile.Init(project, "demo", "0.0.1"); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{filepath.Join(home, ".codex", "auth.json"), filepath.Join(home, ".grok", "auth.json")} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	service := NewService()
	if _, skipped, err := service.SyncForLaunch(context.Background(), project, "", base.Claude); err != nil || skipped {
		t.Fatalf("first skipped=%v err=%v", skipped, err)
	}
	if _, skipped, err := service.SyncForLaunch(context.Background(), project, "", base.Claude); err != nil || !skipped {
		t.Fatalf("second skipped=%v err=%v", skipped, err)
	}
}

// A launch of another agent must not need the staging a running session holds.
func TestSyncForLaunchOfAnotherAgentWhileOneIsRunning(t *testing.T) {
	project, home := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("AGENTPACK_HOME", t.TempDir())
	t.Setenv("AGENTPACK_STAGING_ROOT", t.TempDir())
	if err := manifest.WriteStub(project, "demo", "0.0.1"); err != nil {
		t.Fatal(err)
	}
	if err := lockfile.Init(project, "demo", "0.0.1"); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{filepath.Join(home, ".codex", "auth.json"), filepath.Join(home, ".grok", "auth.json")} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	service := NewService()
	effective, _, err := service.SyncForLaunch(context.Background(), project, "", base.Claude)
	if err != nil {
		t.Fatal(err)
	}
	running, err := staging.AcquireLaunchShared(project, "default")
	if err != nil {
		t.Fatal(err)
	}
	defer running.Unlock()
	// twice round, so that a launch after Cursor's or Antigravity's overlay is covered too
	for _, target := range []base.Target{base.Claude, base.Codex, base.OpenCode, base.Grok, base.Cursor, base.Agy,
		base.Claude, base.Codex, base.Agy, base.Cursor, base.Claude} {
		if _, skipped, err := service.SyncForLaunch(context.Background(), project, "", target); err != nil || !skipped {
			t.Fatalf("%s while claude runs: skipped=%v err=%v", target, skipped, err)
		}
		if err := service.ValidateLaunchInputs(project, project, effective, target); err != nil {
			t.Fatalf("%s: %v", target, err)
		}
	}
}

func TestServiceDryRunDoesNotBuildStaging(t *testing.T) {
	project := t.TempDir()
	t.Setenv("AGENTPACK_HOME", t.TempDir())
	t.Setenv("AGENTPACK_STAGING_ROOT", t.TempDir())
	if err := manifest.WriteStub(project, "demo", "0.0.1"); err != nil {
		t.Fatal(err)
	}
	if err := lockfile.Init(project, "demo", "0.0.1"); err != nil {
		t.Fatal(err)
	}
	result, err := NewService().Sync(context.Background(), project, SyncOptions{DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if result.Skills != 0 || result.Plugins != 0 {
		t.Fatalf("result=%#v", result)
	}
	if _, err := os.Stat(filepath.Join(os.Getenv("AGENTPACK_STAGING_ROOT"), "modes")); !os.IsNotExist(err) {
		t.Fatal("dry run created staging")
	}
}

func TestServiceRejectsModeSelectorsMissingFromCapabilityCatalog(t *testing.T) {
	project := t.TempDir()
	t.Setenv("AGENTPACK_HOME", t.TempDir())
	t.Setenv("AGENTPACK_STAGING_ROOT", t.TempDir())
	contents := "name = \"demo\"\nversion = \"0.0.1\"\n\n[dependencies]\n\n[modes.default]\nbase = \"all\"\ndisable = [\"package:github.com/example/missing\"]\n"
	if err := os.WriteFile(filepath.Join(project, "agentpack.toml"), []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := lockfile.Init(project, "demo", "0.0.1"); err != nil {
		t.Fatal(err)
	}
	_, err := NewService().Sync(context.Background(), project, SyncOptions{DryRun: true})
	if err == nil || !strings.Contains(err.Error(), "unknown package selector target") {
		t.Fatalf("Sync() error = %v", err)
	}
}
