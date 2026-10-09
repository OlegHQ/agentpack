package cache

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	githubsource "github.com/OlegHQ/agentpack/internal/github"
	"github.com/OlegHQ/agentpack/internal/lockfile"
	"github.com/OlegHQ/agentpack/internal/paths"
)

// IntegrityError reports a package tree that does not match its lock entry.
// Fetched distinguishes bytes just downloaded from an existing cache entry.
type IntegrityError struct {
	Package  lockfile.Package
	Path     string
	Expected string
	Actual   string
	Fetched  bool
}

func (err *IntegrityError) Error() string {
	if err.Fetched {
		return fmt.Sprintf("content hash mismatch for %s: the content fetched for commit %s is %s but pack.lock pins %s; nothing was cached or staged; either the source now serves different content for that commit or pack.lock was edited, so restore pack.lock from version control and do not accept the fetched content without reviewing it",
			err.name(), err.Package.Commit, err.Actual, err.Expected)
	}
	return fmt.Sprintf("content hash mismatch for %s: the cache entry %s is %s but pack.lock pins %s; nothing was staged from it; inspect the directory, then run `agentpack sync --repair` to re-fetch commit %s and verify it again",
		err.name(), err.Path, err.Actual, err.Expected, err.Package.Commit)
}

// Details lays the same facts out one per line, so the digests and the cache
// path survive a renderer that reflows error text.
func (err *IntegrityError) Details() string {
	lines := []string{"content hash mismatch: " + err.name(), "  expected  " + err.Expected, "  actual    " + err.Actual}
	if err.Fetched {
		lines = append(lines, "  source    freshly fetched commit "+err.Package.Commit, "  fix       restore pack.lock from version control; if it is intact, the source changed what it serves for this commit")
	} else {
		lines = append(lines, "  cache     "+err.Path, "  commit    "+err.Package.Commit, "  repair    agentpack sync --repair   (re-fetches the commit and verifies it again)")
	}
	return strings.Join(lines, "\n") + "\n"
}

// Summary is the short form to show next to Details.
func (err *IntegrityError) Summary() string {
	if err.Fetched {
		return fmt.Sprintf("content hash mismatch for %s: nothing was cached or staged", err.name())
	}
	return fmt.Sprintf("content hash mismatch for %s: nothing was staged from the cache entry", err.name())
}

func (err *IntegrityError) name() string {
	if err.Package.Module != "" {
		return err.Package.Module
	}
	return err.Package.CacheKey
}

// LockEntryError reports a lock entry whose integrity fields contradict each
// other, which only happens when the entry was edited after it was locked.
type LockEntryError struct {
	Package  lockfile.Package
	Expected string
}

func (err *LockEntryError) Error() string {
	return fmt.Sprintf("pack.lock entry for %s is inconsistent: cache_key %s does not belong to commit %s (that commit has cache_key %s), so the entry was edited after it was locked; nothing was fetched or staged; restore pack.lock from version control, and move a pin with `agentpack update` or `agentpack lock --update`",
		err.Package.Module, err.Package.CacheKey, err.Package.Commit, err.Expected)
}

func (err *LockEntryError) Details() string {
	return strings.Join([]string{
		"inconsistent pack.lock entry: " + err.Package.Module,
		"  commit     " + err.Package.Commit,
		"  cache_key  " + err.Package.CacheKey,
		"  expected   " + err.Expected + "   (the cache_key of that commit)",
		"  fix        restore pack.lock from version control; move a pin with `agentpack update` or `agentpack lock --update`",
	}, "\n") + "\n"
}

func (err *LockEntryError) Summary() string {
	return fmt.Sprintf("pack.lock entry for %s was edited after it was locked: nothing was fetched or staged", err.Package.Module)
}

// CheckLockEntries verifies that every content-hashed GitHub entry still
// names the cache slot of its own repository, path and commit. agentpack
// writes the three together, so a commit that no longer matches its cache_key
// means the lock was edited by hand, and the content hash describes some
// other commit.
func CheckLockEntries(lock lockfile.PackLock) error {
	for _, pkg := range lock.Packages {
		if pkg.ContentHash == "" || isLocalPackage(pkg) || strings.HasPrefix(pkg.URL, "agentpack-local:") {
			continue
		}
		source := githubsource.Source{Owner: pkg.Owner, Repo: pkg.Repo, Path: pkg.Path}
		if expected := ComputeKey(githubsource.NormalizedIdentity(source, pkg.Commit)); pkg.CacheKey != expected {
			return &LockEntryError{Package: pkg, Expected: expected}
		}
	}
	return nil
}

