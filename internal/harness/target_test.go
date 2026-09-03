package harness

import "testing"

func TestTargetsHaveStableOrderAndWorkspaceOverlay(t *testing.T) {
	t.Parallel()
	want := []Target{Claude, Cursor, Codex, OpenCode, Grok, Agy}
	got := AllTargets()
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("AllTargets() = %v", got)
		}
	}
	for _, target := range got {
		if target.UsesWorkspaceOverlay() != (target == Cursor || target == Agy) {
			t.Fatalf("UsesWorkspaceOverlay(%s)", target)
		}
	}
}
