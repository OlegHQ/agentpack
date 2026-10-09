package cache

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/OlegHQ/agentpack/internal/lockfile"
	"github.com/OlegHQ/agentpack/internal/paths"
)

type RemoteRestoreFunc func(pkg lockfile.Package, destination string) error

// FileURL returns a portable absolute file URL. Windows drive paths need a
// leading slash so net/url emits file:///C:/... instead of an opaque URL.
func FileURL(filePath string) string {
	slashPath := filepath.ToSlash(filePath)
	if filepath.VolumeName(filePath) != "" && !strings.HasPrefix(slashPath, "/") {
		slashPath = "/" + slashPath
	}
	return (&url.URL{Scheme: "file", Path: slashPath}).String()
}

// EnsureLockCached makes the cache entry for pkg usable and, when the lock
// carries a content hash, proves it matches before returning. An existing
// entry is never repaired here: a mismatch is an IntegrityError. A missing
// entry is built in a temporary directory and installed only once verified.
func EnsureLockCached(pkg lockfile.Package, restoreRemote RemoteRestoreFunc) (bool, error) {
	out, err := EntryDir(pkg.CacheKey)
	if err != nil {
		return false, err
	}
	if pkg.ContentHash != "" && !emptyOrMissingDir(out) {
		if err := VerifyPackage(pkg); err != nil {
			return false, err
		}
	}
	ready, err := cacheReady(pkg, out)
	if err != nil || ready {
		return ready, err
	}
	fill, err := packageSource(pkg, restoreRemote)
	if err != nil || fill == nil {
		return false, err
	}
	temporary, _, err := buildVerified(pkg, fill)
	if err != nil {
		return false, err
	}
	defer os.RemoveAll(temporary)
	if err := installEntry(temporary, out); err != nil {
		return false, err
	}
	return cacheReady(pkg, out)
}

// VerifyLockCacheIntegrity checks every locked package's cache entry: its
// content hash when the lock has one, and that it still looks like a package.
func VerifyLockCacheIntegrity(lock lockfile.PackLock) error {
	for _, pkg := range lock.Packages {
		if err := VerifyPackage(pkg); err != nil {
			return err
		}
	}
	return VerifyLockCacheLayout(lock)
}

// VerifyLockCacheLayout checks only that every cache entry still looks like
// a package. It reads no file contents and proves nothing about them.
func VerifyLockCacheLayout(lock lockfile.PackLock) error {
	for _, pkg := range lock.Packages {
		if pkg.CacheKey == "" {
			continue
		}
		out, err := EntryDir(pkg.CacheKey)
		if err != nil {
			return err
		}
		if pkg.Kind == lockfile.PackagePlugin {
			if err := NormalizePluginLayout(out); err != nil {
				return err
			}
			if !HasPluginManifest(out) {
				return fmt.Errorf("plugin cache not ready for %s", pkg.CacheKey)
			}
		} else if !IsPackageRoot(out) {
			return fmt.Errorf("skill cache not ready for %s", pkg.CacheKey)
		}
	}
	return nil
}

func emptyOrMissingDir(path string) bool {
	entries, err := os.ReadDir(path)
	return os.IsNotExist(err) || err == nil && len(entries) == 0
}

func cacheReady(pkg lockfile.Package, out string) (bool, error) {
	if pkg.Kind == lockfile.PackagePlugin {
		if err := NormalizePluginLayout(out); err != nil {
			return false, err
		}
		return HasPluginManifest(out), nil
	}
	return regularFile(filepath.Join(out, "SKILL.md")) || HasPluginManifest(out), nil
}

func isLocalPackage(pkg lockfile.Package) bool {
	return pkg.Owner == "path" || pkg.Owner == "local" || strings.HasPrefix(pkg.URL, "file:")
}

func localSourceDir(rawURL string) (string, error) {
	if shorthand, found := strings.CutPrefix(rawURL, "agentpack-local:"); found {
		return paths.LocalMirrorPathFromShorthand(shorthand)
	}
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Scheme != "file" {
		return "", nil
	}
	path, err := url.PathUnescape(parsed.Path)
	if err != nil {
		return "", fmt.Errorf("decode file URL %q: %w", rawURL, err)
	}
	if parsed.Host != "" && parsed.Host != "localhost" {
		path = "//" + parsed.Host + path
	}
	if runtime.GOOS == "windows" && len(path) >= 3 && path[0] == '/' && path[2] == ':' {
		path = path[1:]
	}
	return filepath.FromSlash(path), nil
}
