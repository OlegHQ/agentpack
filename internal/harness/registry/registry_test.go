package registry

import (
	base "github.com/OlegHQ/agentpack/internal/harness"
	"testing"
)

func TestRegistryContainsEveryHarnessOnce(t *testing.T) {
	seen := map[base.Target]bool{}
	for _, candidate := range All() {
		if seen[candidate.ID()] {
			t.Fatalf("duplicate %s", candidate.ID())
		}
		seen[candidate.ID()] = true
	}
	for _, target := range base.AllTargets() {
		if !seen[target] {
			t.Fatalf("missing %s", target)
		}
	}
}
