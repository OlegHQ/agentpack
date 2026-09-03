package github

import (
	"fmt"
	"net/url"
	"path"
	"strings"
)

const (
	Host          = "github.com"
	DefaultGitRef = "HEAD"
)

type Source struct {
	Owner  string
	Repo   string
	GitRef string
	Path   string
}

func SourceFromSegments(owner, repo, inRepoPath string) Source {
	return SourceFromSegmentsRef(owner, repo, inRepoPath, DefaultGitRef)
}

func SourceFromSegmentsRef(owner, repo, inRepoPath, gitRef string) Source {
	if strings.TrimSpace(gitRef) == "" {
		gitRef = DefaultGitRef
	}
	return Source{Owner: owner, Repo: repo, GitRef: strings.TrimSpace(gitRef), Path: inRepoPath}
}

func CanonicalTreeURL(source Source) string {
	result := fmt.Sprintf("https://%s/%s/%s/tree/%s", Host, source.Owner, source.Repo, source.GitRef)
	if inRepoPath := strings.Trim(source.Path, "/"); inRepoPath != "" {
		result += "/" + inRepoPath
	}
	return result
}

func ParseURL(raw string) (Source, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return Source{}, fmt.Errorf("invalid GitHub URL: %w", err)
	}
	host := strings.ToLower(parsed.Hostname())
	if host != Host && !strings.HasSuffix(host, "."+Host) {
		return Source{}, fmt.Errorf("only %s URLs supported, got %s", Host, host)
	}
	segments := nonempty(strings.Split(parsed.EscapedPath(), "/"))
	for index := range segments {
		segments[index], err = url.PathUnescape(segments[index])
		if err != nil {
			return Source{}, fmt.Errorf("decode GitHub URL path: %w", err)
		}
	}
	if len(segments) < 2 {
		return Source{}, fmt.Errorf("expected /owner/repo/...")
	}
	source := Source{Owner: segments[0], Repo: strings.TrimSuffix(segments[1], ".git"), GitRef: DefaultGitRef}
	if len(segments) == 2 {
		return source, nil
	}
	kind := segments[2]
	if (kind != "tree" && kind != "blob") || len(segments) < 5 {
		return Source{}, fmt.Errorf("unsupported GitHub path (expected .../tree/... or .../blob/...): %s", parsed.Path)
	}
	source.GitRef = segments[3]
	source.Path = strings.Join(segments[4:], "/")
	if kind == "blob" {
		base := path.Base(source.Path)
		parent := path.Dir(source.Path)
		if base == "SKILL.md" {
			source.Path = cleanRepoParent(parent)
		} else if base == "plugin.json" {
			manifestDir := path.Base(parent)
			if manifestDir == ".claude-plugin" || manifestDir == ".cursor-plugin" || manifestDir == ".codex-plugin" {
				source.Path = cleanRepoParent(path.Dir(parent))
			}
		}
	}
	return source, nil
}

func NormalizedIdentity(source Source, commit string) string {
	return fmt.Sprintf("github:%s/%s\x00%s\x00%s", strings.ToLower(source.Owner), strings.ToLower(source.Repo), source.Path, strings.ToLower(strings.TrimSpace(commit)))
}

func cleanRepoParent(value string) string {
	if value == "." || value == "/" {
		return ""
	}
	return strings.Trim(value, "/")
}

func nonempty(values []string) []string {
	result := values[:0]
	for _, value := range values {
		if value != "" {
			result = append(result, value)
		}
	}
	return result
}
