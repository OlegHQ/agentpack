package sync

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/OlegHQ/agentpack/internal/cache"
	githubsource "github.com/OlegHQ/agentpack/internal/github"
	"github.com/OlegHQ/agentpack/internal/lockfile"
	"github.com/OlegHQ/agentpack/internal/paths"
	"github.com/OlegHQ/agentpack/internal/resolve"
)

// AddPlugin adds one named plugin of a marketplace catalog. The catalog entry
// says where the plugin lives: a directory of the marketplace repository, or
// another repository (optionally a directory of it) at a ref or commit. The
// plugin is then added like any other package, so the manifest names the
// plugin's own repository and path and pack.lock pins its commit.
func (service Service) AddPlugin(ctx context.Context, projectRoot, marketplace, plugin string, noSync bool) (lockfile.Package, error) {
	spec, err := service.catalogPluginSpec(ctx, marketplace, plugin)
	if err != nil {
		return lockfile.Package{}, err
	}
	return service.Add(ctx, projectRoot, spec, noSync)
}

func (service Service) catalogPluginSpec(ctx context.Context, marketplace, plugin string) (string, error) {
	var source githubsource.Source
	raw := strings.TrimSpace(marketplace)
	hasRef := false
	if strings.HasPrefix(raw, "http://") || strings.HasPrefix(raw, "https://") {
		parsed, err := githubsource.ParseURL(raw)
		if err != nil {
			return "", fmt.Errorf("--plugin needs a GitHub marketplace repository: %w", err)
		}
		source, hasRef = parsed, parsed.GitRef != githubsource.DefaultGitRef
	} else {
		base, ref, found := resolve.SplitModuleAtRef(raw)
		parts := nonemptyParts(base)
		if len(parts) != 0 && parts[0] == githubsource.Host {
			parts = parts[1:]
		}
		if len(parts) < 2 {
			return "", fmt.Errorf("--plugin needs a marketplace repository as owner/repo[@ref], got %q", marketplace)
		}
		source, hasRef = githubsource.SourceFromSegmentsRef(parts[0], parts[1], strings.Join(parts[2:], "/"), ref), found
	}
	commit, err := githubsource.ResolveRef(ctx, service.client(), source.Owner, source.Repo, source.GitRef, false)
	if err != nil {
		return "", err
	}
	archive, err := githubsource.DownloadTarball(ctx, service.client(), source.Owner, source.Repo, commit)
	if err != nil {
		return "", err
	}
	entry, err := cache.ResolveCatalogPlugin(archive, source.Path, plugin)
	if err != nil {
		return "", fmt.Errorf("%s/%s: %w", source.Owner, source.Repo, err)
	}
	owner, repo, ref := entry.Owner, entry.Repo, entry.Ref
	if entry.Local {
		// A plugin inside the marketplace repository follows the ref the
		// marketplace was named with, like any package of that repository.
		owner, repo, ref = source.Owner, source.Repo, ""
		if hasRef {
			ref = source.GitRef
		}
	}
	spec := owner + "/" + repo
	if entry.Path != "" {
		spec += "/" + entry.Path
	}
	if ref != "" {
		spec += "@" + ref
	}
	return spec, nil
}

// keepProjectFiles remembers agentpack.toml and pack.lock as they are and
// returns a function that puts them back, so that a command that fails leaves
// the project as it found it.
func keepProjectFiles(projectRoot string) func() error {
	type kept struct {
		path    string
		data    []byte
		existed bool
	}
	var files []kept
	for _, path := range []string{paths.ManifestPath(projectRoot), paths.LockPath(projectRoot)} {
		data, err := os.ReadFile(path)
		files = append(files, kept{path: path, data: data, existed: err == nil})
	}
	return func() error {
		for _, file := range files {
			if !file.existed {
				if err := os.Remove(file.path); err != nil && !os.IsNotExist(err) {
					return err
				}
				continue
			}
			if current, err := os.ReadFile(file.path); err == nil && string(current) == string(file.data) {
				continue
			}
			if err := os.WriteFile(file.path, file.data, 0o644); err != nil {
				return err
			}
		}
		return nil
	}
}
