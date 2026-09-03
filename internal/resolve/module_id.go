package resolve

import (
	"fmt"
	"strings"

	githubsource "github.com/OlegHQ/agentpack/internal/github"
)

// ModuleID is a canonical Go-style GitHub module path without an @ref suffix.
type ModuleID string

func ParseModuleID(module string) (ModuleID, error) {
	value := strings.TrimSpace(module)
	if before, _, found := strings.Cut(value, "@"); found {
		value = strings.TrimSpace(before)
	}
	if len(value) >= 2 && strings.HasPrefix(value, "\"") && strings.HasSuffix(value, "\"") {
		value = strings.TrimSpace(value[1 : len(value)-1])
	}
	if value == "" {
		return "", fmt.Errorf("empty module id")
	}
	parts := nonemptyPathParts(value)
	if len(parts) < 3 || parts[0] != githubsource.Host {
		return "", fmt.Errorf("module id must start with %s/<owner>/<repo>[/…], got %q", githubsource.Host, module)
	}
	canonical := githubsource.Host + "/" + strings.ToLower(parts[1]) + "/" + strings.ToLower(parts[2])
	if len(parts) > 3 {
		canonical += "/" + strings.Join(parts[3:], "/")
	}
	return ModuleID(canonical), nil
}

func ModuleIDFromOwnerRepoPath(owner, repo, path string) ModuleID {
	result := githubsource.Host + "/" + strings.ToLower(owner) + "/" + strings.ToLower(repo)
	path = strings.Trim(path, "/")
	if path != "" {
		result += "/" + path
	}
	return ModuleID(result)
}

func (module ModuleID) OwnerRepoPath() (owner, repo, path string) {
	parts := strings.Split(string(module), "/")
	owner, repo = parts[1], parts[2]
	if len(parts) > 3 {
		path = strings.Join(parts[3:], "/")
	}
	return owner, repo, path
}

func (module ModuleID) GitHubSource(gitRef string) githubsource.Source {
	owner, repo, path := module.OwnerRepoPath()
	return githubsource.Source{Owner: owner, Repo: repo, GitRef: gitRef, Path: path}
}

func SplitModuleAtRef(spec string) (module, gitRef string, hasRef bool) {
	index := strings.LastIndex(spec, "@")
	if index > 0 && index < len(spec)-1 && !strings.Contains(spec[:index], "@") {
		return strings.TrimSpace(spec[:index]), strings.TrimSpace(spec[index+1:]), true
	}
	return strings.TrimSpace(spec), "", false
}

func nonemptyPathParts(value string) []string {
	raw := strings.Split(value, "/")
	parts := raw[:0]
	for _, part := range raw {
		if part != "" {
			parts = append(parts, part)
		}
	}
	return parts
}
