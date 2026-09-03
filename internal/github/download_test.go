package github

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestDownloadTarballRetriesTransientCodeload(t *testing.T) {
	t.Setenv("AGENTPACK_HOME", t.TempDir())
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GH_TOKEN", "")
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) < 3 {
			response.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = response.Write([]byte("archive"))
	}))
	defer server.Close()
	previousRoot, previousWait := codeloadRoot, retryBaseWait
	codeloadRoot, retryBaseWait = server.URL, time.Millisecond
	defer func() { codeloadRoot, retryBaseWait = previousRoot, previousWait }()
	data, err := DownloadTarball(context.Background(), server.Client(), "owner", "repo", "a123456789abcdef0123456789abcdef01234567")
	if err != nil || string(data) != "archive" || calls.Load() != 3 {
		t.Fatalf("DownloadTarball() = %q, %v; calls=%d", data, err, calls.Load())
	}
}

func TestFetchCodeloadClassifiesStatuses(t *testing.T) {
	for status, disposition := range map[int]fetchDisposition{
		http.StatusUnauthorized:        fetchPermanent,
		http.StatusNotFound:            fetchPermanent,
		http.StatusTooManyRequests:     fetchTransient,
		http.StatusInternalServerError: fetchTransient,
		http.StatusTeapot:              fetchFatal,
	} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) { response.WriteHeader(status) }))
			defer server.Close()
			previous := codeloadRoot
			codeloadRoot = server.URL
			defer func() { codeloadRoot = previous }()
			result := fetchCodeload(context.Background(), server.Client(), "o", "r", "sha", "")
			if result.disposition != disposition {
				t.Fatalf("status %d disposition = %d, want %d", status, result.disposition, disposition)
			}
		})
	}
}

func TestArchiveCheckoutProducesExtractableGitHubShape(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "skill"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "skill", "SKILL.md"), []byte("# fixture\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	archive, err := archiveCheckout(root, "repo-deadbeef")
	if err != nil {
		t.Fatal(err)
	}
	destination := t.TempDir()
	if _, err := ExtractTarballWithPrefix(bytes.NewReader(archive), "", destination); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(destination, "skill", "SKILL.md"))
	if err != nil || string(data) != "# fixture\n" {
		t.Fatalf("extracted=%q err=%v", data, err)
	}
}
