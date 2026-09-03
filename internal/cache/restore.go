package cache

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/OlegHQ/agentpack/internal/lockfile"
	"github.com/OlegHQ/agentpack/internal/paths"
)

type RemoteRestoreFunc func(pkg lockfile.Package, destination string) error

func EnsureLockCached(pkg lockfile.Package, restoreRemote RemoteRestoreFunc) (bool, error) {
	out, err := EntryDir(pkg.CacheKey)
	if err != nil {
		return false, err
	}
	ready, err := cacheReady(pkg, out)
	if err != nil || ready {
		return ready, err
	}
	local, err := localSourceDir(pkg.URL)
	if err != nil {
		return false, err
	}
	if local != "" {
		if info, statErr := os.Stat(local); statErr == nil && info.IsDir() {
			if err := prepareCacheOutput(out); err != nil {
				return false, err
			}
			if err := copyMergeTree(local, out); err != nil {
				return false, err
			}
			if err := NormalizePluginLayout(out); err != nil {
				return false, err
			}
			return cacheReady(pkg, out)
		}
	}
	if isLocalPackage(pkg) {
		return false, nil
	}
	if restoreRemote == nil {
		return false, fmt.Errorf("cache entry %s is missing and no remote restorer is configured", pkg.CacheKey)
	}
	if err := restoreRemote(pkg, out); err != nil {
		return false, err
	}
	if err := NormalizePluginLayout(out); err != nil {
		return false, err
	}
	return cacheReady(pkg, out)
}

func VerifyLockCacheIntegrity(lock lockfile.PackLock) error {
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

func cacheReady(pkg lockfile.Package, out string) (bool, error) {
	if pkg.Kind == lockfile.PackagePlugin {
		if err := NormalizePluginLayout(out); err != nil {
			return false, err
		}
		return HasPluginManifest(out), nil
	}
	return regularFile(filepath.Join(out, "SKILL.md")) || HasPluginManifest(out), nil
}

func prepareCacheOutput(out string) error {
	cacheRoot, err := paths.CacheDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(cacheRoot, 0o755); err != nil {
		return fmt.Errorf("create cache directory %s: %w", cacheRoot, err)
	}
	if err := os.RemoveAll(out); err != nil {
		return fmt.Errorf("remove cache entry %s: %w", out, err)
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		return fmt.Errorf("create cache entry %s: %w", out, err)
	}
	return nil
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
	return filepath.FromSlash(path), nil
}
