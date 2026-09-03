package cache

import (
	"fmt"
	"path/filepath"
	"strings"

	githubsource "github.com/OlegHQ/agentpack/internal/github"
	"github.com/OlegHQ/agentpack/internal/lockfile"
)

func DependencyKey(module, owner, repo, path string) string {
	if module != "" {
		return module
	}
	if owner == "path" {
		return repo
	}
	key := "github.com/" + strings.ToLower(owner) + "/" + strings.ToLower(repo)
	if path = strings.Trim(path, "/"); path != "" {
		key += "/" + path
	}
	return key
}

func ClassifyMaterialized(root, displayURL string, source githubsource.Source, commit, cacheKey string) (lockfile.Package, error) {
	if err := NormalizePluginLayout(root); err != nil {
		return lockfile.Package{}, err
	}
	kind := lockfile.PackageSkill
	if HasPluginManifest(root) {
		if err := EnsurePlugin(root); err != nil {
			return lockfile.Package{}, err
		}
		kind = lockfile.PackagePlugin
	} else if !regularFile(filepath.Join(root, "SKILL.md")) {
		return lockfile.Package{}, fmt.Errorf("invalid cache layout: %s", root)
	}
	module := ""
	if source.Owner != "path" && source.Owner != "local" {
		module = DependencyKey("", source.Owner, source.Repo, source.Path)
	}
	return lockfile.Package{
		Module: module, Kind: kind, URL: displayURL, Owner: source.Owner,
		Repo: source.Repo, Path: source.Path, Commit: commit, CacheKey: cacheKey,
	}, nil
}

func RequireSkill(pkg lockfile.Package) (lockfile.Package, error) {
	if pkg.Kind == lockfile.PackagePlugin {
		return lockfile.Package{}, fmt.Errorf("this path is a full plugin directory (native plugin manifest present); add it as a plugin entry instead of a bare skill")
	}
	return pkg, nil
}
