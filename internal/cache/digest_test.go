package cache

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/OlegHQ/agentpack/internal/lockfile"
)

func TestTreeDigestFollowsTheDocumentedRecordFormat(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "a", "b.md"), "nested")
	writeTestFile(t, filepath.Join(root, "a.txt"), "top")
	// "a.txt" sorts before "a/b.md" bytewise even though a walk visits a/ first.
	want := sha256.New()
	for _, record := range []struct{ path, body string }{{"a.txt", "top"}, {"a/b.md", "nested"}} {
		fmt.Fprintf(want, "f\x00%s\x00%d\x00%s", record.path, len(record.body), record.body)
	}
	got, err := TreeDigest(root)
	if err != nil {
		t.Fatal(err)
	}
	if got != "sha256-tree-v1:"+hex.EncodeToString(want.Sum(nil)) {
		t.Fatalf("TreeDigest() = %s", got)
	}
}

func TestTreeDigestIgnoresLocationTimesAndNonExecutePermissions(t *testing.T) {
	first, second := t.TempDir(), filepath.Join(t.TempDir(), "deeper", "copy")
	writeTestFile(t, filepath.Join(first, "SKILL.md"), "# Demo")
	writeTestFile(t, filepath.Join(first, "scripts", "run.sh"), "echo hi")
	// Same content written in the opposite order, with tighter permissions,
	// an old timestamp, and an extra empty directory.
	writeTestFile(t, filepath.Join(second, "scripts", "run.sh"), "echo hi")
	writeTestFile(t, filepath.Join(second, "SKILL.md"), "# Demo")
	if err := os.Chmod(filepath.Join(second, "SKILL.md"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(second, "scripts"), 0o700); err != nil {
		t.Fatal(err)
	}
	old := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := os.Chtimes(filepath.Join(second, "SKILL.md"), old, old); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(second, "empty"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got, want := mustDigest(t, second), mustDigest(t, first); got != want {
		t.Fatalf("digests differ: %s vs %s", got, want)
	}
}

func TestTreeDigestChangesWithContentPathsModesAndLinks(t *testing.T) {
	build := func(t *testing.T) string {
		root := t.TempDir()
		writeTestFile(t, filepath.Join(root, "SKILL.md"), "# Demo")
		writeTestFile(t, filepath.Join(root, "scripts", "run.sh"), "echo hi")
		return root
	}
	clean := mustDigest(t, build(t))
	for name, tamper := range tamperings() {
		t.Run(name, func(t *testing.T) {
			if (name == "executable" || name == "symlink") && runtime.GOOS == "windows" {
				t.Skip("POSIX file modes and links")
			}
			root := build(t)
			tamper(t, root)
			if mustDigest(t, root) == clean {
				t.Fatal("digest did not change")
			}
		})
	}
}

func TestTreeDigestNeverFollowsLinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX links")
	}
	real, linked := t.TempDir(), filepath.Join(t.TempDir(), "entry")
	writeTestFile(t, filepath.Join(real, "SKILL.md"), "# Demo")
	if err := os.Symlink(real, linked); err != nil {
		t.Fatal(err)
	}
	if _, err := TreeDigest(linked); err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Fatalf("TreeDigest(link to directory) error = %v", err)
	}
	inside := t.TempDir()
	if err := os.Symlink(real, filepath.Join(inside, "skill")); err != nil {
		t.Fatal(err)
	}
	want := sha256.New()
	fmt.Fprintf(want, "l\x00skill\x00%d\x00%s", len(real), real)
	if got := mustDigest(t, inside); got != "sha256-tree-v1:"+hex.EncodeToString(want.Sum(nil)) {
		t.Fatalf("directory link was not hashed as a link: %s", got)
	}
}

// The digest covers the tree after NormalizePluginLayout. If this value moves,
// normalization changed what it writes: existing locks would stop verifying, so
// the algorithm prefix must change with it.
func TestTreeDigestOfNormalizedPluginIsPinned(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, ".claude-plugin", "plugin.json"), `{"name":"demo","version":"1.2.3","description":"Demo"}`)
	writeTestFile(t, filepath.Join(root, ".mcp.json"), `{"mcpServers":{}}`)
	if err := NormalizePluginLayout(root); err != nil {
		t.Fatal(err)
	}
	const want = "sha256-tree-v1:c23f5d16de5e797be4675c72476ec76c09d9abbce416cbc5b289805fd3d684f6"
	if got := mustDigest(t, root); got != want {
		t.Fatalf("normalized plugin digest = %s, want %s", got, want)
	}
}

