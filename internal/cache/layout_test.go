package cache

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
)

func TestComputeKeyIsStableSHA256(t *testing.T) {
	t.Parallel()
	identity := "github:foo/bar\x00path\x00commit"
	sum := sha256.Sum256([]byte(identity))
	if got, want := ComputeKey(identity), hex.EncodeToString(sum[:]); got != want || len(got) != 64 {
		t.Fatalf("ComputeKey() = %q, want %q", got, want)
	}
}

func TestPackageRootDetectorsRecognizeEveryManifest(t *testing.T) {
	t.Parallel()
	for _, relative := range []string{"SKILL.md", ".claude-plugin/plugin.json", ".cursor-plugin/plugin.json", ".codex-plugin/plugin.json", ".agents/plugins/marketplace.json", "agentpack.toml"} {
		t.Run(relative, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, filepath.FromSlash(relative))
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte("{}"), 0o644); err != nil {
				t.Fatal(err)
			}
			if !IsPackageRoot(root) {
				t.Fatalf("IsPackageRoot() missed %s", relative)
			}
			paths := map[string]struct{}{relative: {}}
			if !RepoDirIsPackageRoot(paths, "") {
				t.Fatalf("RepoDirIsPackageRoot() missed %s", relative)
			}
		})
	}
}

func TestSourceFilesRespectsGitignoreForTrackedAndUntrackedFiles(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git unavailable")
	}
	root := t.TempDir()
	for name, body := range map[string]string{".gitignore": "ignored/\n*.tmp\n", "keep.txt": "keep", "tracked.tmp": "tracked", "ignored/file.txt": "ignored"} {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{{"init", "-q"}, {"add", "-f", "tracked.tmp"}} {
		command := exec.Command("git", args...)
		command.Dir = root
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, output)
		}
	}
	files, err := SourceFiles(root)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(files, []string{".gitignore", "keep.txt"}) {
		t.Fatalf("SourceFiles() = %v", files)
	}
}

func TestNativeIgnoreFallbackSupportsNestedNegationAndDoubleStar(t *testing.T) {
	root := t.TempDir()
	for name, body := range map[string]string{
		".gitignore":              "build/\n**/*.tmp\n!keep.tmp\n",
		"keep.tmp":                "keep",
		"drop.tmp":                "drop",
		"nested/.gitignore":       "*.log\n!important.log\n",
		"nested/drop.log":         "drop",
		"nested/important.log":    "keep",
		"nested/deeper/value.tmp": "drop",
		"build/output.txt":        "drop",
		"visible.txt":             "keep",
	} {
		filePath := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(filePath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filePath, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	all := []string{".gitignore", "keep.tmp", "drop.tmp", "nested/.gitignore", "nested/drop.log", "nested/important.log", "nested/deeper/value.tmp", "build/output.txt", "visible.txt"}
	ignored, err := nativeIgnoredFiles(root, all)
	if err != nil {
		t.Fatal(err)
	}
	got := filterIgnored(append([]string(nil), all...), ignored)
	slices.Sort(got)
	want := []string{".gitignore", "keep.tmp", "nested/.gitignore", "nested/important.log", "visible.txt"}
	if !slices.Equal(got, want) {
		t.Fatalf("native ignore kept %v, want %v", got, want)
	}
}

func TestNativeIgnoreFallbackHonorsParentRulesForSubdirectorySource(t *testing.T) {
	repository := t.TempDir()
	if err := os.Mkdir(filepath.Join(repository, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(relative, body string) {
		filePath := filepath.Join(repository, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(filePath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filePath, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(".gitignore", "*.tmp\n/packages/skill/root-only.txt\n")
	write("packages/skill/.gitignore", "!keep.tmp\n")
	root := filepath.Join(repository, "packages", "skill")
	files := []string{"drop.tmp", "keep.tmp", "root-only.txt", "visible.txt"}
	ignored, err := nativeIgnoredFiles(root, files)
	if err != nil {
		t.Fatal(err)
	}
	got := filterIgnored(append([]string(nil), files...), ignored)
	slices.Sort(got)
	want := []string{"keep.tmp", "visible.txt"}
	if !slices.Equal(got, want) {
		t.Fatalf("native ignore kept %v, want %v", got, want)
	}
}

func TestHashAndCopySourceTreeReadsFilesOnceAndMatchesHash(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "SKILL.md"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "nested", "file.txt"), []byte("world"), 0o644); err != nil {
		t.Fatal(err)
	}
	destination := t.TempDir()
	got, err := hashAndCopySourceTree(root, destination)
	if err != nil {
		t.Fatal(err)
	}
	want, err := hashAndCopySourceTree(root, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("hashAndCopySourceTree() = %q, want %q", got, want)
	}
	if body, err := os.ReadFile(filepath.Join(destination, "nested", "file.txt")); err != nil || string(body) != "world" {
		t.Fatalf("copied file = %q, %v", body, err)
	}
}

func BenchmarkHashAndCopySourceTree(b *testing.B) {
	root := b.TempDir()
	payload := make([]byte, 4096)
	for index := range 128 {
		path := filepath.Join(root, "skills", fmt.Sprintf("skill-%03d", index), "SKILL.md")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			b.Fatal(err)
		}
		if err := os.WriteFile(path, payload, 0o644); err != nil {
			b.Fatal(err)
		}
	}
	b.ReportAllocs()
	b.SetBytes(128 * int64(len(payload)))
	destination := b.TempDir()
	b.ResetTimer()
	for range b.N {
		if _, err := hashAndCopySourceTree(root, destination); err != nil {
			b.Fatal(err)
		}
	}
}
