package staging

import (
	"github.com/OlegHQ/agentpack/internal/lockfile"
	"github.com/OlegHQ/agentpack/internal/mode"
	"os"
	"path/filepath"
	"testing"
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