func TestEnsureLockCachedRejectsTamperedEntryWithoutChangingIt(t *testing.T) {
	for name, tamper := range tamperings() {
		t.Run(name, func(t *testing.T) {
			if (name == "executable" || name == "symlink") && runtime.GOOS == "windows" {
				t.Skip("POSIX file modes and links")
			}
			t.Setenv("AGENTPACK_HOME", t.TempDir())
			pkg := lockfile.Package{Module: "github.com/acme/demo", Kind: lockfile.PackageSkill, Owner: "acme", Repo: "demo", Commit: strings.Repeat("a", 40), CacheKey: "entry"}
			out, _ := EntryDir(pkg.CacheKey)
			writeTestFile(t, filepath.Join(out, "SKILL.md"), "# Demo")
			writeTestFile(t, filepath.Join(out, "scripts", "run.sh"), "echo hi")
			pkg.ContentHash = mustDigest(t, out)
			tamper(t, out)
			tampered := mustDigest(t, out)
			fetched := false
			ready, err := EnsureLockCached(pkg, func(lockfile.Package, string) error { fetched = true; return nil })
			var mismatch *IntegrityError
			if ready || !errors.As(err, &mismatch) || fetched {
				t.Fatalf("EnsureLockCached() = %v, %v; fetched=%v", ready, err, fetched)
			}
			for _, part := range []string{pkg.Module, pkg.ContentHash, tampered, out, "agentpack sync --repair"} {
				if !strings.Contains(err.Error(), part) {
					t.Errorf("error does not name %q:\n%v", part, err)
				}
			}
			if mustDigest(t, out) != tampered {
				t.Fatal("verification changed the cache entry")
			}
			if err := VerifyLockCacheIntegrity(lockfile.PackLock{Packages: []lockfile.Package{pkg}}); !errors.As(err, &mismatch) {
				t.Fatalf("VerifyLockCacheIntegrity() = %v", err)
			}
		})
	}
}

func TestEnsureLockCachedInstallsOnlyAFetchThatMatchesTheLock(t *testing.T) {
	t.Setenv("AGENTPACK_HOME", t.TempDir())
	reference := t.TempDir()
	writeTestFile(t, filepath.Join(reference, "SKILL.md"), "# Demo")
	pkg := lockfile.Package{Module: "github.com/acme/demo", Kind: lockfile.PackageSkill, Owner: "acme", Repo: "demo", Commit: strings.Repeat("a", 40), CacheKey: "entry", ContentHash: mustDigest(t, reference)}
	out, _ := EntryDir(pkg.CacheKey)
	serve := func(body string) RemoteRestoreFunc {
		return func(_ lockfile.Package, destination string) error {
			writeTestFile(t, filepath.Join(destination, "SKILL.md"), body)
			return nil
		}
	}
	ready, err := EnsureLockCached(pkg, serve("# Rewritten upstream"))
	var mismatch *IntegrityError
	if ready || !errors.As(err, &mismatch) || !mismatch.Fetched {
		t.Fatalf("EnsureLockCached(different bytes) = %v, %v", ready, err)
	}
	if _, statErr := os.Stat(out); !os.IsNotExist(statErr) {
		t.Fatalf("mismatching fetch reached the cache: %v", statErr)
	}
	leftovers, _ := filepath.Glob(filepath.Join(filepath.Dir(out), ".tmp-fetch-*"))
	if len(leftovers) != 0 {
		t.Fatalf("temporary fetch directories left behind: %v", leftovers)
	}
	if ready, err := EnsureLockCached(pkg, serve("# Demo")); err != nil || !ready {
		t.Fatalf("EnsureLockCached(matching bytes) = %v, %v", ready, err)
	}
	if mustDigest(t, out) != pkg.ContentHash {
		t.Fatal("installed entry does not match the lock")
	}
}

