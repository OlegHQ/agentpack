package cache

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	githubsource "github.com/OlegHQ/agentpack/internal/github"
	"github.com/OlegHQ/agentpack/internal/lockfile"
)

func TestMaterializeGitHubTreeDownloadsClassifiesAndReusesCache(t *testing.T) {
	t.Setenv("AGENTPACK_HOME", t.TempDir())
	sha := "a123456789abcdef0123456789abcdef01234567"
	archive := materializeTarball(t, map[string]string{
		"repo-sha/plugins/demo/.claude-plugin/plugin.json": `{"name":"demo"}`,
		"repo-sha/plugins/demo/commands/run.md":            "run",
	})
	calls := 0
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: http.StatusOK, Status: "200 OK", Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(archive)), Request: request}, nil
	})}
	source := githubsource.Source{Owner: "owner", Repo: "repo", GitRef: sha, Path: "plugins/demo"}
	pkg, err := MaterializeGitHubTree(context.Background(), client, source, "https://github.com/owner/repo/tree/main/plugins/demo", false)
	if err != nil {
		t.Fatal(err)
	}
	if pkg.Kind != lockfile.PackagePlugin || pkg.Commit != sha || calls != 1 {
		t.Fatalf("package = %#v; calls=%d", pkg, calls)
	}
	if _, err := MaterializeGitHubTree(context.Background(), client, source, "url", false); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("cached materialization made %d requests", calls)
	}
}

func TestMaterializeBlobChoosesDeepestPackageRoot(t *testing.T) {
	t.Setenv("AGENTPACK_HOME", t.TempDir())
	sha := "b123456789abcdef0123456789abcdef01234567"
	archive := materializeTarball(t, map[string]string{
		"repo-sha/plugins/demo/SKILL.md":       "# Demo",
		"repo-sha/plugins/demo/agents/test.md": "agent",
	})
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Status: "200 OK", Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(archive)), Request: request}, nil
	})}
	source := githubsource.Source{Owner: "owner", Repo: "repo", GitRef: sha, Path: "plugins/demo/agents/test.md"}
	pkg, err := MaterializeGitHubTree(context.Background(), client, source, "https://github.com/owner/repo/blob/main/plugins/demo/agents/test.md", false)
	if err != nil {
		t.Fatal(err)
	}
	if pkg.Path != "plugins/demo" || pkg.Kind != lockfile.PackageSkill {
		t.Fatalf("package = %#v", pkg)
	}
}

func TestBlobParentPrefixes(t *testing.T) {
	t.Parallel()
	got := strings.Join(BlobParentPrefixes("a/b/c/file.md"), "|")
	if got != "a/b/c|a/b|a|" {
		t.Fatalf("prefixes = %q", got)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func materializeTarball(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var output bytes.Buffer
	gzipWriter := gzip.NewWriter(&output)
	tarWriter := tar.NewWriter(gzipWriter)
	for name, body := range files {
		header := &tar.Header{Name: name, Mode: 0o644, Size: int64(len(body))}
		if err := tarWriter.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if _, err := tarWriter.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}
