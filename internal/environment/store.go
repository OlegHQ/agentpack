package environment

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/OlegHQ/agentpack/internal/lockfile"
	"github.com/OlegHQ/agentpack/internal/manifest"
	"github.com/OlegHQ/agentpack/internal/paths"
	"github.com/OlegHQ/agentpack/internal/slug"
	"github.com/gofrs/flock"
)

// Root returns $AGENTPACK_HOME/environments.
func Root() (string, error) {
	return paths.EnvironmentsRoot()
}

// Dir returns the directory for a named environment under AGENTPACK_HOME.
func Dir(name string) (string, error) {
	root, err := Root()
	if err != nil {
		return "", err
	}
	cleaned := slug.DashedLower(strings.TrimSpace(name))
	if cleaned == "" {
		return "", fmt.Errorf("environment name is required")
	}
	return filepath.Join(root, cleaned), nil
}

// Init publishes a complete local definition; copied path dependencies retain their original source.
func Init(name, destination, fromProject string) (string, error) {
	if _, err := paths.EnsureUserAgentpackLayout(); err != nil {
		return "", err
	}
	var dir string
	var err error
	if destination != "" {
		dir, err = filepath.Abs(destination)
	} else {
		dir, err = Dir(name)
	}
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return "", err
	}
	guard := flock.New(dir + ".init.lock")
	if err := guard.Lock(); err != nil {
		return "", err
	}
	defer guard.Unlock()
	existing := false
	if info, err := os.Lstat(dir); err == nil {
		if !info.IsDir() {
			return "", fmt.Errorf("environment destination is not a directory: %s", dir)
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			return "", err
		}
		if len(entries) != 0 {
			return "", fmt.Errorf("environment destination must be empty: %s", dir)
		}
		existing = true
	} else if !os.IsNotExist(err) {
		return "", err
	}
	temporary, err := os.MkdirTemp(filepath.Dir(dir), ".agentpack-init-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(temporary)
	files := []string{paths.ManifestName, paths.LockfileName}
	if fromProject != "" {
		source, err := filepath.Abs(fromProject)
		if err != nil {
			return "", err
		}
		for _, filename := range append(append([]string(nil), files...), "contract.json") {
			sourcePath := filepath.Join(source, filename)
			info, err := os.Lstat(sourcePath)
			if os.IsNotExist(err) && filename == "contract.json" {
				continue
			}
			if err != nil {
				return "", fmt.Errorf("copy %s: %w", filename, err)
			}
			if !info.Mode().IsRegular() {
				return "", fmt.Errorf("copy %s requires a regular file", filename)
			}
			data, err := os.ReadFile(sourcePath)
			if err != nil {
				return "", fmt.Errorf("copy %s: %w", filename, err)
			}
			if err := os.WriteFile(filepath.Join(temporary, filename), data, 0o600); err != nil {
				return "", err
			}
			if filename == "contract.json" {
				files = append(files, filename)
			}
		}
		copied, err := manifest.Load(temporary)
		if err != nil {
			return "", err
		}
		for module, dependency := range copied.Dependencies {
			if local, ok := dependency.PathValue(); ok && !filepath.IsAbs(local) {
				if err := manifest.AppendPathDependency(temporary, module, filepath.Clean(filepath.Join(source, local))); err != nil {
					return "", err
				}
			}
		}
	} else {
		label := name
		if label == "" {
			label = filepath.Base(dir)
		}
		if err := manifest.WriteStub(temporary, label, "0.0.1"); err != nil {
			return "", err
		}
		if err := lockfile.Init(temporary, label, "0.0.1"); err != nil {
			return "", err
		}
	}
	if _, err := manifest.Load(temporary); err != nil {
		return "", err
	}
	if _, err := lockfile.Load(temporary); err != nil {
		return "", err
	}
	if regularFile(filepath.Join(temporary, "contract.json")) {
		if _, err := LoadContract(filepath.Join(temporary, "contract.json")); err != nil {
			return "", err
		}
	}
	if !existing {
		if _, err := os.Lstat(dir); !os.IsNotExist(err) {
			return "", fmt.Errorf("environment destination appeared during initialization: %s", dir)
		}
		if err := os.Rename(temporary, dir); err != nil {
			return "", err
		}
		return dir, nil
	}
	// Preserve an existing empty user directory. Exclusive links never replace files;
	// returned failures remove only links this operation successfully published.
	var published []string
	for _, filename := range files {
		path := filepath.Join(dir, filename)
		if err := os.Link(filepath.Join(temporary, filename), path); err != nil {
			var cleanupErrors []error
			for _, created := range published {
				if removeErr := os.Remove(created); removeErr != nil {
					cleanupErrors = append(cleanupErrors, fmt.Errorf("remove partial file %s: %w", created, removeErr))
				}
			}
			return "", errors.Join(fmt.Errorf("publish environment: %w", err), errors.Join(cleanupErrors...))
		}
		published = append(published, path)
	}
	return dir, nil
}

// List returns named environments under AGENTPACK_HOME/environments.
func List() ([]string, error) {
	root, err := Root()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var names []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if regularFile(filepath.Join(root, entry.Name(), paths.ManifestName)) || regularFile(filepath.Join(root, entry.Name(), paths.LockfileName)) {
			names = append(names, entry.Name())
		}
	}
	return names, nil
}

// ResolveRef turns a name or filesystem path into an absolute definition root.
func ResolveRef(ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", fmt.Errorf("environment name or path is required")
	}
	if strings.Contains(ref, string(filepath.Separator)) || strings.HasPrefix(ref, ".") || filepath.IsAbs(ref) {
		path, err := filepath.Abs(ref)
		if err != nil {
			return "", err
		}
		if !regularFile(paths.ManifestPath(path)) && !regularFile(paths.LockPath(path)) {
			return "", fmt.Errorf("%w from %s", paths.ErrProjectNotFound, path)
		}
		return path, nil
	}
	dir, err := Dir(ref)
	if err != nil {
		return "", err
	}
	if !regularFile(paths.ManifestPath(dir)) && !regularFile(paths.LockPath(dir)) {
		return "", fmt.Errorf("unknown environment %q (expected %s)", ref, dir)
	}
	return dir, nil
}

func regularFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}