// Repaired is the one-line report for a mismatch that RefetchPackage fixed.
func (err *IntegrityError) Repaired() string {
	return fmt.Sprintf("repaired %s: re-fetched commit %s into %s and verified %s (the entry was %s)",
		err.Package.Module, err.Package.Commit, err.Path, err.Expected, err.Actual)
}

// TreeDigest computes the sha256-tree-v1 digest of a package directory.
//
// Every regular file and symbolic link below root is one record; directories
// and their modes are not recorded, so empty directories do not count. Records
// are ordered by slash-separated relative path, compared bytewise, and each is
// written to one SHA-256 stream as
//
//	kind 0x00 path 0x00 decimal-length 0x00 payload
//
// where kind is "f" for a regular file, "x" for a regular file with any
// execute permission bit, and "l" for a symbolic link. The payload is the file
// bytes, or the link target for a link, which is never followed. Other
// permission bits, ownership, and timestamps are ignored. Any other file type
// is an error. The result is the algorithm prefix followed by lowercase hex.
func TreeDigest(root string) (string, error) {
	info, err := os.Lstat(root)
	if err != nil {
		return "", fmt.Errorf("inspect package tree %s: %w", root, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("package tree %s is not a directory", root)
	}
	type record struct {
		relative string
		mode     fs.FileMode
		size     int64
	}
	var records []record
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() && info.Mode()&fs.ModeSymlink == 0 {
			return fmt.Errorf("unsupported file type %s at %s", info.Mode().Type(), relative)
		}
		records = append(records, record{relative: filepath.ToSlash(relative), mode: info.Mode(), size: info.Size()})
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("walk package tree %s: %w", root, err)
	}
	sort.Slice(records, func(i, j int) bool { return records[i].relative < records[j].relative })
	hasher := sha256.New()
	buffer := make([]byte, 32*1024)
	header := func(kind, relative string, length int64) {
		hasher.Write([]byte(kind))
		hasher.Write([]byte{0})
		hasher.Write([]byte(relative))
		hasher.Write([]byte{0})
		hasher.Write([]byte(strconv.FormatInt(length, 10)))
		hasher.Write([]byte{0})
	}
	for _, item := range records {
		path := filepath.Join(root, filepath.FromSlash(item.relative))
		if item.mode&fs.ModeSymlink != 0 {
			target, err := os.Readlink(path)
			if err != nil {
				return "", fmt.Errorf("read link %s: %w", path, err)
			}
			header("l", item.relative, int64(len(target)))
			hasher.Write([]byte(target))
			continue
		}
		kind := "f"
		if item.mode.Perm()&0o111 != 0 {
			kind = "x"
		}
		file, err := os.Open(path)
		if err != nil {
			return "", fmt.Errorf("open %s: %w", path, err)
		}
		header(kind, item.relative, item.size)
		// The wrapper hides os.File's WriterTo so the shared buffer is used.
		written, copyErr := io.CopyBuffer(hasher, struct{ io.Reader }{file}, buffer)
		closeErr := file.Close()
		if copyErr != nil || closeErr != nil {
			return "", fmt.Errorf("read %s: %w", path, errors.Join(copyErr, closeErr))
		}
		if written != item.size {
			return "", fmt.Errorf("read %s: file changed while hashing", path)
		}
	}
	return lockfile.ContentHashPrefix + hex.EncodeToString(hasher.Sum(nil)), nil
}

// VerifyPackage recomputes the cache entry digest and compares it with the
// lock. A package without a content hash is unverified and passes.
func VerifyPackage(pkg lockfile.Package) error {
	if pkg.ContentHash == "" || pkg.CacheKey == "" {
		return nil
	}
	out, err := EntryDir(pkg.CacheKey)
	if err != nil {
		return err
	}
	actual, err := TreeDigest(out)
	if err != nil {
		return fmt.Errorf("verify %s: %w", pkg.Module, err)
	}
	if actual != pkg.ContentHash {
		return &IntegrityError{Package: pkg, Path: out, Expected: pkg.ContentHash, Actual: actual}
	}
	return nil
}

// StatFingerprint digests the metadata of every content-hashed package's cache
// entry: each file's path, type, permission bits, size, and modification time.
// It reads no file contents, so it cannot replace VerifyPackage. It answers one
// question cheaply: has anything visibly changed since the entries were last
// verified in full?
func StatFingerprint(lock lockfile.PackLock) (string, error) {
	hasher := sha256.New()
	for _, pkg := range lock.Packages {
		if pkg.CacheKey == "" || pkg.ContentHash == "" {
			continue
		}
		out, err := EntryDir(pkg.CacheKey)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(hasher, "%s\x00%s\x00", pkg.CacheKey, pkg.ContentHash)
		if info, err := os.Lstat(out); err != nil || !info.IsDir() {
			fmt.Fprint(hasher, "absent\x00")
			continue
		}
		err = filepath.WalkDir(out, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil || entry.IsDir() {
				return walkErr
			}
			info, err := entry.Info()
			if err != nil {
				return err
			}
			relative, err := filepath.Rel(out, path)
			if err != nil {
				return err
			}
			fmt.Fprintf(hasher, "%s\x00%s\x00%d\x00%d\x00", filepath.ToSlash(relative), info.Mode(), info.Size(), info.ModTime().UnixNano())
			return nil
		})
		if err != nil {
			return "", fmt.Errorf("walk package tree %s: %w", out, err)
		}
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}

