package environment

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/OlegHQ/agentpack/internal/lockfile"
	"github.com/OlegHQ/agentpack/internal/manifest"
	"github.com/OlegHQ/agentpack/internal/paths"
	"github.com/OlegHQ/agentpack/internal/slug"
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

// Init creates an external definition directory with stub manifest and lock.
// When fromProject is set, copies that project's agentpack.toml and pack.lock.
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
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	if regularFile(paths.ManifestPath(dir)) {
		return "", fmt.Errorf("environment already exists: %s", paths.ManifestPath(dir))
	}
	if fromProject != "" {
		source, err := filepath.Abs(fromProject)
		if err != nil {
			return "", err
		}
		for _, name := range []string{paths.ManifestName, paths.LockfileName} {
			data, err := os.ReadFile(filepath.Join(source, name))
			if err != nil {
				return "", fmt.Errorf("copy %s: %w", name, err)
			}
			if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
				return "", err
			}
		}
		return dir, nil
	}
	label := name
	if label == "" {
		label = filepath.Base(dir)
	}
	if err := manifest.WriteStub(dir, label, "0.0.1"); err != nil {
		return "", err
	}
	if err := lockfile.Init(dir, label, "0.0.1"); err != nil {
		return "", err
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
