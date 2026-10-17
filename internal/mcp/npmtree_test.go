package mcp

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha512"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/olegpustovit/agentpack/internal/lockfile"
)

func tarball(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buffer bytes.Buffer
	zipped := gzip.NewWriter(&buffer)
	writer := tar.NewWriter(zipped)
	for name, body := range files {
		if err := writer.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		writer.Write([]byte(body))
	}
	writer.Close()
	zipped.Close()
	return buffer.Bytes()
}

func sri(data []byte) string {
	sum := sha512.Sum512(data)
	return "sha512-" + base64.StdEncoding.EncodeToString(sum[:])
}

func TestParseNPMLockKeepsEveryRuntimePackageAndFindsTheBin(t *testing.T) {
	lock := `{"lockfileVersion":3,"packages":{
	 "":{"dependencies":{"@scope/server":"1.2.3"}},
	 "node_modules/@scope/server":{"version":"1.2.3","resolved":"https://r/x.tgz","integrity":"sha512-root","bin":{"server":"./dist/cli.js","other":"o.js"}},
	 "node_modules/dep":{"version":"2.0.0","integrity":"sha512-dep"},
	 "node_modules/@scope/server/node_modules/dep":{"version":"1.0.0","integrity":"sha512-nested"},
	 "node_modules/fsevents":{"version":"2.3.3","integrity":"sha512-fs","optional":true,"os":["darwin"]},
	 "node_modules/devonly":{"version":"1.0.0","integrity":"sha512-dev","dev":true}}}`
	tree, bin, err := parseNPMLock([]byte(lock), "@scope/server", "1.2.3", "sha512-root")
	if err != nil {
		t.Fatal(err)
	}
	if bin != "dist/cli.js" {
		t.Fatalf("bin = %q", bin)
	}
	var paths []string
	for _, entry := range tree {
		paths = append(paths, entry.Path)
	}
	want := "node_modules/@scope/server node_modules/@scope/server/node_modules/dep node_modules/dep node_modules/fsevents"
	if strings.Join(paths, " ") != want {
		t.Fatalf("paths = %v", paths)
	}
	if tree[3].OS[0] != "darwin" || !tree[3].Optional {
		t.Fatalf("platform constraints lost: %#v", tree[3])
	}
}

func TestParseNPMLockRefusesARootWithAnotherIntegrity(t *testing.T) {
	lock := `{"lockfileVersion":3,"packages":{"node_modules/p":{"version":"1.0.0","integrity":"sha512-new","bin":"cli.js"}}}`
	if _, _, err := parseNPMLock([]byte(lock), "p", "1.0.0", "sha512-recorded"); err == nil || !strings.Contains(err.Error(), "recorded") {
		t.Fatalf("err = %v", err)
	}
}

func TestTreeDigestIgnoresOrderAndChangesWithAnyVersion(t *testing.T) {
	a := []lockfile.NPMTreeEntry{{Path: "node_modules/a", Version: "1", Integrity: "x"}, {Path: "node_modules/b", Version: "1", Integrity: "y"}}
	b := []lockfile.NPMTreeEntry{a[1], a[0]}
	if TreeDigest(a) != TreeDigest(b) {
		t.Fatal("digest depends on order")
	}
	c := []lockfile.NPMTreeEntry{a[0], {Path: "node_modules/b", Version: "2", Integrity: "y"}}
	if TreeDigest(a) == TreeDigest(c) {
		t.Fatal("digest ignores a changed version")
	}
}

func TestMatchesListFollowsNPM(t *testing.T) {
	cases := []struct {
		list  []string
		value string
		want  bool
	}{{nil, "linux", true}, {[]string{"darwin"}, "linux", false}, {[]string{"linux", "darwin"}, "linux", true},
		{[]string{"!win32"}, "linux", true}, {[]string{"!linux"}, "linux", false}}
	for _, c := range cases {
		if got := matchesList(c.list, c.value, nil); got != c.want {
			t.Errorf("matchesList(%v, %s) = %v", c.list, c.value, got)
		}
	}
}

func registryWith(t *testing.T, packages map[string][]byte) *httptest.Server {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, ok := packages[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Write(data)
	}))
	t.Cleanup(server.Close)
	return server
}

func specFor(registry string, entries []lockfile.NPMTreeEntry) TreeSpec {
	return TreeSpec{Server: "s", Package: "srv", Version: "1.0.0", Registry: registry, Bin: "cli.js", TreeDigest: TreeDigest(entries), Tree: entries}
}

