package resolve

import (
	"context"
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
}

type MaterializeFunc func(ctx context.Context, client *http.Client, source githubsource.Source, displayURL string, forceRefresh bool) (lockfile.Package, error)

type Resolver struct {
	Client      *http.Client
	Tags        TagLister
	Materialize MaterializeFunc
}

func NewResolver(ctx context.Context, client *http.Client) Resolver {
	return Resolver{
		Client:      client,
		Tags:        githubTagLister{ctx: ctx, client: client},
		Materialize: cache.MaterializeGitHubTree,
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
		gitRef, err := effectiveGitRef(constraints, resolver.Tags, owner, repo, module, options)
		if err != nil {
			return lockfile.PackLock{}, err
		}
		source := module.GitHubSource(gitRef)
		pkg, err := resolver.Materialize(ctx, resolver.Client, source, githubsource.CanonicalTreeURL(source), options.RefreshFloating)
		if err != nil {
			return lockfile.PackLock{}, err
		}
		pkg.Module, pkg.Direct = string(module), direct[module]
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

func effectiveGitRef(constraints ModuleConstraints, tags TagLister, owner, repo string, module ModuleID, options ResolveOptions) (string, error) {
	if constraints.Exact != "" {
		return constraints.Exact, nil
	}
	if !options.RefreshFloating && options.Previous != nil {
		for _, pkg := range options.Previous.Packages {
			if pkg.Module == string(module) {
				return pkg.Commit, nil
			}
		}
	}
	return constraints.PickGitRef(tags, owner, repo, options.RefreshFloating)
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
