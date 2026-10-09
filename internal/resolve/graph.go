package resolve

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"

	"github.com/OlegHQ/agentpack/internal/cache"
	githubsource "github.com/OlegHQ/agentpack/internal/github"
	"github.com/OlegHQ/agentpack/internal/lockfile"
	"github.com/OlegHQ/agentpack/internal/manifest"
)

type ResolveOptions struct {
	Previous        *lockfile.PackLock
	RefreshFloating bool
	// RefreshModules refreshes only the named modules. It is used by
	// `agentpack update SPEC`; exact commit pins remain immutable.
	RefreshModules map[string]bool
	// RecordContentHashes re-fetches packages whose lock entry has no content
	// hash so one can be recorded from freshly downloaded bytes. Without it a
	// package that is already cached stays unverified.
	RecordContentHashes bool
	// RepairCache replaces a cache entry that fails verification with a fresh
	// fetch of the pinned commit instead of failing.
	RepairCache bool
	// Notify receives one-line reports about cache entries that were replaced.
	Notify func(message string)
}

type MaterializeFunc func(ctx context.Context, client *http.Client, source githubsource.Source, displayURL string, forceRefresh bool) (lockfile.Package, error)

type Resolver struct {
	Client      *http.Client
	Tags        TagLister
	Materialize MaterializeFunc
	Restore     cache.RemoteRestoreFunc
}

func NewResolver(ctx context.Context, client *http.Client) Resolver {
	return Resolver{
		Client:      client,
		Tags:        githubTagLister{ctx: ctx, client: client},
		Materialize: cache.MaterializeGitHubTree,
		Restore:     cache.GitHubRestore(ctx, client),
	}
}

