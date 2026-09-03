package resolve

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/OlegHQ/agentpack/internal/cache"
	githubsource "github.com/OlegHQ/agentpack/internal/github"
	"github.com/OlegHQ/agentpack/internal/lockfile"
	"github.com/OlegHQ/agentpack/internal/manifest"
)

func TestResolverTraversesNestedDependenciesAndMarksDirectPackages(t *testing.T) {
	t.Setenv("AGENTPACK_HOME", t.TempDir())
	shaA, shaB := strings.Repeat("a", 40), strings.Repeat("b", 40)
	project := &manifest.Manifest{Name: "demo", Version: "1.2.3", Dependencies: map[string]manifest.Dependency{
		"github.com/acme/root": shortDependency(shaA),
	}}
	fake := fakeMaterializer{nested: map[string]string{
		"github.com/acme/root": "[dependencies]\n\"github.com/acme/child\" = \"" + shaB + "\"\n",
	}}
	resolver := Resolver{Materialize: fake.materialize}
	lock, err := resolver.Resolve(context.Background(), t.TempDir(), project, ResolveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if lock.Meta.Name != "demo" || lock.Meta.Version != "1.2.3" || len(lock.Packages) != 2 {
		t.Fatalf("lock = %#v", lock)
	}
	if lock.Packages[0].Module != "github.com/acme/child" || lock.Packages[0].Direct {
		t.Fatalf("child = %#v", lock.Packages[0])
	}
	if lock.Packages[1].Module != "github.com/acme/root" || !lock.Packages[1].Direct {
		t.Fatalf("root = %#v", lock.Packages[1])
	}
}

func TestResolverReusesPreviousFloatingCommit(t *testing.T) {
	t.Setenv("AGENTPACK_HOME", t.TempDir())
	module := "github.com/acme/root"
	commit := strings.Repeat("c", 40)
	project := &manifest.Manifest{Dependencies: map[string]manifest.Dependency{module: shortDependency("")}}
	previous := &lockfile.PackLock{Packages: []lockfile.Package{{Module: module, Commit: commit}}}
	fake := fakeMaterializer{}
	lock, err := (Resolver{Materialize: fake.materialize}).Resolve(context.Background(), t.TempDir(), project, ResolveOptions{Previous: previous})
	if err != nil {
		t.Fatal(err)
	}
	if len(lock.Packages) != 1 || lock.Packages[0].Commit != commit {
		t.Fatalf("lock = %#v", lock)
	}
}

func TestResolverRejectsExactConstraintDiscoveredAfterPin(t *testing.T) {
	t.Setenv("AGENTPACK_HOME", t.TempDir())
	shaA, shaB, shaOld, shaNew := strings.Repeat("a", 40), strings.Repeat("b", 40), strings.Repeat("c", 40), strings.Repeat("d", 40)
	project := &manifest.Manifest{Dependencies: map[string]manifest.Dependency{"github.com/acme/a": shortDependency(shaA)}}
	fake := fakeMaterializer{nested: map[string]string{
		"github.com/acme/a": "[dependencies]\n\"github.com/acme/b\" = \"" + shaOld + "\"\n\"github.com/acme/z\" = \"" + shaB + "\"\n",
		"github.com/acme/z": "[dependencies]\n\"github.com/acme/b\" = \"" + shaNew + "\"\n",
	}}
	_, err := (Resolver{Materialize: fake.materialize}).Resolve(context.Background(), t.TempDir(), project, ResolveOptions{})
	if err == nil || !strings.Contains(err.Error(), "conflicting commit pins") {
		t.Fatalf("error = %v", err)
	}
}

func TestResolverCopiesPathDependencyAndRejectsNestedPaths(t *testing.T) {
	home, projectRoot := t.TempDir(), t.TempDir()
	t.Setenv("AGENTPACK_HOME", home)
	local := filepath.Join(projectRoot, "local")
	writeResolveFile(t, filepath.Join(local, "SKILL.md"), "# Local")
	writeResolveFile(t, filepath.Join(local, "agentpack.toml"), "[dependencies]\nchild = { path = \"../child\" }\n")
	relative := "local"
	project := &manifest.Manifest{Dependencies: map[string]manifest.Dependency{"local-skill": {Table: &manifest.DependencyTable{Path: &relative}}}}
	_, err := (Resolver{}).Resolve(context.Background(), projectRoot, project, ResolveOptions{})
	if err == nil || !strings.Contains(err.Error(), "transitive path dependencies are not supported") {
		t.Fatalf("error = %v", err)
	}
}

func shortDependency(value string) manifest.Dependency {
	return manifest.Dependency{Short: &value}
}

type fakeMaterializer struct{ nested map[string]string }

func (fake fakeMaterializer) materialize(_ context.Context, _ *http.Client, source githubsource.Source, _ string, _ bool) (lockfile.Package, error) {
	module := string(ModuleIDFromOwnerRepoPath(source.Owner, source.Repo, source.Path))
	cacheKey := cache.ComputeKey(module + "\x00" + source.GitRef)
	destination, err := cache.EntryDir(cacheKey)
	if err != nil {
		return lockfile.Package{}, err
	}
	if err := os.MkdirAll(destination, 0o755); err != nil {
		return lockfile.Package{}, err
	}
	if err := os.WriteFile(filepath.Join(destination, "SKILL.md"), []byte("# "+module), 0o644); err != nil {
		return lockfile.Package{}, err
	}
	if nested := fake.nested[module]; nested != "" {
		if err := os.WriteFile(filepath.Join(destination, "agentpack.toml"), []byte(nested), 0o644); err != nil {
			return lockfile.Package{}, err
		}
	}
	return lockfile.Package{Kind: lockfile.PackageSkill, Owner: source.Owner, Repo: source.Repo, Path: source.Path, Commit: source.GitRef, CacheKey: cacheKey}, nil
}

func writeResolveFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
