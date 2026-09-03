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
	}
}

func TestUnsupportedVersionIsRejected(t *testing.T) {
	root := t.TempDir()
	raw := "lockfile-version = 1\n[meta]\nname = \"p\"\nversion = \"0.1.0\"\n"
	if err := os.WriteFile(filepath.Join(root, "pack.lock"), []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Load(root)
	if err == nil || !strings.Contains(err.Error(), "unsupported lockfile-version 1") {
		t.Fatalf("Load() error = %v", err)
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
