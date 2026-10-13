package github

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"runtime"
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

func TestExtractTarballSkipsDirectoryEntryAtSelectedPrefix(t *testing.T) {
	t.Parallel()
	var compressed bytes.Buffer
	gzipWriter := gzip.NewWriter(&compressed)
	tarWriter := tar.NewWriter(gzipWriter)
	for _, header := range []*tar.Header{
		{Name: "repo-sha/plugins/pkg/", Typeflag: tar.TypeDir, Mode: 0o755},
		{Name: "repo-sha/plugins/pkg/SKILL.md", Mode: 0o644, Size: 6},
	} {
		if err := tarWriter.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if header.Size != 0 {
			if _, err := tarWriter.Write([]byte("# Demo")); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	destination := t.TempDir()
	count, err := ExtractTarballWithPrefix(bytes.NewReader(compressed.Bytes()), "plugins/pkg", destination)
	if err != nil || count != 1 {
		t.Fatalf("ExtractTarballWithPrefix() = %d, %v", count, err)
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

func TestExtractTarballKeepsOnlyLinksThatStayInsideTheExtractedTree(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symbolic links need privileges on Windows")
	}
	t.Parallel()
	var compressed bytes.Buffer
	gzipWriter := gzip.NewWriter(&compressed)
	tarWriter := tar.NewWriter(gzipWriter)
	entries := []struct{ name, body, link string }{
		{name: "repo-sha/pkg/engineering/skills/devops/SKILL.md", body: "# DevOps"},
		{name: "repo-sha/pkg/.codex/skills/devops", link: "../../engineering/skills/devops"},
		{name: "repo-sha/pkg/.codex/skills/slash", link: "../../engineering/skills/devops/"},
		{name: "repo-sha/pkg/.codex/skills/up", link: "../../../shared"},
		{name: "repo-sha/pkg/.codex/skills/absolute", link: "/etc"},
		{name: "repo-sha/pkg/.codex/skills/unclean", link: "devops/../../../../outside"},
		{name: "repo-sha/pkg/.codex/skills/devops/planted.md", body: "written through a link"},
		{name: "repo-sha/shared/SKILL.md", body: "# Shared"},
	}
	for _, entry := range entries {
		header := &tar.Header{Name: entry.name, Mode: 0o644, Size: int64(len(entry.body))}
		if entry.link != "" {
			header.Typeflag, header.Linkname, header.Mode = tar.TypeSymlink, entry.link, 0o777
		}
		if err := tarWriter.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if _, err := tarWriter.Write([]byte(entry.body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	destination := t.TempDir()
	if _, err := ExtractTarballWithPrefix(bytes.NewReader(compressed.Bytes()), "pkg", destination); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"devops", "slash"} {
		if body, err := os.ReadFile(filepath.Join(destination, ".codex", "skills", name, "SKILL.md")); err != nil || string(body) != "# DevOps" {
			t.Fatalf("%s through its link = %q, %v", name, body, err)
		}
	}
	for _, name := range []string{"up", "absolute", "unclean"} {
		info, err := os.Lstat(filepath.Join(destination, ".codex", "skills", name))
		if err != nil || !info.Mode().IsRegular() || info.Size() != 0 {
			t.Fatalf("%s must stay an empty file, got %v, %v", name, info, err)
		}
	}
	if _, err := os.Stat(filepath.Join(destination, "engineering", "skills", "devops", "planted.md")); err == nil {
		t.Fatal("an archive entry was written through a link")
	}
}
