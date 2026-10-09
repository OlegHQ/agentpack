package lockfile

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
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

func TestConcurrentSavesNeverExposePartialLockfile(t *testing.T) {
	root := t.TempDir()
	initial := EmptyForProject(root)
	if err := initial.Save(root); err != nil {
		t.Fatal(err)
	}
	var writers sync.WaitGroup
	errors := make(chan error, 5)
	for writer := 0; writer < 4; writer++ {
		writers.Add(1)
		go func(writer int) {
			defer writers.Done()
			for iteration := 0; iteration < 50; iteration++ {
				lock := EmptyForProject(root)
				lock.Meta.Version = fmt.Sprintf("%d.%d.0", writer, iteration)
				if err := lock.Save(root); err != nil {
					errors <- err
					return
				}
			}
		}(writer)
	}
	for iteration := 0; iteration < 200; iteration++ {
		if _, err := Load(root); err != nil {
			errors <- err
			break
		}
	}
	writers.Wait()
	close(errors)
	for err := range errors {
		t.Fatal(err)
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

func TestVersion2LockLoadsUnverifiedAndStaysVersion2(t *testing.T) {
	root := t.TempDir()
	raw := "lockfile_version = 2\n\n[meta]\nname = \"p\"\nversion = \"0.1.0\"\n\n[[packages]]\nmodule = \"github.com/acme/demo\"\nkind = \"skill\"\nurl = \"u\"\nowner = \"acme\"\nrepo = \"demo\"\ncommit = \"c\"\ncache_key = \"k\"\n"
	if err := os.WriteFile(filepath.Join(root, "pack.lock"), []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	lock, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if unverified := lock.UnverifiedPackages(); len(unverified) != 1 || unverified[0] != "github.com/acme/demo" {
		t.Fatalf("UnverifiedPackages() = %v", unverified)
	}
	if err := lock.Save(root); err != nil {
		t.Fatal(err)
	}
	saved, _ := os.ReadFile(filepath.Join(root, "pack.lock"))
	if !strings.HasPrefix(string(saved), "lockfile_version = 2\n") || strings.Contains(string(saved), "content_hash") {
		t.Fatalf("a lock without version 3 fields was not written as version 2:\n%s", saved)
	}
}

func TestVersion3RoundTripsContentHashesAndMCPServers(t *testing.T) {
	root := t.TempDir()
	lock := EmptyForProject(root)
	lock.Packages = []Package{{Module: "github.com/acme/demo", Kind: PackageSkill, CacheKey: "k", ContentHash: ContentHashPrefix + strings.Repeat("a", 64)}}
	lock.MCPServers = []MCPServer{
		{Name: "playwright", Source: "plugin", Launcher: "npm", Status: MCPPinned, Definition: "sha256:d", Requested: "@playwright/mcp@latest", Package: "@playwright/mcp", Version: "1.2.3", Integrity: "sha512-x", Registry: "https://registry.npmjs.org"},
		{Name: "linear", Source: "plugin", Launcher: "remote", Status: MCPUnpinnable, Definition: "sha256:e", Host: "mcp.linear.app"},
	}
	if err := lock.Save(root); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(root, "pack.lock"))
	if !strings.HasPrefix(string(raw), "lockfile_version = 3\n") || strings.Index(string(raw), "name = 'linear'") > strings.Index(string(raw), "name = 'playwright'") {
		t.Fatalf("unexpected version 3 lock:\n%s", raw)
	}
	loaded, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.LockfileVersion != 3 || len(loaded.UnverifiedPackages()) != 0 || loaded.Packages[0].ContentHash != lock.Packages[0].ContentHash {
		t.Fatalf("loaded = %#v", loaded)
	}
	if server, found := loaded.MCPServer("playwright"); !found || server != lock.MCPServers[0] {
		t.Fatalf("MCPServer(playwright) = %#v, %v", server, found)
	}
}

func TestPresentButInvalidContentHashIsRejected(t *testing.T) {
	digest := strings.Repeat("a", 64)
	for name, value := range map[string]string{
		"unknown algorithm":   "sha256-tree-v9:" + digest,
		"no algorithm prefix": "sha256-" + digest,
		"bare hex":            digest,
		"short digest":        ContentHashPrefix + digest[:63],
		"uppercase digest":    ContentHashPrefix + strings.Repeat("A", 64),
		"trailing data":       ContentHashPrefix + digest + "0",
		"empty value":         "",
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			raw := "lockfile_version = 3\n\n[meta]\nname = \"p\"\nversion = \"0.1.0\"\n\n[[packages]]\nmodule = \"github.com/acme/demo\"\nkind = \"skill\"\nurl = \"u\"\nowner = \"acme\"\nrepo = \"demo\"\ncommit = \"c\"\ncache_key = \"k\"\ncontent_hash = \"" + value + "\"\n"
			if err := os.WriteFile(filepath.Join(root, "pack.lock"), []byte(raw), 0o644); err != nil {
				t.Fatal(err)
			}
			_, err := Load(root)
			var invalid *ContentHashError
			if !errors.Is(err, ErrContentHash) || !errors.As(err, &invalid) || invalid.Value != value || !strings.Contains(err.Error(), "sha256-tree-v1:") {
				t.Fatalf("Load() error = %v", err)
			}
		})
	}
	// Only a missing key means "no content hash".
	root := t.TempDir()
	raw := "lockfile_version = 2\n\n[meta]\nname = \"p\"\nversion = \"0.1.0\"\n\n[[packages]]\nmodule = \"github.com/acme/demo\"\nkind = \"skill\"\nurl = \"u\"\nowner = \"acme\"\nrepo = \"demo\"\ncommit = \"c\"\ncache_key = \"k\"\n"
	if err := os.WriteFile(filepath.Join(root, "pack.lock"), []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	if lock, err := Load(root); err != nil || len(lock.UnverifiedPackages()) != 1 {
		t.Fatalf("Load(absent key) = %#v, %v", lock, err)
	}
}
