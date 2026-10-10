package staging

import (
	"testing"

	"github.com/OlegHQ/agentpack/internal/paths"
)

func TestRebuildExclusiveBlockedByLaunchShared(t *testing.T) {
	t.Setenv("AGENTPACK_HOME", t.TempDir())
	if _, err := paths.EnsureUserAgentpackLayout(); err != nil {
		t.Fatal(err)
	}
	project := t.TempDir()
	shared, err := AcquireLaunchShared(project, "default")
	if err != nil {
		t.Fatal(err)
	}
	defer shared.Unlock()
	if _, err := TryAcquireRebuildExclusive(project, "default"); err == nil {
		t.Fatal("expected exclusive rebuild to fail while launch holds shared lock")
	}
	if err := shared.Unlock(); err != nil {
		t.Fatal(err)
	}
	exclusive, err := TryAcquireRebuildExclusive(project, "default")
	if err != nil {
		t.Fatal(err)
	}
	_ = exclusive.Unlock()
}
