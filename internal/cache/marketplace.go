package cache

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

var pluginManifestPaths = []string{
	filepath.Join(".claude-plugin", "plugin.json"),
	filepath.Join(".cursor-plugin", "plugin.json"),
	filepath.Join(".codex-plugin", "plugin.json"),
}

type marketplaceSpec struct {
	relativePath string
	priority     uint8
}

var marketplaceSpecs = []marketplaceSpec{
	{relativePath: filepath.Join(".agents", "plugins", "marketplace.json"), priority: 0},
	{relativePath: filepath.Join(".cursor-plugin", "marketplace.json"), priority: 1},
	{relativePath: filepath.Join(".claude-plugin", "marketplace.json"), priority: 2},
}

type marketplaceDocument struct {
	Metadata struct {
		PluginRoot *string `json:"pluginRoot"`
	} `json:"metadata"`
	Plugins []marketplacePlugin `json:"plugins"`
}

type marketplacePlugin struct {
	Name   string          `json:"name"`
	Source json.RawMessage `json:"source"`
}

type localPluginSource struct {
	path     string
	priority uint8
}

func materializeSinglePluginMarketplace(root string) (bool, error) {
	if !hasMarketplaceManifest(root) {
		return false, nil
	}
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return false, fmt.Errorf("resolve marketplace root %s: %w", root, err)
	}
	pluginNames := make(map[string]struct{})
	localSources := make(map[string]uint8)
	for _, spec := range marketplaceSpecs {
		manifestPath := filepath.Join(root, spec.relativePath)
		if !regularFile(manifestPath) {
			continue
		}
		var document marketplaceDocument
		if err := readJSON(manifestPath, &document); err != nil {
			return false, fmt.Errorf("invalid marketplace manifest %s: %w", manifestPath, err)
		}
		namesInDocument := make(map[string]struct{}, len(document.Plugins))
		for _, plugin := range document.Plugins {
			name := strings.TrimSpace(plugin.Name)
			if name == "" {
				return false, fmt.Errorf("marketplace manifest %s contains a plugin with an empty name", manifestPath)
			}
			if _, duplicate := namesInDocument[name]; duplicate {
				return false, fmt.Errorf("marketplace manifest %s contains duplicate plugin name %q", manifestPath, name)
			}
			namesInDocument[name] = struct{}{}
			pluginNames[name] = struct{}{}
			relativeSource, local, err := localSourcePath(plugin.Source, document.Metadata.PluginRoot, manifestPath, name)
			if err != nil {
				return false, err
			}
			if !local {
				continue
			}
			source, err := canonicalLocalSource(canonicalRoot, relativeSource, manifestPath, name)
			if err != nil {
				return false, err
			}
			if err := validatePluginSource(source, name, manifestPath); err != nil {
				return false, err
			}
			if existing, exists := localSources[source]; !exists || spec.priority > existing {
				localSources[source] = spec.priority
			}
		}
	}
	if len(pluginNames) == 0 {
		return false, fmt.Errorf("marketplace at %s contains no plugins", root)
	}
	if len(pluginNames) != 1 {
		names := mapKeys(pluginNames)
		return false, fmt.Errorf("marketplace at %s contains multiple plugins (%s); add a specific plugin source directory instead", root, strings.Join(names, ", "))
	}
	if len(localSources) == 0 {
		return false, fmt.Errorf("marketplace plugin %q at %s has no local source; add its Git or package source directly", mapKeys(pluginNames)[0], root)
	}
	sources := make([]localPluginSource, 0, len(localSources))
	for path, priority := range localSources {
		sources = append(sources, localPluginSource{path: path, priority: priority})
	}
	sort.Slice(sources, func(i, j int) bool {
		if sources[i].priority != sources[j].priority {
			return sources[i].priority < sources[j].priority
		}
		return sources[i].path < sources[j].path
	})
	parent := filepath.Dir(root)
	normalized, err := os.MkdirTemp(parent, ".agentpack-marketplace-normalized-")
	if err != nil {
		return false, fmt.Errorf("create normalized marketplace directory: %w", err)
	}
	installed := false
	defer func() {
		if !installed {
			_ = os.RemoveAll(normalized)
		}
	}()
	for _, source := range sources {
		if err := copyMergeTree(source.path, normalized); err != nil {
			return false, err
		}
	}
	if !hasAnyRelativeFile(normalized, pluginManifestPaths) {
		return false, fmt.Errorf("marketplace at %s did not resolve to a plugin manifest", root)
	}
	if err := replaceDirectory(root, normalized); err != nil {
		return false, err
	}
	installed = true
	return true, nil
}

