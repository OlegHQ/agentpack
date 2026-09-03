package cache

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/OlegHQ/agentpack/internal/lockfile"
	"github.com/OlegHQ/agentpack/internal/paths"
)

func TestCacheIndexRoundTrip(t *testing.T) {
	home := t.TempDir()
	t.Setenv("AGENTPACK_HOME", home)
	record := EntryRecord{
		Kind: lockfile.PackageSkill, SourceURL: "https://example.com", Owner: "o", Repo: "r",
		Path: "p", Commit: strings.Repeat("c", 40), FetchedAtUnix: 0,
	}
	if err := UpsertEntry("deadbeef", record, []string{"o/r/p"}); err != nil {
		t.Fatal(err)
	}
	got, found, err := GetEntry("deadbeef")
	if err != nil || !found || got.Commit != record.Commit {
		t.Fatalf("GetEntry() = %+v, %v, %v", got, found, err)
	}
	keys, err := ListKeys()
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(keys, []string{"deadbeef"}) {
		t.Fatalf("ListKeys() = %v", keys)
	}
	key, found, err := LookupAlias("O/R/P")
	if err != nil || !found || key != "deadbeef" {
		t.Fatalf("LookupAlias() = %q, %v, %v", key, found, err)
	}
	databasePath, err := paths.CacheDBPath()
	if err != nil || databasePath != home+string(filepath.Separator)+"cache"+string(filepath.Separator)+"db.bbolt" {
		t.Fatalf("CacheDBPath() = %q, %v", databasePath, err)
	}
}

func TestMissingCacheIndexIsEmpty(t *testing.T) {
	t.Setenv("AGENTPACK_HOME", t.TempDir())
	if _, found, err := LookupAlias("missing"); err != nil || found {
		t.Fatalf("LookupAlias() = %v, %v", found, err)
	}
	keys, err := ListKeys()
	if err != nil || len(keys) != 0 {
		t.Fatalf("ListKeys() = %v, %v", keys, err)
	}
}

func TestAliasesForGitHubEntry(t *testing.T) {
	t.Parallel()
	got := AliasesForGitHubEntry("Owner", "Repo", "/Skills/PDF/", " PDF-Tools ")
	want := []string{"owner/repo/Skills/PDF", "pdf-tools"}
	if !slices.Equal(got, want) {
		t.Fatalf("AliasesForGitHubEntry() = %v, want %v", got, want)
	}
}
