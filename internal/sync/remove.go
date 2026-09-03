package sync

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/OlegHQ/agentpack/internal/cache"
	githubsource "github.com/OlegHQ/agentpack/internal/github"
	"github.com/OlegHQ/agentpack/internal/manifest"
	"github.com/OlegHQ/agentpack/internal/resolve"
)

func ResolveRemoveSpec(cwd, rawSpec string, project *manifest.Manifest) (string, error) {
	spec := strings.TrimSpace(rawSpec)
	if spec == "" {
		return "", fmt.Errorf("empty remove spec")
	}
	if directory, ok := existingDirectory(cwd, spec); ok {
		base := filepath.Base(directory)
		if _, found := project.Dependencies[base]; found {
			return base, nil
		}
	}
	if strings.HasPrefix(spec, "http://") || strings.HasPrefix(spec, "https://") {
		source, err := githubsource.ParseURL(spec)
		if err != nil {
			return "", err
		}
		return firstExistingModule(project, moduleCandidates(source.Owner, source.Repo, source.Path), spec)
	}
	parts := nonemptyParts(spec)
	if len(parts) == 1 {
		tail := strings.ToLower(parts[0])
		var matches []string
		for key := range project.Dependencies {
			lower := strings.ToLower(key)
			if lower == tail || strings.HasSuffix(lower, "/"+tail) {
				matches = append(matches, key)
			}
		}
		if len(matches) == 1 {
			return matches[0], nil
		}
		if len(matches) > 1 {
			sort.Strings(matches)
			return "", fmt.Errorf("ambiguous remove spec %q matches multiple dependencies (%s); specify a fuller owner/repo/path", spec, strings.Join(matches, ", "))
		}
	}
	base, _, _ := resolve.SplitModuleAtRef(spec)
	if len(parts) >= 2 && parts[0] != githubsource.Host {
		return firstExistingModule(project, moduleCandidates(parts[0], parts[1], strings.Join(parts[2:], "/")), spec)
	}
	module, err := resolve.ParseModuleID(base)
	if err != nil {
		return "", err
	}
	owner, repo, path := module.OwnerRepoPath()
	return firstExistingModule(project, moduleCandidates(owner, repo, path), spec)
}

func moduleCandidates(owner, repo, inRepoPath string) []string {
	owner, repo = strings.ToLower(owner), strings.ToLower(repo)
	if githubsource.PathLooksLikeFile(inRepoPath) {
		prefixes := cache.BlobParentPrefixes(inRepoPath)
		result := make([]string, len(prefixes))
		for index, prefix := range prefixes {
			result[index] = string(resolve.ModuleIDFromOwnerRepoPath(owner, repo, prefix))
		}
		return result
	}
	return []string{string(resolve.ModuleIDFromOwnerRepoPath(owner, repo, inRepoPath))}
}

func firstExistingModule(project *manifest.Manifest, candidates []string, raw string) (string, error) {
	for _, candidate := range candidates {
		if _, found := project.Dependencies[candidate]; found {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("dependency not found: %s", raw)
}
