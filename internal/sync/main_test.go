package sync

import (
	"context"
	"os"
	"testing"

	"github.com/olegpustovit/agentpack/internal/lockfile"
	"github.com/olegpustovit/agentpack/internal/mcp"
)

// TestMain gives the tests that do not choose a staging root one of their
// own, at a path short enough to hold Codex homes too, and removes it. Without
// it each such test leaves an agentpack-<hash> directory in the system
// temporary directory.
func TestMain(m *testing.M) {
	root, err := os.MkdirTemp("/tmp", "ap")
	if err != nil {
		root, err = os.MkdirTemp("", "ap")
	}
	if err != nil {
		panic(err)
	}
	os.Setenv("AGENTPACK_STAGING_ROOT", root)
	// the lock tests serve npm metadata from stand-in registries; npm itself is never started
	mcp.ResolveTree = func(context.Context, string, string, string, string) ([]lockfile.NPMTreeEntry, string, error) {
		return nil, "", mcp.ErrNoNPM
	}
	code := m.Run()
	_ = os.RemoveAll(root)
	os.Exit(code)
}
