package opencode

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	base "github.com/OlegHQ/agentpack/internal/harness"
	"github.com/OlegHQ/agentpack/internal/mode"
)

func TestPrepareSeedsConfigAndAttributionIdempotently(t *testing.T) {
	project, stagingRoot, home := t.TempDir(), t.TempDir(), t.TempDir()
	t.Setenv("AGENTPACK_STAGING_ROOT", stagingRoot)
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("AGENTPACK_KEEP_ATTRIBUTION", "")
	userRoot := filepath.Join(home, ".config", "opencode")
	if err := os.MkdirAll(userRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(userRoot, "opencode.json"), []byte(`{"provider":"test"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx := base.StageContext{ProjectRoot: project, Mode: mode.ImplicitEffective()}
	harness := New()
	if err := harness.Prepare(ctx); err != nil {
		t.Fatal(err)
	}
	if err := harness.Prepare(ctx); err != nil {
		t.Fatal(err)
	}
	root, _ := harness.StagedRoot(ctx)
	data, _ := os.ReadFile(filepath.Join(root, "opencode.json"))
	var config map[string]any
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
	if config["provider"] != "test" || len(config["instructions"].([]any)) != 1 {
		t.Fatalf("config = %#v", config)
	}
	if _, err := os.Stat(filepath.Join(root, instructionsFile)); err != nil {
		t.Fatal(err)
	}
}
