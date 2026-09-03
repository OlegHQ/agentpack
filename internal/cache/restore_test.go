package cache

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/OlegHQ/agentpack/internal/lockfile"
)

func TestEnsureLockCachedRestoresFileSource(t *testing.T) {
	home := t.TempDir()
	t.Setenv("AGENTPACK_HOME", home)
	source := t.TempDir()
	writeTestFile(t, filepath.Join(source, "SKILL.md"), "# Local")
	pkg := lockfile.Package{Kind: lockfile.PackageSkill, Owner: "path", URL: fileURL(source), CacheKey: "local-key"}
	ready, err := EnsureLockCached(pkg, nil)
	if err != nil || !ready {
		t.Fatalf("EnsureLockCached() = %v, %v", ready, err)
	}
	out, _ := EntryDir(pkg.CacheKey)
	if _, err := os.Stat(filepath.Join(out, "SKILL.md")); err != nil {
		t.Fatal(err)
	}
}

func TestEnsureLockCachedMissingLocalSourceIsNonfatal(t *testing.T) {
	t.Setenv("AGENTPACK_HOME", t.TempDir())
	pkg := lockfile.Package{Kind: lockfile.PackageSkill, Owner: "local", URL: "agentpack-local:missing/pkg", CacheKey: "missing"}
	ready, err := EnsureLockCached(pkg, nil)
	if err != nil || ready {
		t.Fatalf("EnsureLockCached() = %v, %v", ready, err)
	}
}

func TestEnsureLockCachedUsesRemoteRestorer(t *testing.T) {
	t.Setenv("AGENTPACK_HOME", t.TempDir())
	pkg := lockfile.Package{Kind: lockfile.PackagePlugin, Owner: "owner", Repo: "repo", CacheKey: "remote"}
	called := false
	ready, err := EnsureLockCached(pkg, func(_ lockfile.Package, destination string) error {
		called = true
		writeTestFile(t, filepath.Join(destination, ".cursor-plugin", "plugin.json"), `{"name":"demo"}`)
		return nil
	})
	if err != nil || !ready || !called {
		t.Fatalf("EnsureLockCached() = %v, %v; called=%v", ready, err, called)
	}
}

func TestVerifyLockCacheIntegrity(t *testing.T) {
	t.Setenv("AGENTPACK_HOME", t.TempDir())
	pluginRoot, _ := EntryDir("plugin")
	writeTestFile(t, filepath.Join(pluginRoot, ".codex-plugin", "plugin.json"), `{"name":"demo"}`)
	skillRoot, _ := EntryDir("skill")
	writeTestFile(t, filepath.Join(skillRoot, "SKILL.md"), "# Demo")
	lock := lockfile.PackLock{Packages: []lockfile.Package{
		{Kind: lockfile.PackagePlugin, CacheKey: "plugin"},
		{Kind: lockfile.PackageSkill, CacheKey: "skill"},
		{Kind: lockfile.PackageSkill},
	}}
	if err := VerifyLockCacheIntegrity(lock); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(skillRoot, "SKILL.md")); err != nil {
		t.Fatal(err)
	}
	if err := VerifyLockCacheIntegrity(lock); err == nil {
		t.Fatal("expected missing skill cache error")
	}
}

func fileURL(path string) string {
	return FileURL(path)
}
