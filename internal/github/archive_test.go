package github

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"
)

func TestExtractTarballWithPrefixAndCollectPaths(t *testing.T) {
	t.Parallel()
	archive := githubTarball(t, map[string]string{
		"repo-sha/plugins/pkg/SKILL.md": "# Demo",
		"repo-sha/plugins/pkg/extra.md": "extra",
		"repo-sha/elsewhere/file.md":    "skip",
	})
	paths, err := CollectRepoRelativePaths(bytes.NewReader(archive))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := paths["plugins/pkg/SKILL.md"]; !ok {
		t.Fatalf("paths = %v", paths)
	}
	destination := t.TempDir()
	count, err := ExtractTarballWithPrefix(bytes.NewReader(archive), "plugins/pkg", destination)
	if err != nil || count != 2 {
		t.Fatalf("ExtractTarballWithPrefix() = %d, %v", count, err)
	}
	if body, err := os.ReadFile(filepath.Join(destination, "SKILL.md")); err != nil || string(body) != "# Demo" {
		t.Fatalf("SKILL.md = %q, %v", body, err)
	}
}

func TestArchivePathHelpers(t *testing.T) {
	t.Parallel()
	paths := map[string]struct{}{"plugins/demo/.claude-plugin/plugin.json": {}}
	root := func(paths map[string]struct{}, directory string) bool {
		_, ok := paths[directory+"/.claude-plugin/plugin.json"]
		return ok
	}
	prefix, ok := ChoosePackagePrefix(paths, "plugins/demo/agents/a.md", root)
	if !ok || prefix != "plugins/demo" {
		t.Fatalf("ChoosePackagePrefix() = %q, %v", prefix, ok)
	}
	if !PathLooksLikeFile("plugins/demo/agents/a.md") || PathLooksLikeFile("skills/demo/SKILL.md") {
		t.Fatal("file path detection mismatch")
	}
	if _, err := safeExtractPath(t.TempDir(), "../../escape"); err == nil {
		t.Fatal("expected traversal rejection")
	}
}

func githubTarball(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var compressed bytes.Buffer
	gzipWriter := gzip.NewWriter(&compressed)
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
	return compressed.Bytes()
}