func TestRenameEntryAcceptsOnlyTheSameTreeInstalledConcurrently(t *testing.T) {
	root := t.TempDir()
	out, same, different := filepath.Join(root, "entry"), filepath.Join(root, "same"), filepath.Join(root, "different")
	// The slot is already filled, as if another process won the race.
	writeTestFile(t, filepath.Join(out, "SKILL.md"), "# Demo")
	writeTestFile(t, filepath.Join(same, "SKILL.md"), "# Demo")
	writeTestFile(t, filepath.Join(different, "SKILL.md"), "# Different")
	if err := renameEntry(same, out); err != nil {
		t.Fatalf("identical concurrent install was rejected: %v", err)
	}
	if err := renameEntry(different, out); err == nil || !strings.Contains(err.Error(), "install cache entry") {
		t.Fatalf("renameEntry(different tree) error = %v", err)
	}
	if body, err := os.ReadFile(filepath.Join(out, "SKILL.md")); err != nil || string(body) != "# Demo" {
		t.Fatalf("occupied slot changed: %q, %v", body, err)
	}
}

func TestRefetchPackageReplacesOnlyWhatDiffersAndNeverAgainstTheLock(t *testing.T) {
	t.Setenv("AGENTPACK_HOME", t.TempDir())
	pkg := lockfile.Package{Module: "github.com/acme/demo", Kind: lockfile.PackageSkill, Owner: "acme", Repo: "demo", Commit: strings.Repeat("a", 40), CacheKey: "entry"}
	out, _ := EntryDir(pkg.CacheKey)
	writeTestFile(t, filepath.Join(out, "SKILL.md"), "# Demo")
	upstream := "# Demo"
	restore := func(_ lockfile.Package, destination string) error {
		writeTestFile(t, filepath.Join(destination, "SKILL.md"), upstream)
		return nil
	}
	digest, replaced, err := RefetchPackage(pkg, restore)
	if err != nil || replaced || digest != mustDigest(t, out) {
		t.Fatalf("RefetchPackage(identical) = %s, %v, %v", digest, replaced, err)
	}
	pkg.ContentHash = digest
	writeTestFile(t, filepath.Join(out, "SKILL.md"), "# Demo\nTAMPER")
	if _, replaced, err = RefetchPackage(pkg, restore); err != nil || !replaced || mustDigest(t, out) != digest {
		t.Fatalf("RefetchPackage(tampered cache) replaced=%v err=%v", replaced, err)
	}
	writeTestFile(t, filepath.Join(out, "SKILL.md"), "# Demo\nTAMPER")
	tampered := mustDigest(t, out)
	upstream = "# Rewritten upstream"
	var mismatch *IntegrityError
	if _, _, err = RefetchPackage(pkg, restore); !errors.As(err, &mismatch) || !mismatch.Fetched {
		t.Fatalf("RefetchPackage(upstream differs) error = %v", err)
	}
	if mustDigest(t, out) != tampered {
		t.Fatal("a fetch that contradicts the lock replaced the cache entry")
	}
}

func TestStatFingerprintSeesEveryVisibleChangeButReadsNoContent(t *testing.T) {
	build := func(t *testing.T) (lockfile.PackLock, string) {
		t.Setenv("AGENTPACK_HOME", t.TempDir())
		out, _ := EntryDir("entry")
		writeTestFile(t, filepath.Join(out, "SKILL.md"), "# Demo")
		writeTestFile(t, filepath.Join(out, "scripts", "run.sh"), "echo hi")
		return lockfile.PackLock{Packages: []lockfile.Package{{Kind: lockfile.PackageSkill, CacheKey: "entry", ContentHash: mustDigest(t, out)}}}, out
	}
	fingerprint := func(t *testing.T, lock lockfile.PackLock) string {
		t.Helper()
		value, err := StatFingerprint(lock)
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	for name, tamper := range tamperings() {
		t.Run(name, func(t *testing.T) {
			if (name == "executable" || name == "symlink") && runtime.GOOS == "windows" {
				t.Skip("POSIX file modes and links")
			}
			lock, out := build(t)
			before := fingerprint(t, lock)
			if fingerprint(t, lock) != before {
				t.Fatal("fingerprint is not stable")
			}
			tamper(t, out)
			if fingerprint(t, lock) == before {
				t.Fatal("fingerprint did not change")
			}
		})
	}
	t.Run("same size and restored time", func(t *testing.T) {
		lock, out := build(t)
		path := filepath.Join(out, "SKILL.md")
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		before := fingerprint(t, lock)
		writeTestFile(t, path, "# Dem0")
		if err := os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
			t.Fatal(err)
		}
		// This is the limit of the metadata check, and why it only ever
		// decides whether to skip a sync, never whether content is intact.
		if fingerprint(t, lock) != before {
			t.Fatal("fingerprint read file contents")
		}
		if err := VerifyLockCacheIntegrity(lock); err == nil {
			t.Fatal("full verification missed the edit")
		}
	})
	t.Run("packages without a content hash are left out", func(t *testing.T) {
		lock, out := build(t)
		lock.Packages[0].ContentHash = ""
		before := fingerprint(t, lock)
		writeTestFile(t, filepath.Join(out, "SKILL.md"), "# Changed")
		if fingerprint(t, lock) != before {
			t.Fatal("an unverified package affected the fingerprint")
		}
	})
}

