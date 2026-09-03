package sync

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/OlegHQ/agentpack/internal/cache"
	githubsource "github.com/OlegHQ/agentpack/internal/github"
	"github.com/OlegHQ/agentpack/internal/lockfile"
)

func TestAddResolverReusesSlashAliasAndHostForm(t *testing.T) {
	t.Setenv("AGENTPACK_HOME", t.TempDir())
	cacheKey := "cached-owner-repo-path"
	root, _ := cache.EntryDir(cacheKey)
	writeSyncFile(t, filepath.Join(root, "SKILL.md"), "# Skill")
	record := cache.EntryRecord{Kind: lockfile.PackageSkill, SourceURL: "https://github.com/owner/repo/tree/main/skills/reuse", Owner: "owner", Repo: "repo", Path: "skills/reuse", Commit: strings.Repeat("a", 40), FetchedAtUnix: time.Now().Unix()}
	if err := cache.UpsertEntry(cacheKey, record, []string{"owner/repo/skills/reuse"}); err != nil {
		t.Fatal(err)
	}
	resolver := NewAddResolver(&http.Client{})
	for _, spec := range []string{"owner/repo/skills/reuse", "github.com/owner/repo/skills/reuse"} {
		resolved, err := resolver.Resolve(context.Background(), t.TempDir(), spec)
		if err != nil {
			t.Fatal(err)
		}
		if resolved.Package.CacheKey != cacheKey || resolved.Shorthand != "owner/repo/skills/reuse" {
			t.Fatalf("resolved = %#v", resolved)
		}
	}
}

func TestAddResolverLocalMirrorWinsUnlessRefIsExplicit(t *testing.T) {
	home := t.TempDir()
	t.Setenv("AGENTPACK_HOME", home)
	mirror := filepath.Join(home, "local", "owner", "repo", "skill")
	writeSyncFile(t, filepath.Join(mirror, "SKILL.md"), "# Local")
	remoteCalls := 0
	resolver := AddResolver{Materialize: func(_ context.Context, _ *http.Client, source githubsource.Source, _ string, _ bool) (lockfile.Package, error) {
		remoteCalls++
		return lockfile.Package{Kind: lockfile.PackageSkill, Commit: source.GitRef}, nil
	}}
	local, err := resolver.Resolve(context.Background(), t.TempDir(), "owner/repo/skill")
	if err != nil || local.Package.URL != "agentpack-local:owner/repo/skill" || remoteCalls != 0 {
		t.Fatalf("local = %#v, %v; calls=%d", local, err, remoteCalls)
	}
	explicit, err := resolver.Resolve(context.Background(), t.TempDir(), "owner/repo/skill@dev")
	if err != nil || explicit.GitRef != "dev" || explicit.Package.Commit != "dev" || remoteCalls != 1 {
		t.Fatalf("explicit = %#v, %v; calls=%d", explicit, err, remoteCalls)
	}
}

func TestAddResolverCopiesFilesystemPackage(t *testing.T) {
	t.Setenv("AGENTPACK_HOME", t.TempDir())
	cwd := t.TempDir()
	writeSyncFile(t, filepath.Join(cwd, "demo", "SKILL.md"), "# Demo")
	resolved, err := NewAddResolver(nil).Resolve(context.Background(), cwd, "demo")
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Package.Owner != "path" || !strings.HasPrefix(resolved.Package.URL, "file:") {
		t.Fatalf("resolved = %#v", resolved)
	}
}

func TestRecordFetchedStoresPackageAndShorthandAliases(t *testing.T) {
	t.Setenv("AGENTPACK_HOME", t.TempDir())
	pkg := lockfile.Package{Kind: lockfile.PackageSkill, Owner: "Owner", Repo: "Repo", Path: "skills/demo", CacheKey: "key"}
	if err := RecordFetched(pkg, "Custom/Alias"); err != nil {
		t.Fatal(err)
	}
	for _, alias := range []string{"owner/repo/skills/demo", "demo", "custom/alias"} {
		if key, found, err := cache.LookupAlias(alias); err != nil || !found || key != "key" {
			t.Fatalf("alias %q = %q, %v, %v", alias, key, found, err)
		}
	}
}

func writeSyncFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
