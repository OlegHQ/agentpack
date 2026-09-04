package lockfile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFreshLockOmitsEmptySections(t *testing.T) {
	root := t.TempDir()
	lock := EmptyForProject(root)
	if err := lock.Save(root); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(root, "pack.lock"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "[config]") || strings.Contains(string(raw), "[[packages]]") {
		t.Fatalf("empty sections were serialized:\n%s", raw)
	}
	loaded, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.SkillCount() != 0 || loaded.PluginCount() != 0 {
		t.Fatalf("fresh lock has packages: %+v", loaded.Packages)
	}
}

func TestLegacySectionsAreRejected(t *testing.T) {
	root := t.TempDir()
	raw := "lockfile-version = 2\n[meta]\nname = \"p\"\nversion = \"0.1.0\"\n[[plugins]]\nmodule = \"x\"\n"
	if err := os.WriteFile(filepath.Join(root, "pack.lock"), []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(root); err == nil {
		t.Fatal("Load() accepted legacy [[plugins]] section")
	} else if strings.Contains(err.Error(), "strict mode") || !strings.Contains(err.Error(), "plugins") || !strings.Contains(err.Error(), "agentpack lock") {
		t.Fatalf("Load() returned an opaque error: %v", err)
	}
}

func TestLoadsEarlyGoHyphenatedVersionKey(t *testing.T) {
	root := t.TempDir()
	raw := "lockfile-version = 2\n[meta]\nname = \"p\"\nversion = \"0.1.0\"\n"
	if err := os.WriteFile(filepath.Join(root, "pack.lock"), []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	lock, err := Load(root)
	if err != nil || lock.LockfileVersion != 2 {
		t.Fatalf("Load() = %#v, %v", lock, err)
	}
}

func TestUnsupportedVersionIsRejected(t *testing.T) {
	root := t.TempDir()
	raw := "lockfile_version = 1\n[meta]\nname = \"p\"\nversion = \"0.1.0\"\n"
	if err := os.WriteFile(filepath.Join(root, "pack.lock"), []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Load(root)
	if err == nil || !strings.Contains(err.Error(), "unsupported lockfile_version 1") {
		t.Fatalf("Load() error = %v", err)
	}
}

func TestLoadsRustV2LockfileSchema(t *testing.T) {
	root := t.TempDir()
	raw := `lockfile_version = 2

[meta]
name = "real-project"
version = "0.0.1"

[config]
disabled_plugins = ["legacy"]

[[packages]]
module = "github.com/anthropics/skills/skills/frontend-design"
direct = true
kind = "skill"
url = "https://github.com/anthropics/skills/tree/main/skills/frontend-design"
owner = "anthropics"
repo = "skills"
path = "skills/frontend-design"
commit = "0123456789012345678901234567890123456789"
cache_key = "0123456789012345678901234567890123456789012345678901234567890123"
name = ""
`
	path := filepath.Join(root, "pack.lock")
	if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	lock, err := LoadFromPath(path)
	if err != nil {
		t.Fatalf("load Rust v2 lockfile: %v", err)
	}
	if lock.LockfileVersion != 2 || len(lock.Packages) != 1 || lock.Config.DisabledPlugins[0] != "legacy" {
		t.Fatalf("decoded lockfile = %#v", lock)
	}
}

func TestSaveSortsPackagesWithoutMutatingCaller(t *testing.T) {
	root := t.TempDir()
	lock := EmptyForProject(root)
	lock.Packages = []Package{{Module: "z", Kind: PackageSkill}, {Module: "a", Kind: PackagePlugin}}
	if err := lock.Save(root); err != nil {
		t.Fatal(err)
	}
	if lock.Packages[0].Module != "z" {
		t.Fatal("Save() mutated package order")
	}
	raw, err := os.ReadFile(filepath.Join(root, "pack.lock"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(raw), "name =") != 1 {
		t.Fatalf("empty optional package name was serialized:\n%s", raw)
	}
	loaded, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Packages[0].Module != "a" || loaded.Packages[1].Module != "z" {
		t.Fatalf("saved order = %+v", loaded.Packages)
	}
}

func TestPackageNeedsBackfill(t *testing.T) {
	t.Parallel()
	if !(Package{Kind: PackagePlugin, URL: "https://example.com"}).NeedsBackfill() {
		t.Fatal("partial plugin should need backfill")
	}
	if (Package{Kind: PackageSkill, URL: "https://example.com"}).NeedsBackfill() {
		t.Fatal("skill should not need plugin backfill")
	}
}
