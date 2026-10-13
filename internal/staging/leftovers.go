package staging

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/OlegHQ/agentpack/internal/harness/codex"
	"github.com/OlegHQ/agentpack/internal/paths"
	"github.com/gofrs/flock"
)

// MarkProjectDirectories records the project's path in the directories its
// staging lives in outside $AGENTPACK_HOME: the staging tree and the Codex
// homes. Every rebuild does this, so that the directories can be recognised
// as left over once the project is deleted.
func MarkProjectDirectories(projectRoot string) {
	if staging, _, err := paths.StagingProjectDir(projectRoot); err == nil {
		_ = paths.MarkProjectDirectory(staging, projectRoot)
	}
	if homes, err := codex.ProjectHomes(projectRoot); err == nil {
		_ = paths.MarkProjectDirectory(homes, projectRoot)
	}
}

// RemoveOrphanedProjectDirectories removes what deleted projects left behind:
// /tmp/agentpack-<hash> staging trees and the Codex homes under /var/tmp/ap-c.
// Only a directory that agentpack marked, and whose project directory no
// longer exists, is removed; a Codex home that a running session still holds
// is left for a later run. Failures are ignored: tidying never fails a sync.
func RemoveOrphanedProjectDirectories(projectRoot string) {
	// The system temporary directory holds each project's pack.lock guard
	// (agentpack-<hash>) and, when no staging root is configured, its staging.
	for _, orphan := range paths.OrphanedProjectDirectories(os.TempDir()) {
		if strings.HasPrefix(filepath.Base(orphan), "agentpack-") {
			_ = os.RemoveAll(orphan)
		}
	}
	removeStaleLockGuards(os.TempDir())
	homes, err := codex.ProjectHomes(projectRoot)
	if err != nil {
		return
	}
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

// removeStaleLockGuards removes the agentpack-<hash> directories that hold
// nothing but a pack.lock guard file nobody has touched for a day and nobody
// holds. Versions before the project marker left one per project and test
// run, with no way to tell whose it was.
func removeStaleLockGuards(parent string) {
	entries, err := os.ReadDir(parent)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), "agentpack-") || len(entry.Name()) != len("agentpack-")+16 {
			continue
		}
		directory := filepath.Join(parent, entry.Name())
		contents, err := os.ReadDir(directory)
		if err != nil || len(contents) != 1 || contents[0].Name() != "pack.lock.lock" {
			continue
		}
		guardPath := filepath.Join(directory, "pack.lock.lock")
		info, err := os.Stat(guardPath)
		if err != nil || time.Since(info.ModTime()) < 24*time.Hour {
			continue
		}
		guard := flock.New(guardPath)
		if free, err := guard.TryLock(); err != nil || !free {
			continue
		}
		_ = os.Remove(guardPath)
		_ = guard.Unlock()
		_ = os.Remove(directory)
	}
}
