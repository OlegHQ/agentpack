package database

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/OlegHQ/agentpack/internal/paths"
)

func TestOpenCachePreservesLegacyRedDBAndCreatesBolt(t *testing.T) {
	home := t.TempDir()
	t.Setenv("AGENTPACK_HOME", home)
	databasePath, err := paths.CacheDBPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(databasePath), 0o755); err != nil {
		t.Fatal(err)
	}
	legacy := []byte("redb\x1a\x0a incompatible fixture")
	if err := os.WriteFile(databasePath, legacy, 0o600); err != nil {
		t.Fatal(err)
	}
	if database, err := OpenCache(true); err != nil || database != nil {
		t.Fatalf("read-only migration = %v, %v", database, err)
	}
	backups, err := filepath.Glob(databasePath + ".legacy-redb-*")
	if err != nil || len(backups) != 1 {
		t.Fatalf("legacy backups = %v, %v", backups, err)
	}
	if body, err := os.ReadFile(backups[0]); err != nil || string(body) != string(legacy) {
		t.Fatalf("legacy backup = %q, %v", body, err)
	}
	database, err := OpenCache(false)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(databasePath); err != nil {
		t.Fatal(err)
	}
}
