package sync

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/OlegHQ/agentpack/internal/cache"
	githubsource "github.com/OlegHQ/agentpack/internal/github"
	"github.com/OlegHQ/agentpack/internal/lockfile"
	"github.com/OlegHQ/agentpack/internal/paths"
	"github.com/OlegHQ/agentpack/internal/resolve"
)

type ResolvedAdd struct {
	Package   lockfile.Package
	Shorthand string
	GitRef    string
}

type AddResolver struct {
	Client      *http.Client
	Materialize cacheMaterializeFunc
}

type cacheMaterializeFunc func(context.Context, *http.Client, githubsource.Source, string, bool) (lockfile.Package, error)

func NewAddResolver(client *http.Client) AddResolver {
	return AddResolver{Client: client, Materialize: cache.MaterializeGitHubTree}
}

func (resolver AddResolver) Resolve(ctx context.Context, cwd, rawSpec string) (ResolvedAdd, error) {
	spec := strings.TrimSpace(rawSpec)
	if spec == "" {
		return ResolvedAdd{}, fmt.Errorf("empty add spec")
	}
	if resolver.Materialize == nil {
		resolver.Materialize = cache.MaterializeGitHubTree
	}
	if strings.HasPrefix(spec, "http://") || strings.HasPrefix(spec, "https://") {
		source, err := githubsource.ParseURL(spec)
		if err != nil {
			return ResolvedAdd{}, fmt.Errorf("only https://github.com/… URLs are supported: %w", err)
		}
		pkg, err := resolver.Materialize(ctx, resolver.Client, source, spec, false)
		return ResolvedAdd{Package: pkg}, err
	}
	if local, ok := existingDirectory(cwd, spec); ok {
		pkg, err := addFilesystem(local)
		return ResolvedAdd{Package: pkg}, err
	}
	base, gitRef, hasRef := resolve.SplitModuleAtRef(spec)
	parts := nonemptyParts(base)
	if len(parts) != 0 && parts[0] == githubsource.Host {
		parts = parts[1:]
	}
	switch len(parts) {
	case 0:
		return ResolvedAdd{}, fmt.Errorf("invalid add spec")
	case 1:
		pkg, err := resolver.addOne(ctx, parts[0])
		return ResolvedAdd{Package: pkg}, err
	default:
		owner, repo, inRepoPath := parts[0], parts[1], strings.Join(parts[2:], "/")
		shorthand := strings.Join(parts, "/")
		if !hasRef {
			if mirror, ok := localMirror(shorthand); ok {
				pkg, err := addLocalMirror(mirror, shorthand, owner, repo, inRepoPath)
				return ResolvedAdd{Package: pkg, Shorthand: shorthand}, err
			}
			if pkg, found, err := resolver.fromAlias(ctx, shorthand); err != nil || found {
				return ResolvedAdd{Package: pkg, Shorthand: shorthand}, err
			}
		}
		if !hasRef {
			gitRef = githubsource.DefaultGitRef
		}
		source := githubsource.SourceFromSegmentsRef(owner, repo, inRepoPath, gitRef)
		pkg, err := resolver.Materialize(ctx, resolver.Client, source, githubsource.CanonicalTreeURL(source), false)
		result := ResolvedAdd{Package: pkg, Shorthand: shorthand}
		if hasRef {
			result.GitRef = gitRef
		}
		return result, err
	}
}

func (resolver AddResolver) addOne(ctx context.Context, name string) (lockfile.Package, error) {
	root, err := paths.LocalRegistryRoot()
	if err != nil {
		return lockfile.Package{}, err
	}
	mirror := filepath.Join(root, name)
	if info, err := os.Stat(mirror); err == nil && info.IsDir() {
		return addLocalMirror(mirror, name, "local", name, "")
	}
	if pkg, found, err := resolver.fromAlias(ctx, name); err != nil || found {
		return pkg, err
	}
	return lockfile.Package{}, fmt.Errorf("unknown package %s: not in local/ (%s) and not in cache index", name, mirror)
}

