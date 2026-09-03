package agy

import (
	"os"
	"path/filepath"
	"testing"

	base "github.com/OlegHQ/agentpack/internal/harness"
	"github.com/OlegHQ/agentpack/internal/mode"
)

func TestPrepareWritesManifestAndAttributionRule(t *testing.T) {
	project := t.TempDir()
	t.Setenv("AGENTPACK_HOME", t.TempDir())
	t.Setenv("AGENTPACK_STAGING_ROOT", t.TempDir())
	t.Setenv("AGENTPACK_KEEP_ATTRIBUTION", "")
	ctx := base.StageContext{ProjectRoot: project, Mode: mode.ImplicitEffective()}
	harness := New()
	if err := harness.Prepare(ctx); err != nil {
		t.Fatal(err)
	}
	if err := harness.Verify(ctx); err != nil {
		t.Fatal(err)
	}
	root, _ := harness.StagedRoot(ctx)
	for _, relative := range []string{"plugin.json", "rules/agentpack-no-attribution.md"} {
		if _, err := os.Stat(filepath.Join(root, relative)); err != nil {
			t.Fatal(err)
		}
	}
}
