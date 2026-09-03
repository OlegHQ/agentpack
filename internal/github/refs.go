package github

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/storage/memory"
)

var apiRoot = "https://api.github.com"

func ResolveRef(ctx context.Context, client *http.Client, owner, repo, gitRef string, forceRefresh bool) (string, error) {
	if isFullCommit(gitRef) {
		return strings.ToLower(gitRef), nil
	}
	cached, hasCached, err := loadCachedRef(owner, repo, gitRef)
	if err != nil {
		return "", err
	}
	if !forceRefresh {
		if hasCached && isFresh(cached.CheckedAtUnix, refCacheTTL) {
			return cached.SHA, nil
		}
		if tags, found, err := loadCachedTags(owner, repo); err != nil {
			return "", err
		} else if found && isFresh(tags.CheckedAtUnix, tagCacheTTL) {
			for _, tag := range tags.Tags {
				if tag.Name == gitRef {
					return tag.SHA, nil
				}
			}
		}
	}
	var body struct {
		SHA string `json:"sha"`
	}
	restErr := githubJSON(ctx, client, fmt.Sprintf("/repos/%s/%s/commits/%s", owner, repo, gitRef), &body)
	sha := strings.ToLower(strings.TrimSpace(body.SHA))
	if restErr == nil && isFullCommit(sha) {
		if err := storeCachedRef(owner, repo, gitRef, sha); err != nil {
			return "", err
		}
		return sha, nil
	}
	if gitSHA, gitErr := resolveRefWithGit(ctx, owner, repo, gitRef); gitErr == nil {
		if err := storeCachedRef(owner, repo, gitRef, gitSHA); err != nil {
			return "", err
		}
		return gitSHA, nil
	}
	if hasCached {
		return cached.SHA, nil
	}
	if restErr != nil {
		return "", restErr
	}
	return "", fmt.Errorf("unexpected sha length: %s", sha)
}

func ListTags(ctx context.Context, client *http.Client, owner, repo string, forceRefresh bool) ([]Tag, error) {
	cached, found, err := loadCachedTags(owner, repo)
	if err != nil {
		return nil, err
	}
	if !forceRefresh && found && isFresh(cached.CheckedAtUnix, tagCacheTTL) {
		return append([]Tag(nil), cached.Tags...), nil
	}
	var response []struct {
		Name   string `json:"name"`
		Commit struct {
			SHA string `json:"sha"`
		} `json:"commit"`
	}
	restErr := githubJSON(ctx, client, fmt.Sprintf("/repos/%s/%s/tags?per_page=100", owner, repo), &response)
	if restErr == nil {
		tags := make([]Tag, 0, len(response))
		for _, tag := range response {
			tags = append(tags, Tag{Name: tag.Name, SHA: strings.ToLower(tag.Commit.SHA)})
		}
		sort.Slice(tags, func(i, j int) bool { return tags[i].Name < tags[j].Name })
		if err := storeCachedTags(owner, repo, tags); err != nil {
			return nil, err
		}
		return tags, nil
	}
	if tags, gitErr := listTagsWithGit(ctx, owner, repo); gitErr == nil {
		if err := storeCachedTags(owner, repo, tags); err != nil {
			return nil, err
		}
		return tags, nil
	}
	if found {
		return append([]Tag(nil), cached.Tags...), nil
	}
	return nil, restErr
}

func githubJSON(ctx context.Context, client *http.Client, endpoint string, destination any) error {
	if client == nil {
		client = &http.Client{Timeout: 120 * time.Second}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, apiRoot+endpoint, nil)
	if err != nil {
		return err
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	if token := githubToken(); token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("GitHub API: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 500))
		return fmt.Errorf("GitHub API: GET %s -> %s: %s", request.URL, response.Status, body)
	}
	if err := json.NewDecoder(response.Body).Decode(destination); err != nil {
		return fmt.Errorf("GitHub API JSON: %w", err)
	}
	return nil
}

func resolveRefWithGit(ctx context.Context, owner, repo, gitRef string) (string, error) {
	output, err := gitLSRemote(ctx, owner, repo, gitRef)
	if err != nil {
		return "", err
	}
	return resolveSHAFromLSRemote(output, gitRef)
}

func listTagsWithGit(ctx context.Context, owner, repo string) ([]Tag, error) {
	output, err := gitLSRemote(ctx, owner, repo, "refs/tags/*")
	if err != nil {
		return nil, err
	}
	return tagPairsFromLSRemote(output), nil
}

func gitLSRemote(ctx context.Context, owner, repo, _ string) (string, error) {
	remote := git.NewRemote(memory.NewStorage(), &config.RemoteConfig{
		Name: "origin",
		URLs: []string{"https://github.com/" + owner + "/" + repo + ".git"},
	})
	references, err := remote.ListContext(ctx, &git.ListOptions{PeelingOption: git.AppendPeeled})
	if err != nil {
		return "", fmt.Errorf("git protocol ls-refs: %w", err)
	}
	hashes := make(map[string]string, len(references))
	for _, reference := range references {
		if !reference.Hash().IsZero() {
			hashes[reference.Name().String()] = reference.Hash().String()
		}
	}
	var output strings.Builder
	for _, reference := range references {
		hash := reference.Hash().String()
		if reference.Hash().IsZero() {
			hash = hashes[reference.Target().String()]
		}
		if hash == "" {
			continue
		}
		fmt.Fprintf(&output, "%s\t%s\n", hash, reference.Name())
	}
	return output.String(), nil
}

func resolveSHAFromLSRemote(output, gitRef string) (string, error) {
	refs := parseLSRemote(output)
	wanted := []string{gitRef, "refs/heads/" + gitRef, "refs/tags/" + gitRef + "^{}", "refs/tags/" + gitRef}
	if gitRef == "HEAD" {
		wanted = []string{"HEAD"}
	}
	for _, name := range wanted {
		if sha := refs[name]; isFullCommit(sha) {
			return strings.ToLower(sha), nil
		}
	}
	return "", fmt.Errorf("git protocol fallback could not resolve ref %s", gitRef)
}

func tagPairsFromLSRemote(output string) []Tag {
	refs := parseLSRemote(output)
	byName := make(map[string]string)
	for ref, sha := range refs {
		if !strings.HasPrefix(ref, "refs/tags/") {
			continue
		}
		name := strings.TrimPrefix(ref, "refs/tags/")
		peeled := strings.TrimSuffix(name, "^{}")
		if strings.HasSuffix(name, "^{}") || byName[peeled] == "" {
			byName[peeled] = strings.ToLower(sha)
		}
	}
	tags := make([]Tag, 0, len(byName))
	for name, sha := range byName {
		tags = append(tags, Tag{Name: name, SHA: sha})
	}
	sort.Slice(tags, func(i, j int) bool { return tags[i].Name < tags[j].Name })
	return tags
}

func parseLSRemote(output string) map[string]string {
	refs := make(map[string]string)
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 {
			refs[fields[1]] = fields[0]
		}
	}
	return refs
}

func isFullCommit(value string) bool {
	if len(value) != 40 {
		return false
	}
	for _, character := range value {
		if !strings.ContainsRune("0123456789abcdefABCDEF", character) {
			return false
		}
	}
	return true
}

func githubToken() string {
	if token := os.Getenv("GITHUB_TOKEN"); token != "" {
		return token
	}
	return os.Getenv("GH_TOKEN")
}
