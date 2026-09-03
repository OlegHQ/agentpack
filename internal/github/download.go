package github

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
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
// receive the same three-attempt exponential retry policy as other sources.
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
	checkout, err := os.MkdirTemp("", "agentpack-git-archive-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(checkout)
	remote := "https://github.com/" + owner + "/" + repo + ".git"
	repository, err := git.PlainCloneContext(ctx, checkout, false, &git.CloneOptions{URL: remote, NoCheckout: true})
	if err != nil {
		return nil, fmt.Errorf("git clone: %w", err)
	}
	worktree, err := repository.Worktree()
	if err != nil {
		return nil, err
	}
	if err := worktree.Checkout(&git.CheckoutOptions{Hash: plumbing.NewHash(sha), Force: true}); err != nil {
		return nil, fmt.Errorf("git checkout %s: %w", sha, err)
	}
	return archiveCheckout(checkout, repo+"-"+sha)
}

func archiveCheckout(root, prefix string) ([]byte, error) {
	var output bytes.Buffer
	gzipWriter := gzip.NewWriter(&output)
	tarWriter := tar.NewWriter(gzipWriter)
	err := filepath.Walk(root, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == filepath.Join(root, ".git") {
			return filepath.SkipDir
		}
		if path == root {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		link := ""
		if info.Mode()&os.ModeSymlink != 0 {
			link, err = os.Readlink(path)
			if err != nil {
				return err
			}
		}
		header, err := tar.FileInfoHeader(info, link)
		if err != nil {
			return err
		}
		header.Name = filepath.ToSlash(filepath.Join(prefix, relative))
		if info.IsDir() {
			header.Name += "/"
		}
		if err := tarWriter.WriteHeader(header); err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(tarWriter, file)
		closeErr := file.Close()
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	})
	if err != nil {
		return nil, err
	}
	if err := tarWriter.Close(); err != nil {
		return nil, err
	}
	if err := gzipWriter.Close(); err != nil {
		return nil, err
	}
	return bytes.Clone(output.Bytes()), nil
}