func (resolver AddResolver) fromAlias(ctx context.Context, alias string) (lockfile.Package, bool, error) {
	cacheKey, found, err := cache.LookupAlias(alias)
	if err != nil || !found {
		return lockfile.Package{}, false, err
	}
	record, found, err := cache.GetEntry(cacheKey)
	if err != nil || !found {
		return lockfile.Package{}, false, err
	}
	pkg := lockfile.Package{Kind: record.Kind, URL: record.SourceURL, Owner: record.Owner, Repo: record.Repo, Path: record.Path, Commit: record.Commit, CacheKey: cacheKey}
	ready, err := cache.EnsureLockCached(pkg, cache.GitHubRestore(ctx, resolver.Client))
	if err != nil {
		return lockfile.Package{}, true, err
	}
	if !ready {
		return lockfile.Package{}, true, fmt.Errorf("cache for %s is empty and local/path sources are unavailable here", cacheKey)
	}
	root, err := cache.EntryDir(cacheKey)
	if err != nil {
		return lockfile.Package{}, true, err
	}
	source := githubsource.Source{Owner: record.Owner, Repo: record.Repo, GitRef: githubsource.DefaultGitRef, Path: record.Path}
	pkg, err = cache.ClassifyMaterialized(root, record.SourceURL, source, record.Commit, cacheKey)
	return pkg, true, err
}

func RecordFetched(pkg lockfile.Package, shorthand string) error {
	record := cache.EntryRecord{Kind: pkg.Kind, SourceURL: pkg.URL, Owner: pkg.Owner, Repo: pkg.Repo, Path: pkg.Path, Commit: pkg.Commit, FetchedAtUnix: time.Now().Unix()}
	name := ""
	if pkg.Kind == lockfile.PackageSkill {
		name = skillFolderName(pkg)
	} else if root, err := cache.EntryDir(pkg.CacheKey); err == nil {
		name = pluginName(root)
	}
	aliases := cache.AliasesForGitHubEntry(pkg.Owner, pkg.Repo, pkg.Path, name)
	shorthand = strings.ToLower(strings.TrimSpace(shorthand))
	if shorthand != "" && !slices.Contains(aliases, shorthand) {
		aliases = append(aliases, shorthand)
	}
	return cache.UpsertEntry(pkg.CacheKey, record, aliases)
}

func addFilesystem(directory string) (lockfile.Package, error) {
	cacheKey, commit, root, err := cache.CopyPackageDirToCache(directory, "path:"+directory)
	if err != nil {
		return lockfile.Package{}, err
	}
	fileURL := cache.FileURL(directory)
	name := filepath.Base(directory)
	return cache.ClassifyMaterialized(root, fileURL, githubsource.SourceFromSegments("path", name, ""), commit, cacheKey)
}

func addLocalMirror(directory, spec, owner, repo, inRepoPath string) (lockfile.Package, error) {
	cacheKey, commit, root, err := cache.CopyPackageDirToCache(directory, "local:"+spec)
	if err != nil {
		return lockfile.Package{}, err
	}
	return cache.ClassifyMaterialized(root, "agentpack-local:"+spec, githubsource.SourceFromSegments(owner, repo, inRepoPath), commit, cacheKey)
}

func existingDirectory(cwd, spec string) (string, bool) {
	candidate := spec
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(cwd, candidate)
	}
	canonical, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		return "", false
	}
	info, err := os.Stat(canonical)
	return canonical, err == nil && info.IsDir()
}

func localMirror(spec string) (string, bool) {
	path, err := paths.LocalMirrorPathFromShorthand(spec)
	if err != nil {
		return "", false
	}
	info, err := os.Stat(path)
	return path, err == nil && info.IsDir()
}

func pluginName(root string) string {
	for _, manifestPath := range []string{cache.ClaudePluginManifestPath(root), cache.CursorPluginManifestPath(root), cache.CodexPluginManifestPath(root)} {
		data, err := os.ReadFile(manifestPath)
		if err != nil {
			continue
		}
		var value struct {
			Name string `json:"name"`
		}
		if json.Unmarshal(data, &value) == nil && value.Name != "" {
			return value.Name
		}
	}
	return ""
}

func skillFolderName(pkg lockfile.Package) string {
	if pkg.Path == "" {
		return pkg.Repo
	}
	return filepath.Base(filepath.FromSlash(pkg.Path))
}

func nonemptyParts(value string) []string {
	var result []string
	for _, part := range strings.Split(value, "/") {
		if part != "" {
			result = append(result, part)
		}
	}
	return result
}
