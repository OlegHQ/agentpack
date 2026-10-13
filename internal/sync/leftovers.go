package sync

import (
	"os"
	"path/filepath"

	"github.com/OlegHQ/agentpack/internal/harness/codex"
	"github.com/OlegHQ/agentpack/internal/paths"
	"github.com/gofrs/flock"
)

// tidyProjectDirectories marks the directories this project's staging lives
// in outside $AGENTPACK_HOME, and removes the ones other projects left behind
// when they were deleted: /tmp/agentpack-<hash> staging trees and the Codex
// homes under /var/tmp/ap-c. Only a directory that agentpack marked, and whose
// project directory no longer exists, is removed; a Codex home that a running
// session still holds is left for a later run. Failures are ignored: tidying
// never fails a sync.
func tidyProjectDirectories(projectRoot string) {
	staging, parent, err := paths.StagingProjectDir(projectRoot)
	if err != nil {
		return
	}
	_ = paths.MarkProjectDirectory(staging, projectRoot)
	if parent != "" {
		for _, orphan := range paths.OrphanedProjectDirectories(parent) {
			_ = os.RemoveAll(orphan)
		}
	}
	homes, err := codex.ProjectHomes(projectRoot)
	if err != nil {
		return
	}
	_ = paths.MarkProjectDirectory(homes, projectRoot)
	for _, orphan := range paths.OrphanedProjectDirectories(filepath.Dir(homes)) {
		if codexHomesInUse(orphan) {
			continue
		}
		_ = os.RemoveAll(orphan)
	}
}

// codexHomesInUse reports whether a launch still holds a lease on one of the
// generations below a project's Codex homes directory.
func codexHomesInUse(directory string) bool {
	leases, _ := filepath.Glob(filepath.Join(directory, "*", "*.lease"))
	for _, path := range leases {
		lease := flock.New(path)
		free, err := lease.TryLock()
		if err != nil || !free {
			return true
		}
		_ = lease.Unlock()
	}
	return false
}
