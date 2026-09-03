package grok

import (
	base "github.com/OlegHQ/agentpack/internal/harness"
	"github.com/OlegHQ/agentpack/internal/mode"
	"os"
	"path/filepath"
	"testing"
)

func TestPreparePreservesNativeSessions(t *testing.T) {
	project, home := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("AGENTPACK_STAGING_ROOT", t.TempDir())
	ctx := base.StageContext{ProjectRoot: project, Mode: mode.ImplicitEffective()}
	h := New()
	if err := h.Prepare(ctx); err != nil {
		t.Fatal(err)
	}
	staged, _ := pathsHome(ctx)
	if err := os.WriteFile(filepath.Join(staged, "sessions", "thread.jsonl"), []byte("session"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := h.Verify(ctx); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(home, ".grok", "sessions", "thread.jsonl"))
	if err != nil || string(data) != "session" {
		t.Fatalf("native session=%q err=%v", data, err)
	}
}
func pathsHome(ctx base.StageContext) (string, error) {
	return filepath.Join(os.Getenv("AGENTPACK_STAGING_ROOT"), "modes", "default", "grok-home"), nil
}