func (resolver Resolver) Resolve(ctx context.Context, projectRoot string, project *manifest.Manifest, options ResolveOptions) (lockfile.PackLock, error) {
	lock := lockfile.PackLock{LockfileVersion: lockfile.Version, Meta: lockfile.Meta{Name: project.Name, Version: project.Version}}
	if len(project.Dependencies) == 0 {
		return lock, nil
	}
	if resolver.Materialize == nil {
		resolver.Materialize = cache.MaterializeGitHubTree
	}
	if resolver.Tags == nil {
		resolver.Tags = githubTagLister{ctx: ctx, client: resolver.Client}
	}

	var pathPackages []lockfile.Package
	githubDependencies := make(map[string]manifest.Dependency)
	var transitiveFromPath []dependencyEntry
	for _, key := range sortedDependencyKeys(project.Dependencies) {
		dependency := project.Dependencies[key]
		relative, isPath := dependency.PathValue()
		if !isPath {
			githubDependencies[key] = dependency
			continue
		}
		absolute, err := filepath.Abs(filepath.Join(projectRoot, relative))
		if err != nil {
			return lockfile.PackLock{}, err
		}
		canonical, err := filepath.EvalSymlinks(absolute)
		if err != nil {
			return lockfile.PackLock{}, fmt.Errorf("path dependency %q points to %q which does not exist", key, relative)
		}
		stat, err := os.Stat(canonical)
		if err != nil {
			return lockfile.PackLock{}, fmt.Errorf("inspect path dependency %q at %q: %w", key, canonical, err)
		}
		if !stat.IsDir() {
			return lockfile.PackLock{}, fmt.Errorf("path dependency %q at %q is not a directory", key, canonical)
		}
		cacheKey, commit, destination, err := cache.CopyPackageDirToCache(canonical, "path:"+canonical)
		if err != nil {
			return lockfile.PackLock{}, err
		}
		fileURL := cache.FileURL(canonical)
		pkg, err := cache.ClassifyMaterialized(destination, fileURL, githubsource.Source{Owner: "path", Repo: key, GitRef: githubsource.DefaultGitRef}, commit, cacheKey)
		if err != nil {
			return lockfile.PackLock{}, err
		}
		if pkg.ContentHash, err = cache.TreeDigest(destination); err != nil {
			return lockfile.PackLock{}, err
		}
		pkg.Module, pkg.Direct = key, true
		pathPackages = append(pathPackages, pkg)
		nested, err := manifest.LoadNestedDependencies(destination)
		if err != nil {
			return lockfile.PackLock{}, err
		}
		for nestedKey, nestedDependency := range nested {
			if _, isNestedPath := nestedDependency.PathValue(); isNestedPath {
				return lockfile.PackLock{}, fmt.Errorf("transitive path dependencies are not supported (found in path dep %q)", key)
			}
			transitiveFromPath = append(transitiveFromPath, dependencyEntry{key: nestedKey, dependency: nestedDependency})
		}
	}

	merged := make(map[ModuleID]ModuleConstraints)
	queue := make([]ModuleID, 0, len(githubDependencies)+len(transitiveFromPath))
	queued := make(map[ModuleID]bool)
	direct := make(map[ModuleID]bool)
	if err := seedDependencies(githubDependencies, merged, &queue, queued, direct, true); err != nil {
		return lockfile.PackLock{}, err
	}
	sort.Slice(transitiveFromPath, func(i, j int) bool { return transitiveFromPath[i].key < transitiveFromPath[j].key })
	for _, entry := range transitiveFromPath {
		if err := seedDependency(entry.key, entry.dependency, merged, &queue, queued, direct, false); err != nil {
			return lockfile.PackLock{}, err
		}
	}
	resolved := make(map[ModuleID]lockfile.Package)
	for len(queue) != 0 {
		module := queue[0]
		queue = queue[1:]
		if _, done := resolved[module]; done {
			continue
		}
		constraints := merged[module]
		owner, repo, _ := module.OwnerRepoPath()
		refresh := (options.RefreshFloating || options.RefreshModules[string(module)]) && constraints.Exact == ""
		gitRef, err := effectiveGitRef(constraints, resolver.Tags, owner, repo, module, refresh, options.Previous)
		if err != nil {
			return lockfile.PackLock{}, err
		}
		source := module.GitHubSource(gitRef)
		pkg, err := resolver.Materialize(ctx, resolver.Client, source, githubsource.CanonicalTreeURL(source), refresh)
		if err != nil {
			return lockfile.PackLock{}, err
		}
		pkg.Module, pkg.Direct = string(module), direct[module]
		if pkg, err = resolver.settleContentHash(pkg, options); err != nil {
			return lockfile.PackLock{}, err
		}
		resolved[module] = pkg
		destination, err := cache.EntryDir(pkg.CacheKey)
		if err != nil {
			return lockfile.PackLock{}, err
		}
		nested, err := manifest.LoadNestedDependencies(destination)
		if err != nil {
			return lockfile.PackLock{}, err
		}
		for _, key := range sortedDependencyKeys(nested) {
			child, incoming, err := dependencyConstraint(key, nested[key])
			if err != nil {
				return lockfile.PackLock{}, err
			}
			current := merged[child]
			if err := current.Merge(incoming); err != nil {
				return lockfile.PackLock{}, err
			}
			merged[child] = current
			if pinned, done := resolved[child]; done {
				if current.Exact != "" && current.Exact != pinned.Commit {
					return lockfile.PackLock{}, fmt.Errorf("transitive dependency %q must be at commit %s, but is already pinned at %s", child, current.Exact, pinned.Commit)
				}
				continue
			}
			if !queued[child] {
				queued[child] = true
				queue = append(queue, child)
			}
		}
	}
	lock.Packages = append(lock.Packages, pathPackages...)
	for _, pkg := range resolved {
		lock.Packages = append(lock.Packages, pkg)
	}
	sort.Slice(lock.Packages, func(i, j int) bool { return lock.Packages[i].Module < lock.Packages[j].Module })
	return lock, nil
}