func TestInstallTreeUnpacksTheLockedTreeAndReusesIt(t *testing.T) {
	root := tarball(t, map[string]string{"package/cli.js": "console.log('hi')", "package/package.json": "{}"})
	dep := tarball(t, map[string]string{"package/index.js": "module.exports=1"})
	server := registryWith(t, map[string][]byte{"/srv/-/srv-1.0.0.tgz": root, "/dep/-/dep-2.0.0.tgz": dep})
	entries := []lockfile.NPMTreeEntry{{Path: "node_modules/srv", Version: "1.0.0", Integrity: sri(root)},
		{Path: "node_modules/dep", Version: "2.0.0", Integrity: sri(dep)},
		{Path: "node_modules/fsevents", Version: "2.3.3", Integrity: "sha512-unused", Optional: true, OS: []string{"plan9-never"}}}
	cache := t.TempDir()
	bin, err := InstallTree(context.Background(), server.Client(), cache, specFor(server.URL, entries))
	if err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(bin); err != nil || string(data) != "console.log('hi')" {
		t.Fatalf("bin %s: %q %v", bin, data, err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(filepath.Dir(bin)), "dep", "index.js")); err != nil {
		t.Fatalf("dependency not unpacked: %v", err)
	}
	server.Close() // a finished tree needs no network
	if again, err := InstallTree(context.Background(), nil, cache, specFor(server.URL, entries)); err != nil || again != bin {
		t.Fatalf("reuse: %s %v", again, err)
	}
}

func TestInstallTreeRefusesATamperedTarballAndWritesNothing(t *testing.T) {
	good := tarball(t, map[string]string{"package/cli.js": "good"})
	bad := tarball(t, map[string]string{"package/cli.js": "evil"})
	server := registryWith(t, map[string][]byte{"/srv/-/srv-1.0.0.tgz": bad})
	entries := []lockfile.NPMTreeEntry{{Path: "node_modules/srv", Version: "1.0.0", Integrity: sri(good)}}
	cache := t.TempDir()
	_, err := InstallTree(context.Background(), server.Client(), cache, specFor(server.URL, entries))
	if err == nil || !strings.Contains(err.Error(), "pack.lock records") {
		t.Fatalf("err = %v", err)
	}
	if left, _ := os.ReadDir(cache); len(left) != 0 {
		t.Fatalf("cache not empty after a refused install: %v", left)
	}
}

func TestInstallTreeRefusesASpecWhoseTreeWasEdited(t *testing.T) {
	entries := []lockfile.NPMTreeEntry{{Path: "node_modules/srv", Version: "1.0.0", Integrity: "sha512-a"}}
	spec := specFor("http://unused", entries)
	spec.Tree = []lockfile.NPMTreeEntry{{Path: "node_modules/srv", Version: "1.0.1", Integrity: "sha512-a"}}
	if _, err := InstallTree(context.Background(), nil, t.TempDir(), spec); err == nil || !strings.Contains(err.Error(), "digest") {
		t.Fatalf("err = %v", err)
	}
}

func TestExtractTarballRefusesPathsLeavingThePackage(t *testing.T) {
	data := tarball(t, map[string]string{"package/../../escape.js": "x"})
	if err := extractTarball(data, t.TempDir()); err == nil {
		t.Fatal("a path leaving the package was extracted")
	}
}

func TestApplyTreesStartsALockedServerThroughMCPExec(t *testing.T) {
	command := "npx"
	server := Server{Command: &command, Args: []string{"-y", "@playwright/mcp@latest", "--headless"}}
	entries := Entries{"playwright": {Server: server}}
	record := lockfile.MCPServer{Name: "playwright", Launcher: LauncherNPM, Status: lockfile.MCPPinned, Definition: server.Definition(),
		Requested: "@playwright/mcp@latest", Package: "@playwright/mcp", Version: "0.0.83", Bin: "cli.js",
		Tree: []lockfile.NPMTreeEntry{{Path: "node_modules/@playwright/mcp", Version: "0.0.83", Integrity: "sha512-x"}}}
	record.TreeDigest = TreeDigest(record.Tree)
	dir := t.TempDir()
	out, err := ApplyTrees(entries, []lockfile.MCPServer{record}, "/bin/agentpack", dir)
	if err != nil {
		t.Fatal(err)
	}
	got := out["playwright"].Server
	if *got.Command != "/bin/agentpack" || strings.Join(got.Args, " ") != "mcp-exec --spec "+filepath.Join(dir, "playwright.json")+" -- --headless" {
		t.Fatalf("rewritten to %s %v", *got.Command, got.Args)
	}
	if _, err := os.Stat(filepath.Join(dir, "playwright.json")); err != nil {
		t.Fatal(err)
	}
	if *entries["playwright"].Server.Command != "npx" {
		t.Fatal("ApplyTrees changed its input")
	}
}

func TestInstallTreeReinstallsATreeEditedInTheCache(t *testing.T) {
	root := tarball(t, map[string]string{"package/cli.js": "good"})
	server := registryWith(t, map[string][]byte{"/srv/-/srv-1.0.0.tgz": root})
	entries := []lockfile.NPMTreeEntry{{Path: "node_modules/srv", Version: "1.0.0", Integrity: sri(root)}}
	cache := t.TempDir()
	bin, err := InstallTree(context.Background(), server.Client(), cache, specFor(server.URL, entries))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bin, []byte("evil"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := InstallTree(context.Background(), server.Client(), cache, specFor(server.URL, entries)); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(bin); string(data) != "good" {
		t.Fatalf("edited cache file kept: %q", data)
	}
}
