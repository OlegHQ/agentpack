package staging

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/OlegHQ/agentpack/internal/paths"
	"github.com/gofrs/flock"
)

const rebuildLockName = ".rebuild.lock"

func rebuildLockPath(projectRoot, modeName string) (string, error) {
	root, err := paths.StagingRootForMode(projectRoot, modeName)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", err
	}
	return filepath.Join(root, rebuildLockName), nil
}

// AcquireLaunchShared holds a shared lock while a harness process uses staging.
func AcquireLaunchShared(projectRoot, modeName string) (*flock.Flock, error) {
	path, err := rebuildLockPath(projectRoot, modeName)
	if err != nil {
		return nil, err
	}
	lock := flock.New(path)
	if err := lock.RLock(); err != nil {
		return nil, fmt.Errorf("acquire staging launch lock: %w", err)
	}
	return lock, nil
}

// TryAcquireRebuildExclusive fails when a launch still holds the shared lock.
func TryAcquireRebuildExclusive(projectRoot, modeName string) (*flock.Flock, error) {
	path, err := rebuildLockPath(projectRoot, modeName)
	if err != nil {
		return nil, err
	}
	lock := flock.New(path)
	ok, err := lock.TryLock()
	if err != nil {
		return nil, fmt.Errorf("acquire staging rebuild lock: %w", err)
	}
	if !ok {
		return nil, fmt.Errorf("staging for mode %q is in use by an active launch; wait for it to exit before restore/rebuild", modeName)
	}
	return lock, nil
}
