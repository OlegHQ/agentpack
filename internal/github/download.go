package github

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/OlegHQ/agentpack/internal/paths"
)

var (
	codeloadRoot  = "https://codeload.github.com"
	retryBaseWait = 400 * time.Millisecond
)

type fetchDisposition uint8

const (
	fetchSuccess fetchDisposition = iota
	fetchTransient
	fetchPermanent
	fetchFatal
)

type fetchResult struct {
	data        []byte
	disposition fetchDisposition
	reason      string
	err         error
}

// DownloadTarball tries anonymous codeload first, authenticated codeload for
// private repositories second, then the Git protocol. Transient HTTP failures
// receive the same three-attempt exponential retry policy as the Rust client.
func DownloadTarball(ctx context.Context, client *http.Client, owner, repo, sha string) ([]byte, error) {
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Minute}
	}
	type source struct {
		name  string
		token string
	}
	sources := []source{{name: "codeload-anon"}}
	if token := githubToken(); token != "" {
		sources = append(sources, source{name: "codeload-auth", token: token})
	}
	var attempts []string
	for _, source := range sources {
		result := fetchCodeloadWithRetry(ctx, client, owner, repo, sha, source.token)
		if result.disposition == fetchSuccess {
			return result.data, nil
		}
		if result.disposition == fetchFatal {
			return nil, result.err
		}
		attempts = append(attempts, source.name+": "+result.reason)
	}
	data, err := fetchTarballWithGit(ctx, owner, repo, sha)
	if err == nil {
		return data, nil
	}
	attempts = append(attempts, "git-protocol: "+err.Error())
	short := sha
	if len(short) > 8 {
		short = short[:8]
	}
	return nil, fmt.Errorf("all tarball sources failed for %s/%s@%s:\n  %s", owner, repo, short, strings.Join(attempts, "\n  "))
}

func fetchCodeloadWithRetry(ctx context.Context, client *http.Client, owner, repo, sha, token string) fetchResult {
	var result fetchResult
	for attempt := 0; attempt < 3; attempt++ {
		result = fetchCodeload(ctx, client, owner, repo, sha, token)
		if result.disposition != fetchTransient || attempt == 2 {
			return result
		}
		timer := time.NewTimer(retryBaseWait << attempt)
		select {
		case <-ctx.Done():
			timer.Stop()
			return fetchResult{disposition: fetchFatal, err: ctx.Err()}
		case <-timer.C:
		}
	}
	return result
}

func fetchCodeload(ctx context.Context, client *http.Client, owner, repo, sha, token string) fetchResult {
	url := fmt.Sprintf("%s/%s/%s/tar.gz/%s", codeloadRoot, owner, repo, sha)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fetchResult{disposition: fetchFatal, err: err}
	}
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := client.Do(request)
	if err != nil {
		return fetchResult{disposition: fetchTransient, reason: "network: " + err.Error()}
	}
	defer response.Body.Close()
	switch {
	case response.StatusCode >= 200 && response.StatusCode < 300:
		data, err := io.ReadAll(response.Body)
		if err != nil {
			return fetchResult{disposition: fetchTransient, reason: "network: read body: " + err.Error()}
		}
		return fetchResult{data: data, disposition: fetchSuccess}
	case response.StatusCode == 401 || response.StatusCode == 403 || response.StatusCode == 404:
		return fetchResult{disposition: fetchPermanent, reason: response.Status}
	case response.StatusCode == 429 || response.StatusCode >= 500:
		return fetchResult{disposition: fetchTransient, reason: response.Status}
	default:
		return fetchResult{disposition: fetchFatal, err: fmt.Errorf("GET %s -> %s", url, response.Status)}
	}
}

func fetchTarballWithGit(ctx context.Context, owner, repo, sha string) ([]byte, error) {
	if !isFullCommit(sha) {
		return nil, fmt.Errorf("invalid commit sha %q", sha)
	}
	home, err := paths.EnsureUserAgentpackLayout()
	if err != nil {
		return nil, err
	}
	clone := filepath.Join(home, "git-protocol", "clones", owner+"--"+repo+".git")
	if err := os.MkdirAll(filepath.Dir(clone), 0o755); err != nil {
		return nil, fmt.Errorf("create Git cache: %w", err)
	}
	remote := "https://github.com/" + owner + "/" + repo + ".git"
	if _, err := os.Stat(filepath.Join(clone, "HEAD")); errors.Is(err, os.ErrNotExist) {
		if err := runGit(ctx, "clone", "--bare", "--filter=blob:none", remote, clone); err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, err
	} else if err := runGitIn(ctx, clone, "fetch", "--force", "--prune", remote, "+refs/heads/*:refs/heads/*", "+refs/tags/*:refs/tags/*"); err != nil {
		return nil, err
	}
	command := exec.CommandContext(ctx, "git", "-C", clone, "archive", "--format=tar.gz", "--prefix="+repo+"-"+sha+"/", sha)
	output, err := command.Output()
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return nil, fmt.Errorf("git archive: %w: %s", err, strings.TrimSpace(string(exit.Stderr)))
		}
		return nil, fmt.Errorf("git archive: %w", err)
	}
	return bytes.Clone(output), nil
}

func runGit(ctx context.Context, arguments ...string) error {
	command := exec.CommandContext(ctx, "git", arguments...)
	output, err := command.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git %s: %w: %s", arguments[0], err, strings.TrimSpace(string(output)))
	}
	return nil
}

func runGitIn(ctx context.Context, directory string, arguments ...string) error {
	all := append([]string{"-C", directory}, arguments...)
	return runGit(ctx, all...)
}
