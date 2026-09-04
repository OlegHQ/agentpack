package database

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/OlegHQ/agentpack/internal/paths"
	bolt "go.etcd.io/bbolt"
	berrors "go.etcd.io/bbolt/errors"
)

const openTimeout = 2 * time.Second

// OpenCache opens agentpack's shared metadata index. The Rust implementation
// used RedDB at the same documented path; preserve that incompatible file on
// first Go use and start a fresh Bolt index. Resolved package trees and the
// project lockfile remain authoritative, so cached aliases/metadata can be
// rebuilt without data loss.
func OpenCache(readOnly bool) (*bolt.DB, error) {
	databasePath, err := paths.CacheDBPath()
	if err != nil {
		return nil, err
	}
	if readOnly {
		if _, err := os.Stat(databasePath); errors.Is(err, os.ErrNotExist) {
			return nil, nil
		} else if err != nil {
			return nil, fmt.Errorf("inspect cache database %s: %w", databasePath, err)
		}
	} else if err := os.MkdirAll(filepath.Dir(databasePath), 0o755); err != nil {
		return nil, fmt.Errorf("create cache database directory: %w", err)
	}

	database, err := bolt.Open(databasePath, 0o600, &bolt.Options{ReadOnly: readOnly, Timeout: openTimeout})
	if err == nil {
		return database, nil
	}
	if !errors.Is(err, berrors.ErrInvalid) {
		return nil, fmt.Errorf("open cache database %s: %w", databasePath, err)
	}
	backup := databasePath + ".legacy-redb-" + time.Now().UTC().Format("20060102T150405.000000000Z")
	if renameErr := os.Rename(databasePath, backup); renameErr != nil {
		if errors.Is(renameErr, os.ErrNotExist) {
			return OpenCache(readOnly)
		}
		return nil, fmt.Errorf("preserve incompatible cache database as %s: %w", backup, renameErr)
	}
	if readOnly {
		return nil, nil
	}
	database, err = bolt.Open(databasePath, 0o600, &bolt.Options{Timeout: openTimeout})
	if err != nil {
		return nil, fmt.Errorf("create cache database %s after preserving %s: %w", databasePath, backup, err)
	}
	return database, nil
}
