package sync

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/OlegHQ/agentpack/internal/lockfile"
	"github.com/OlegHQ/agentpack/internal/manifest"
	"github.com/OlegHQ/agentpack/internal/paths"
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