func BenchmarkTreeDigest(b *testing.B) {
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
	b.ResetTimer()
	for range b.N {
		if _, err := TreeDigest(root); err != nil {
			b.Fatal(err)
		}
	}
}

// Five skills of twelve 4 KiB files each, as checked on a launch whose inputs
// are unchanged: the layout check alone (all a lock without content hashes
// gets), the metadata fingerprint added to it by default, and full hashing.
func BenchmarkLaunchCacheCheck(b *testing.B) {
	b.Setenv("AGENTPACK_HOME", b.TempDir())
	var lock lockfile.PackLock
	payload := make([]byte, 4096)
	for skill := range 5 {
		pkg := lockfile.Package{Kind: lockfile.PackageSkill, CacheKey: fmt.Sprintf("skill-%d", skill)}
		out, _ := EntryDir(pkg.CacheKey)
		for file := range 12 {
			path := filepath.Join(out, "references", fmt.Sprintf("file-%02d.md", file))
			if file == 0 {
				path = filepath.Join(out, "SKILL.md")
			}
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				b.Fatal(err)
			}
			if err := os.WriteFile(path, payload, 0o644); err != nil {
				b.Fatal(err)
			}
		}
		digest, err := TreeDigest(out)
		if err != nil {
			b.Fatal(err)
		}
		pkg.ContentHash = digest
		lock.Packages = append(lock.Packages, pkg)
	}
	for name, check := range map[string]func() error{
		"layout-only": func() error { return VerifyLockCacheLayout(lock) },
		"metadata-fingerprint": func() error {
			if _, err := StatFingerprint(lock); err != nil {
				return err
			}
			return VerifyLockCacheLayout(lock)
		},
		"full-hash": func() error { return VerifyLockCacheIntegrity(lock) },
	} {
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				if err := check(); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// tamperings are the ways a cache entry can be changed behind agentpack's back.
func tamperings() map[string]func(*testing.T, string) {
	return map[string]func(*testing.T, string){
		"edited": func(t *testing.T, root string) {
			writeTestFile(t, filepath.Join(root, "SKILL.md"), "# Demo\nTAMPER")
		},
		"added": func(t *testing.T, root string) {
			writeTestFile(t, filepath.Join(root, "scripts", "extra.sh"), "curl evil")
		},
		"deleted": func(t *testing.T, root string) {
			if err := os.Remove(filepath.Join(root, "scripts", "run.sh")); err != nil {
				t.Fatal(err)
			}
		},
		"renamed": func(t *testing.T, root string) {
			if err := os.Rename(filepath.Join(root, "scripts", "run.sh"), filepath.Join(root, "scripts", "go.sh")); err != nil {
				t.Fatal(err)
			}
		},
		"executable": func(t *testing.T, root string) {
			if err := os.Chmod(filepath.Join(root, "scripts", "run.sh"), 0o755); err != nil {
				t.Fatal(err)
			}
		},
		"symlink": func(t *testing.T, root string) {
			// Same bytes when read through the link, but no longer a file.
			target := filepath.Join(t.TempDir(), "run.sh")
			writeTestFile(t, target, "echo hi")
			path := filepath.Join(root, "scripts", "run.sh")
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, path); err != nil {
				t.Fatal(err)
			}
		},
	}
}

func mustDigest(t *testing.T, root string) string {
	t.Helper()
	digest, err := TreeDigest(root)
	if err != nil {
		t.Fatal(err)
	}
	return digest
}
