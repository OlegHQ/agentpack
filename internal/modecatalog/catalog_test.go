package modecatalog

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/OlegHQ/agentpack/internal/lockfile"
	"github.com/OlegHQ/agentpack/internal/mode"
)

func TestCatalogValidatesPackageAndDotAgentsPaths(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(t.TempDir(), "home")
	t.Setenv("AGENTPACK_HOME", home)
	key := "kkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkk"
	cacheRoot := filepath.Join(home, "cache", key)
	if err := os.MkdirAll(filepath.Join(cacheRoot, "hooks"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cacheRoot, "hooks", "hooks.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".agents", "rules"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".agents", "rules", "a.mdc"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	locked := lockfile.PackLock{Packages: []lockfile.Package{{Module: "github.com/acme/repo", CacheKey: key}}}
	catalog, err := BuildCapabilityCatalog(root, &locked, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{"package-path:github.com/acme/repo:hooks/hooks.json", ".agents:rules/a.mdc"} {
		selector, err := mode.ParseSelector(raw)
		if err != nil {
			t.Fatal(err)
		}
		if err := catalog.Validate(selector); err != nil {
			t.Fatalf("%s: %v", raw, err)
		}
	}
}
