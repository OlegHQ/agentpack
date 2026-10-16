package cache

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	githubsource "github.com/olegpustovit/agentpack/internal/github"
	"github.com/olegpustovit/agentpack/internal/lockfile"
	"github.com/olegpustovit/agentpack/internal/paths"
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

func TestCopyMergeTreeStopsAtALinkBackToADirectoryBeingCopied(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX links")
	}
	source, destination := t.TempDir(), filepath.Join(t.TempDir(), "copy")
	writeTestFile(t, filepath.Join(source, "skills", "demo", "SKILL.md"), "# Demo")
	if err := os.Symlink("..", filepath.Join(source, "skills", "loop")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("demo", filepath.Join(source, "skills", "alias")); err != nil {
		t.Fatal(err)
	}
	if err := copyMergeTree(source, destination); err != nil {
		t.Fatal(err)
	}
	for _, relative := range []string{"skills/demo/SKILL.md", "skills/alias/SKILL.md"} {
		if _, err := os.Stat(filepath.Join(destination, filepath.FromSlash(relative))); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := os.Stat(filepath.Join(destination, "skills", "loop", "skills")); err == nil {
		t.Fatal("the link back to the copied directory was followed")
	}
}

// gopls-lsp, lua-lsp, php-lsp and pyright-lsp of anthropics/claude-plugins-official
// exist as catalog entries with an lspServers map; their directories hold a
// LICENSE and a README. Claude Code takes such an entry as the plugin's
// manifest, and so does agentpack.
func TestMaterializeTakesAMarketplaceEntryAsThePluginManifest(t *testing.T) {
	t.Setenv("AGENTPACK_HOME", t.TempDir())
	sha := "c123456789abcdef0123456789abcdef01234567"
	archive := materializeTarball(t, map[string]string{
		"repo-sha/.claude-plugin/marketplace.json": `{"name":"official","plugins":[
			{"name":"pyright-lsp","source":"./plugins/pyright-lsp","category":"development","strict":false,"description":"Python","lspServers":{"pyright":{"command":"pyright-langserver","args":["--stdio"],"extensionToLanguage":{".py":"python"}}}},
			{"name":"bundle","source":"./plugins/bundle","strict":false,"skills":["./skills/a"]},
			{"name":"remote","source":{"source":"github","repo":"o/r"}}]}`,
		"repo-sha/plugins/pyright-lsp/README.md": "# pyright",
		"repo-sha/plugins/pyright-lsp/LICENSE":   "Apache",
		"repo-sha/plugins/bundle/README.md":      "# bundle",
		"repo-sha/plugins/unlisted/README.md":    "# unlisted",
	})
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Status: "200 OK", Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(archive)), Request: request}, nil
	})}
	fetch := func(path string) (lockfile.Package, error) {
		source := githubsource.Source{Owner: "owner", Repo: "repo", GitRef: sha, Path: path}
		return MaterializeGitHubTree(context.Background(), client, source, "url", false)
	}
	pkg, err := fetch("plugins/pyright-lsp")
	if err != nil {
		t.Fatal(err)
	}
	out, _ := EntryDir(pkg.CacheKey)
	data, err := os.ReadFile(filepath.Join(out, ".claude-plugin", "plugin.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest map[string]json.RawMessage
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	if _, found := manifest["lspServers"]; !found || manifest["category"] != nil || manifest["strict"] != nil || manifest["source"] != nil {
		t.Fatalf("plugin.json = %s", data)
	}
	// Restoring the entry from the archive gives the same tree, so the lock verifies.
	restored := t.TempDir()
	restore := GitHubRestore(context.Background(), client)
	if err := restore(lockfile.Package{Owner: "owner", Repo: "repo", Commit: sha, Path: "plugins/pyright-lsp"}, restored); err != nil {
		t.Fatal(err)
	}
	if again, _ := os.ReadFile(filepath.Join(restored, ".claude-plugin", "plugin.json")); string(again) != string(data) {
		t.Fatalf("restored plugin.json = %s", again)
	}
	var catalogOnly *CatalogOnlyError
	if _, err := fetch("plugins/bundle"); !errors.As(err, &catalogOnly) || !strings.Contains(err.Error(), "skills ./skills/a") {
		t.Fatalf("catalog entry with a missing path: %v", err)
	}
	if _, err := fetch("plugins/unlisted"); err == nil || errors.As(err, &catalogOnly) || !strings.Contains(err.Error(), "is not a package") {
		t.Fatalf("unlisted directory error = %v", err)
	}
	// A rejected entry leaves nothing behind that a later command could take for a package.
	root, _ := paths.CacheDir()
	entries, _ := os.ReadDir(root)
	for _, entry := range entries {
		if entry.IsDir() && regularFile(filepath.Join(root, entry.Name(), "README.md")) && !regularFile(filepath.Join(root, entry.Name(), "LICENSE")) {
			t.Fatalf("cache kept the rejected entry %s", entry.Name())
		}
	}
}
