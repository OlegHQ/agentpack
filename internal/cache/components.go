package cache

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/tailscale/hujson"
)

// PluginManifest is one vendor's plugin.json as published in a package.
type PluginManifest struct {
	// Vendor is "codex", "cursor", or "claude".
	Vendor string
	// Relative is the manifest path inside the package, slash-separated.
	Relative string
	Fields   map[string]json.RawMessage
}

// ReadPluginManifests returns the plugin manifests of a package in merge
// order: Codex, Cursor, then Claude, so that a later one wins where two
// vendors declare the same thing. An unreadable manifest is left out.
func ReadPluginManifests(root string) []PluginManifest {
	var manifests []PluginManifest
	for _, candidate := range []PluginManifest{
		{Vendor: "codex", Relative: ".codex-plugin/plugin.json"},
		{Vendor: "cursor", Relative: ".cursor-plugin/plugin.json"},
		{Vendor: "claude", Relative: ".claude-plugin/plugin.json"},
	} {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(candidate.Relative)))
		if err != nil {
			continue
		}
		standard, err := hujson.Standardize(append(data, '\n'))
		if err != nil || json.Unmarshal(standard, &candidate.Fields) != nil {
			continue
		}
		manifests = append(manifests, candidate)
	}
	return manifests
}

// Declared splits a manifest component field into the paths it names and the
// values it defines inline. Vendors accept a path, an inline value, or an
// array mixing the two.
func (manifest PluginManifest) Declared(field string) (paths []string, inline []json.RawMessage) {
	raw, found := manifest.Fields[field]
	if !found {
		return nil, nil
	}
	var visit func(json.RawMessage, bool)
	visit = func(value json.RawMessage, top bool) {
		trimmed := strings.TrimSpace(string(value))
		switch {
		case strings.HasPrefix(trimmed, `"`):
			var path string
			if json.Unmarshal(value, &path) == nil && strings.TrimSpace(path) != "" {
				paths = append(paths, path)
			}
		case strings.HasPrefix(trimmed, "[") && top:
			var items []json.RawMessage
			if json.Unmarshal(value, &items) == nil {
				for _, item := range items {
					visit(item, false)
				}
			}
		case strings.HasPrefix(trimmed, "{"), strings.HasPrefix(trimmed, "["):
			inline = append(inline, value)
		}
	}
	visit(raw, true)
	return paths, inline
}

// ResolveInside resolves a path a package declares about itself. Symbolic
// links are followed, and the result must stay inside root: a declaration can
// name any part of the package and nothing else on the machine.
func ResolveInside(root, declared string) (string, error) {
	normalized := strings.ReplaceAll(declared, "\\", "/")
	if strings.HasPrefix(normalized, "/") || filepath.IsAbs(declared) || filepath.VolumeName(declared) != "" {
		return "", fmt.Errorf("path %q is absolute; it must be relative to the package", declared)
	}
	for _, part := range strings.Split(normalized, "/") {
		if part == ".." {
			return "", fmt.Errorf("path %q leaves the package", declared)
		}
	}
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(filepath.Join(canonicalRoot, filepath.FromSlash(normalized)))
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("path %q does not exist in the package", declared)
		}
		return "", fmt.Errorf("path %q cannot be resolved: %w", declared, err)
	}
	if !Inside(canonicalRoot, resolved) {
		return "", fmt.Errorf("path %q resolves outside the package", declared)
	}
	return resolved, nil
}

// Inside reports whether path is root or lies below it. Both must already be
// free of symbolic links.
func Inside(root, path string) bool {
	relative, err := filepath.Rel(root, path)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}