// settleContentHash decides the content hash of a materialized package. A hash
// already in the lock for the same cache key always wins and the cache must
// match it; a new hash is only ever taken from bytes fetched in this run.
func (resolver Resolver) settleContentHash(pkg lockfile.Package, options ResolveOptions) (lockfile.Package, error) {
	expected := ""
	if options.Previous != nil {
		for _, previous := range options.Previous.Packages {
			if previous.CacheKey != pkg.CacheKey {
				continue
			}
			// Same slot means same repository, path and commit: keep the
			// entry's URL as locked so an unchanged pin rewrites nothing.
			if previous.URL != "" {
				pkg.URL = previous.URL
			}
			if previous.ContentHash != "" {
				expected = previous.ContentHash
				break
			}
		}
	}
	fetched := pkg.ContentHash
	notify := func(format string, arguments ...any) {
		if options.Notify != nil {
			options.Notify(fmt.Sprintf(format, arguments...))
		}
	}
	switch {
	case expected != "" && fetched != "":
		if fetched == expected {
			return pkg, nil
		}
		if out, err := cache.EntryDir(pkg.CacheKey); err == nil {
			_ = os.RemoveAll(out)
		}
		pkg.ContentHash = expected
		return pkg, &cache.IntegrityError{Package: pkg, Expected: expected, Actual: fetched, Fetched: true}
	case expected != "":
		pkg.ContentHash = expected
		err := cache.VerifyPackage(pkg)
		var mismatch *cache.IntegrityError
		if !options.RepairCache || !errors.As(err, &mismatch) {
			return pkg, err
		}
		if _, _, err := cache.RefetchPackage(pkg, resolver.Restore); err != nil {
			return pkg, err
		}
		notify("%s", mismatch.Repaired())
		return pkg, nil
	case fetched == "" && options.RecordContentHashes:
		digest, replaced, err := cache.RefetchPackage(pkg, resolver.Restore)
		if err != nil {
			return pkg, err
		}
		if replaced {
			notify("cache entry for %s differed from commit %s as fetched now and was replaced", pkg.Module, pkg.Commit)
		}
		pkg.ContentHash = digest
	}
	return pkg, nil
}

func seedDependencies(dependencies map[string]manifest.Dependency, merged map[ModuleID]ModuleConstraints, queue *[]ModuleID, queued, direct map[ModuleID]bool, isDirect bool) error {
	for _, key := range sortedDependencyKeys(dependencies) {
		if err := seedDependency(key, dependencies[key], merged, queue, queued, direct, isDirect); err != nil {
			return err
		}
	}
	return nil
}

func seedDependency(key string, dependency manifest.Dependency, merged map[ModuleID]ModuleConstraints, queue *[]ModuleID, queued, direct map[ModuleID]bool, isDirect bool) error {
	module, incoming, err := dependencyConstraint(key, dependency)
	if err != nil {
		return err
	}
	current := merged[module]
	if err := current.Merge(incoming); err != nil {
		return err
	}
	merged[module] = current
	if !queued[module] {
		queued[module] = true
		*queue = append(*queue, module)
	}
	if isDirect {
		direct[module] = true
	}
	return nil
}

func dependencyConstraint(key string, dependency manifest.Dependency) (ModuleID, ModuleConstraints, error) {
	base, keyRef, hasRef := SplitModuleAtRef(key)
	module, err := ParseModuleID(base)
	if err != nil {
		return "", ModuleConstraints{}, err
	}
	constraints, err := ConstraintsFromDependency(dependency, keyRef, hasRef)
	return module, constraints, err
}

func effectiveGitRef(constraints ModuleConstraints, tags TagLister, owner, repo string, module ModuleID, refresh bool, previous *lockfile.PackLock) (string, error) {
	if constraints.Exact != "" {
		return constraints.Exact, nil
	}
	if !refresh && previous != nil {
		for _, pkg := range previous.Packages {
			if pkg.Module == string(module) {
				return pkg.Commit, nil
			}
		}
	}
	return constraints.PickGitRef(tags, owner, repo, refresh)
}

func sortedDependencyKeys(dependencies map[string]manifest.Dependency) []string {
	keys := make([]string, 0, len(dependencies))
	for key := range dependencies {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

type githubTagLister struct {
	ctx    context.Context
	client *http.Client
}

type dependencyEntry struct {
	key        string
	dependency manifest.Dependency
}

func (lister githubTagLister) ListTags(owner, repo string, forceRefresh bool) ([]Tag, error) {
	tags, err := githubsource.ListTags(lister.ctx, lister.client, owner, repo, forceRefresh)
	if err != nil {
		return nil, err
	}
	result := make([]Tag, len(tags))
	for index, tag := range tags {
		result[index] = Tag{Name: tag.Name, SHA: tag.SHA}
	}
	return result, nil
}
