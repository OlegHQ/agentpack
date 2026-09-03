package hooks

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/OlegHQ/agentpack/internal/harness"
	"github.com/OlegHQ/agentpack/internal/mode"
)

func StageOriginPackages(bundle Bundle, target harness.Target, targetRoot string, effective mode.Effective) (map[string]string, error) {
	roots := make(map[string]string)
	seen := make(map[string]bool)
	for _, hook := range bundle.Hooks {
		origin := hook.Origin
		if origin.Layer == SeededNative || seen[origin.SourceID()] {
			continue
		}
		seen[origin.SourceID()] = true
		destination := filepath.Join(HookAssetRoot(target, targetRoot), origin.PackageKey, "package")
		if err := copyFilteredTree(origin.SourceRoot, destination, origin, effective); err != nil {
			return nil, err
		}
		roots[origin.PackageKey] = destination
	}
	return roots, nil
}

func copyFilteredTree(source, destination string, origin Origin, effective mode.Effective) error {
	info, err := os.Stat(source)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return nil
	}
	return filepath.WalkDir(source, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == source {
			return nil
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		runtimePath := filepath.ToSlash(relative)
		allowed := true
		switch origin.Layer {
		case PackPlugin, BareSkill:
			allowed, err = effective.AllowsPackagePath(origin.Module, runtimePath)
		case DotAgents:
			allowed, err = effective.AllowsDotAgentsPath(runtimePath)
		}
		if err != nil {
			return err
		}
		if !allowed {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		output := filepath.Join(destination, relative)
		if entry.IsDir() {
			return os.MkdirAll(output, 0o755)
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		return copyRegularFile(path, output)
	})
}

func copyRegularFile(source, destination string) error {
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return err
	}
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(output, input)
	closeErr := output.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	return nil
}

func RequireStagedPackage(staged map[string]string, origin Origin) (string, error) {
	root, found := staged[origin.PackageKey]
	if !found {
		return "", fmt.Errorf("missing staged hook package root for %s (%s)", origin.Module, origin.PackageKey)
	}
	return root, nil
}