// RefetchPackage builds the package again from its pinned source in a
// temporary directory and installs it over the cache entry only when it
// differs from what is cached. A fetched tree that contradicts the lock entry's
// content hash is discarded and leaves the cache untouched.
func RefetchPackage(pkg lockfile.Package, restoreRemote RemoteRestoreFunc) (digest string, replaced bool, err error) {
	out, err := EntryDir(pkg.CacheKey)
	if err != nil {
		return "", false, err
	}
	fill, err := packageSource(pkg, restoreRemote)
	if err != nil {
		return "", false, err
	}
	if fill == nil {
		return "", false, fmt.Errorf("cannot re-fetch %s: its source is unavailable here", pkg.Module)
	}
	temporary, digest, err := buildVerified(pkg, fill)
	if err != nil {
		return "", false, err
	}
	defer os.RemoveAll(temporary)
	if current, currentErr := TreeDigest(out); currentErr == nil && current == digest {
		return digest, false, nil
	}
	if err := installEntry(temporary, out); err != nil {
		return "", false, err
	}
	return digest, true, nil
}

// packageSource returns the function that writes the package tree into a
// directory, or nil when the package is local and its source is missing.
func packageSource(pkg lockfile.Package, restoreRemote RemoteRestoreFunc) (func(string) error, error) {
	local, err := localSourceDir(pkg.URL)
	if err != nil {
		return nil, err
	}
	if local != "" {
		if info, statErr := os.Stat(local); statErr == nil && info.IsDir() {
			return func(destination string) error { return copyMergeTree(local, destination) }, nil
		}
	}
	if isLocalPackage(pkg) {
		return nil, nil
	}
	if restoreRemote == nil {
		return nil, fmt.Errorf("cache entry %s is missing and no remote restorer is configured", pkg.CacheKey)
	}
	return func(destination string) error { return restoreRemote(pkg, destination) }, nil
}

// buildVerified fills a temporary sibling of the cache entry, normalizes it,
// and checks it against the lock before anything can read it as cache content.
func buildVerified(pkg lockfile.Package, fill func(string) error) (temporary, digest string, err error) {
	cacheRoot, err := paths.CacheDir()
	if err != nil {
		return "", "", err
	}
	if err := os.MkdirAll(cacheRoot, 0o755); err != nil {
		return "", "", fmt.Errorf("create cache directory %s: %w", cacheRoot, err)
	}
	directory, err := os.MkdirTemp(cacheRoot, ".tmp-fetch-")
	if err != nil {
		return "", "", fmt.Errorf("create cache temporary directory: %w", err)
	}
	temporary = directory
	defer func() {
		if err != nil {
			_ = os.RemoveAll(directory)
		}
	}()
	if err = fill(temporary); err != nil {
		return "", "", err
	}
	if err = os.Chmod(temporary, 0o755); err != nil {
		return "", "", err
	}
	if err = NormalizePluginLayout(temporary); err != nil {
		return "", "", err
	}
	if digest, err = TreeDigest(temporary); err != nil {
		return "", "", err
	}
	if pkg.ContentHash != "" && digest != pkg.ContentHash {
		err = &IntegrityError{Package: pkg, Expected: pkg.ContentHash, Actual: digest, Fetched: true}
		return "", "", err
	}
	return temporary, digest, nil
}

func installEntry(temporary, out string) error {
	if err := os.RemoveAll(out); err != nil {
		return fmt.Errorf("remove cache entry %s: %w", out, err)
	}
	return renameEntry(temporary, out)
}

// renameEntry moves a verified tree into its cache slot. If the slot is
// occupied again, another agentpack process installed the entry between the
// removal and the rename; that is success when it holds the same tree.
func renameEntry(temporary, out string) error {
	err := os.Rename(temporary, out)
	if err == nil {
		return nil
	}
	ours, oursErr := TreeDigest(temporary)
	theirs, theirsErr := TreeDigest(out)
	if oursErr == nil && theirsErr == nil && ours == theirs {
		return nil
	}
	return fmt.Errorf("install cache entry %s: %w", out, err)
}
