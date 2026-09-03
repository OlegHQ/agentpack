package cache

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/OlegHQ/agentpack/internal/manifest"
	"github.com/OlegHQ/agentpack/internal/paths"
)

func NormalizePluginLayout(root string) error {
	if !HasPluginManifest(root) && !regularFile(filepath.Join(root, "SKILL.md")) && !regularFile(filepath.Join(root, paths.ManifestName)) {
		if _, err := materializeSinglePluginMarketplace(root); err != nil {
			return err
		}
	}
	manifests := []struct {
		directory string
		path      string
	}{
		{directory: ".claude-plugin", path: ClaudePluginManifestPath(root)},
		{directory: ".cursor-plugin", path: CursorPluginManifestPath(root)},
		{directory: ".codex-plugin", path: CodexPluginManifestPath(root)},
	}
	if !HasPluginManifest(root) {
		if err := synthesizeManifestsFromAgentpack(root); err != nil {
			return err
		}
	}
	var source string
	for _, candidate := range manifests {
		if regularFile(candidate.path) {
			source = candidate.path
			break
		}
	}
	if source == "" {
		return nil
	}
	for _, target := range manifests {
		if !regularFile(target.path) {
			if err := synthesizePluginManifest(root, source, target.directory); err != nil {
				return err
			}
		}
	}
	legacyMCP := filepath.Join(root, ".mcp.json")
	canonicalMCP := filepath.Join(root, "mcp.json")
	if regularFile(legacyMCP) && !regularFile(canonicalMCP) {
		if err := copyFile(legacyMCP, canonicalMCP); err != nil {
			return err
		}
	}
	return nil
}

func synthesizePluginManifest(root, sourceManifest, targetDirectory string) error {
	var source map[string]any
	if err := readJSON(sourceManifest, &source); err != nil {
		return err
	}
	name, _ := source["name"].(string)
	if name == "" {
		name = "plugin"
	}
	version, _ := source["version"].(string)
	if version == "" {
		version = "1.0.0"
	}
	description, _ := source["description"].(string)
	if description == "" {
		description, _ = source["displayName"].(string)
	}
	stub := map[string]any{"name": name, "version": version, "description": description}
	if targetDirectory == ".cursor-plugin" {
		displayName, _ := source["displayName"].(string)
		if displayName == "" {
			displayName = name
		}
		stub["displayName"] = displayName
	}
	return writeJSON(filepath.Join(root, targetDirectory, "plugin.json"), stub)
}

func synthesizeManifestsFromAgentpack(root string) error {
	projectManifest, err := manifest.Load(root)
	if err != nil || projectManifest == nil {
		return err
	}
	claude := map[string]any{"name": projectManifest.Name, "version": projectManifest.Version, "description": projectManifest.Description}
	if err := writeJSON(ClaudePluginManifestPath(root), claude); err != nil {
		return err
	}
	cursor := map[string]any{"name": projectManifest.Name, "displayName": projectManifest.Name, "version": projectManifest.Version, "description": projectManifest.Description}
	return writeJSON(CursorPluginManifestPath(root), cursor)
}

func writeJSON(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create JSON parent %s: %w", filepath.Dir(path), err)
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("encode JSON %s: %w", path, err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write JSON %s: %w", path, err)
	}
	return nil
}
