package staging

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/OlegHQ/agentpack/internal/artifacts"
	"github.com/OlegHQ/agentpack/internal/cache"
	"github.com/OlegHQ/agentpack/internal/lockfile"
	"github.com/OlegHQ/agentpack/internal/mode"
	"github.com/OlegHQ/agentpack/internal/paths"
)

func CollectGuidance(projectRoot string, lock lockfile.PackLock, effective mode.Effective) (string, error) {
	var rules []*artifacts.Markdown
	plugins := lock.Plugins()
	sort.Slice(plugins, func(i, j int) bool { return plugins[i].CacheKey < plugins[j].CacheKey })
	for _, plugin := range plugins {
		if plugin.CacheKey == "" || disabledPlugin(lock, plugin.CacheKey) {
			continue
		}
		root, err := cache.EntryDir(plugin.CacheKey)
		if err != nil {
			continue
		}
		found, err := collectRules(root, func(relative string) (bool, error) { return effective.AllowsPackagePath(plugin.Module, relative) })
		if err != nil {
			return "", err
		}
		rules = append(rules, found...)
	}
	found, err := collectRules(paths.ProjectDotAgentsDir(projectRoot), effective.AllowsDotAgentsPath)
	if err != nil {
		return "", err
	}
	rules = append(rules, found...)
	if len(rules) == 0 {
		return "", nil
	}
	var output strings.Builder
	output.WriteString("# Agentpack-injected guidance\n\n_The following rules were declared with `alwaysApply: true` in one or more pinned plugins. They are injected into every supported harness for consistency._\n\n")
	seen := make(map[string]struct{})
	for _, rule := range rules {
		if _, exists := seen[rule.Name]; exists {
			continue
		}
		seen[rule.Name] = struct{}{}
		fmt.Fprintf(&output, "---\n\n## %s\n\n", rule.Name)
		if description := strings.TrimSpace(rule.Description); description != "" {
			fmt.Fprintf(&output, "_%s_\n\n", description)
		}
		output.WriteString(strings.TrimSpace(rule.Body))
		output.WriteString("\n\n")
	}
	return output.String(), nil
}

func collectRules(root string, allowed func(string) (bool, error)) ([]*artifacts.Markdown, error) {
	if info, err := os.Stat(root); err != nil || !info.IsDir() {
		return nil, nil
	}
	var rules []*artifacts.Markdown
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		extension := strings.ToLower(filepath.Ext(path))
		if extension != ".md" && extension != ".mdc" {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		enabled, err := allowed(relative)
		if err != nil || !enabled {
			return err
		}
		contents, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		artifact, err := artifacts.Parse(relative, string(contents), "")
		if err == nil && artifact != nil && artifact.Kind == artifacts.Rule && artifact.AlwaysApply {
			rules = append(rules, artifact)
		}
		return nil
	})
	return rules, err
}