func localSourcePath(raw json.RawMessage, pluginRoot *string, manifestPath, pluginName string) (string, bool, error) {
	var sourceText string
	trimmed := strings.TrimSpace(string(raw))
	switch {
	case strings.HasPrefix(trimmed, "\""):
		if err := json.Unmarshal(raw, &sourceText); err != nil {
			return "", false, fmt.Errorf("marketplace source for %q in %s must be a string or object", pluginName, manifestPath)
		}
	case strings.HasPrefix(trimmed, "{"):
		var object struct {
			Source string  `json:"source"`
			Path   *string `json:"path"`
		}
		if objectErr := json.Unmarshal(raw, &object); objectErr != nil {
			return "", false, fmt.Errorf("marketplace source for %q in %s must be a string or object", pluginName, manifestPath)
		}
		if object.Source != "local" {
			return "", false, nil
		}
		if object.Path == nil {
			return "", false, fmt.Errorf("local marketplace source for %q in %s has no string path", pluginName, manifestPath)
		}
		sourceText = *object.Path
	default:
		return "", false, fmt.Errorf("marketplace source for %q in %s must be a string or object", pluginName, manifestPath)
	}
	if pluginRoot == nil && !strings.HasPrefix(sourceText, "./") {
		return "", false, fmt.Errorf("local marketplace source %q for %q in %s must start with ./", sourceText, pluginName, manifestPath)
	}
	parts := make([]string, 0, 2)
	if pluginRoot != nil {
		clean, err := safeRelativePath(*pluginRoot, manifestPath, pluginName)
		if err != nil {
			return "", false, err
		}
		parts = append(parts, clean)
	}
	clean, err := safeRelativePath(sourceText, manifestPath, pluginName)
	if err != nil {
		return "", false, err
	}
	parts = append(parts, clean)
	relative := filepath.Join(parts...)
	if relative == "" || relative == "." {
		return "", false, fmt.Errorf("local marketplace source for %q in %s resolves to the marketplace root", pluginName, manifestPath)
	}
	return relative, true, nil
}

func safeRelativePath(value, manifestPath, pluginName string) (string, error) {
	normalized := strings.ReplaceAll(value, "\\", "/")
	if strings.HasPrefix(normalized, "/") || filepath.IsAbs(value) || filepath.VolumeName(value) != "" {
		return "", unsafeMarketplacePath(value, manifestPath, pluginName)
	}
	var parts []string
	for _, part := range strings.Split(normalized, "/") {
		switch part {
		case "", ".":
			continue
		case "..":
			return "", unsafeMarketplacePath(value, manifestPath, pluginName)
		default:
			parts = append(parts, part)
		}
	}
	return filepath.Join(parts...), nil
}

func unsafeMarketplacePath(value, manifestPath, pluginName string) error {
	return fmt.Errorf("unsafe marketplace source %q for %q in %s: paths must stay inside the marketplace root", value, pluginName, manifestPath)
}

func canonicalLocalSource(canonicalRoot, relativeSource, manifestPath, pluginName string) (string, error) {
	candidate := filepath.Join(canonicalRoot, relativeSource)
	canonical, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		return "", fmt.Errorf("cannot resolve marketplace source for %q in %s at %s: %w", pluginName, manifestPath, candidate, err)
	}
	relative, err := filepath.Rel(canonicalRoot, canonical)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("marketplace source for %q in %s escapes the marketplace root: %s", pluginName, manifestPath, candidate)
	}
	info, err := os.Stat(canonical)
	if err != nil || !info.IsDir() {
		return "", fmt.Errorf("marketplace source for %q in %s is not a directory: %s", pluginName, manifestPath, candidate)
	}
	return canonical, nil
}

func validatePluginSource(source, expectedName, marketplace string) error {
	found := false
	for _, relative := range pluginManifestPaths {
		manifestPath := filepath.Join(source, relative)
		if !regularFile(manifestPath) {
			continue
		}
		found = true
		var manifest struct {
			Name *string `json:"name"`
		}
		if err := readJSON(manifestPath, &manifest); err != nil {
			return err
		}
		if manifest.Name == nil {
			return fmt.Errorf("plugin manifest %s referenced by %s has no string name", manifestPath, marketplace)
		}
		if *manifest.Name != expectedName {
			return fmt.Errorf("marketplace plugin name %q in %s does not match manifest name %q at %s", expectedName, marketplace, *manifest.Name, manifestPath)
		}
	}
	if !found {
		return fmt.Errorf("marketplace plugin %q in %s points to %s, which has no .claude-plugin, .cursor-plugin, or .codex-plugin manifest", expectedName, marketplace, source)
	}
	return nil
}

func replaceDirectory(current, replacement string) error {
	backupFile, err := os.CreateTemp(filepath.Dir(current), ".agentpack-marketplace-backup-")
	if err != nil {
		return fmt.Errorf("reserve marketplace backup: %w", err)
	}
	backup := backupFile.Name()
	if err := backupFile.Close(); err != nil {
		return fmt.Errorf("close marketplace backup reservation: %w", err)
	}
	if err := os.Remove(backup); err != nil {
		return fmt.Errorf("remove marketplace backup reservation: %w", err)
	}
	if err := os.Rename(current, backup); err != nil {
		return fmt.Errorf("move marketplace %s to backup: %w", current, err)
	}
	if err := os.Rename(replacement, current); err != nil {
		rollbackErr := os.Rename(backup, current)
		if rollbackErr != nil {
			return errors.Join(fmt.Errorf("install normalized marketplace: %w", err), fmt.Errorf("rollback marketplace: %w", rollbackErr))
		}
		return fmt.Errorf("install normalized marketplace: %w", err)
	}
	if err := os.RemoveAll(backup); err != nil {
		return fmt.Errorf("remove marketplace backup %s: %w", backup, err)
	}
	return nil
}

func readJSON(path string, destination any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read JSON %s: %w", path, err)
	}
	if err := json.Unmarshal(data, destination); err != nil {
		return fmt.Errorf("parse JSON %s: %w", path, err)
	}
	return nil
}

func hasAnyRelativeFile(root string, paths []string) bool {
	for _, relative := range paths {
		if regularFile(filepath.Join(root, relative)) {
			return true
		}
	}
	return false
}

func mapKeys(values map[string]struct{}) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
