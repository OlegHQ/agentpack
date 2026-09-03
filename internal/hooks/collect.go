package hooks

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/OlegHQ/agentpack/internal/cache"
	"github.com/OlegHQ/agentpack/internal/lockfile"
	"github.com/OlegHQ/agentpack/internal/mode"
	"github.com/OlegHQ/agentpack/internal/paths"
	"github.com/OlegHQ/agentpack/internal/slug"
)

func Collect(projectRoot string, lock lockfile.PackLock, seededCodexHooks string, effective mode.Effective) (Bundle, error) {
	var bundle Bundle
	if regularFile(seededCodexHooks) {
		root := filepath.Dir(seededCodexHooks)
		parsed, err := parseSource(SeededNative, "seeded-codex-user", "", root, seededCodexHooks, "hooks.json")
		if err != nil {
			return Bundle{}, err
		}
		bundle.Hooks = append(bundle.Hooks, parsed.Hooks...)
	}
	plugins := packagesByKind(lock, lockfile.PackagePlugin)
	if err := collectPackages(plugins, PackPlugin, effective, &bundle); err != nil {
		return Bundle{}, err
	}
	skills := packagesByKind(lock, lockfile.PackageSkill)
	for _, skill := range skills {
		if skillShadowed(skill, plugins) {
			continue
		}
		if err := collectPackages([]lockfile.Package{skill}, BareSkill, effective, &bundle); err != nil {
			return Bundle{}, err
		}
	}
	dotAgents := paths.ProjectDotAgentsDir(projectRoot)
	dotHooks := filepath.Join(dotAgents, "hooks", "hooks.json")
	if regularFile(dotHooks) {
		allowed, err := effective.AllowsDotAgentsPath("hooks/hooks.json")
		if err != nil {
			return Bundle{}, err
		}
		if allowed {
			parsed, err := parseSource(DotAgents, "dot-agents", "", dotAgents, dotHooks, "hooks/hooks.json")
			if err != nil {
				return Bundle{}, err
			}
			bundle.Hooks = append(bundle.Hooks, parsed.Hooks...)
		}
	}
	sort.SliceStable(bundle.Hooks, func(i, j int) bool {
		a, b := bundle.Hooks[i].Origin, bundle.Hooks[j].Origin
		if a.Layer.Rank() != b.Layer.Rank() {
			return a.Layer.Rank() < b.Layer.Rank()
		}
		if a.Module != b.Module {
			return a.Module < b.Module
		}
		if a.SourceRelative != b.SourceRelative {
			return a.SourceRelative < b.SourceRelative
		}
		if a.EventIndex != b.EventIndex {
			return a.EventIndex < b.EventIndex
		}
		if a.MatcherGroupIndex != b.MatcherGroupIndex {
			return a.MatcherGroupIndex < b.MatcherGroupIndex
		}
		return a.HookIndex < b.HookIndex
	})
	return bundle, nil
}

func collectPackages(packages []lockfile.Package, layer Layer, effective mode.Effective, bundle *Bundle) error {
	for _, pkg := range packages {
		if pkg.CacheKey == "" {
			continue
		}
		allowed, err := effective.AllowsPackagePath(pkg.Module, "hooks/hooks.json")
		if err != nil {
			return err
		}
		if !allowed {
			continue
		}
		root, err := cache.EntryDir(pkg.CacheKey)
		if err != nil {
			return err
		}
		source := filepath.Join(root, "hooks", "hooks.json")
		if !regularFile(source) {
			continue
		}
		parsed, err := parseSource(layer, pkg.Module, pkg.CacheKey, root, source, "hooks/hooks.json")
		if err != nil {
			return err
		}
		bundle.Hooks = append(bundle.Hooks, parsed.Hooks...)
	}
	return nil
}

func parseSource(layer Layer, module, cacheKey, sourceRoot, sourceFile, sourceRelative string) (Bundle, error) {
	data, err := os.ReadFile(sourceFile)
	if err != nil {
		return Bundle{}, err
	}
	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		return Bundle{}, fmt.Errorf("parse %s: %w", sourceFile, err)
	}
	origin := Origin{Layer: layer, Module: module, CacheKey: cacheKey, SourceRelative: sourceRelative, SourceRoot: sourceRoot, SourceFile: sourceFile, PackageKey: packageKey(cacheKey, module, layer)}
	if layer == SeededNative {
		return ParseCodex(sourceFile, value, origin)
	}
	return ParseNested(sourceFile, value, origin)
}

func packageKey(cacheKey, module string, layer Layer) string {
	if cacheKey != "" {
		return cacheKey
	}
	prefix := map[Layer]string{SeededNative: "seeded", PackPlugin: "plugin", BareSkill: "skill", DotAgents: "dot-agents"}[layer]
	return prefix + "-" + slug.Dashed(module)
}

func packagesByKind(lock lockfile.PackLock, kind lockfile.PackageKind) []lockfile.Package {
	var result []lockfile.Package
	for _, pkg := range lock.Packages {
		if pkg.Kind == kind {
			result = append(result, pkg)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Module < result[j].Module })
	return result
}

func skillShadowed(skill lockfile.Package, plugins []lockfile.Package) bool {
	for _, plugin := range plugins {
		if plugin.CacheKey == "" || plugin.Commit == "" || plugin.Owner == "" || plugin.Repo == "" || skill.Owner != plugin.Owner || skill.Repo != plugin.Repo || skill.Commit != plugin.Commit {
			continue
		}
		prefix, path := strings.TrimSuffix(plugin.Path, "/"), strings.TrimSuffix(skill.Path, "/")
		if prefix == "" || path == prefix || strings.HasPrefix(path, prefix+"/") {
			return true
		}
	}
	return false
}

func regularFile(path string) bool {
	if path == "" {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}
