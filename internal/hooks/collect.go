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
		bundle.Skipped = append(bundle.Skipped, parsed.Skipped...)
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
			bundle.Skipped = append(bundle.Skipped, parsed.Skipped...)
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
		root, err := cache.EntryDir(pkg.CacheKey)
		if err != nil {
			return err
		}
		eventOffset := 0
		for _, source := range packageSources(root, pkg, layer, bundle) {
			allowed, err := effective.AllowsPackagePath(pkg.Module, source.gate)
			if err != nil {
				return err
			}
			if !allowed {
				continue
			}
			origin := Origin{Layer: layer, Module: pkg.Module, CacheKey: pkg.CacheKey, SourceRelative: source.relative, SourceRoot: root, SourceFile: source.file, PackageKey: packageKey(pkg.CacheKey, pkg.Module, layer)}
			var parsed Bundle
			if source.dialect == CursorDialect {
				parsed, err = ParseCursor(source.file, source.value, origin)
			} else {
				parsed, err = ParseNested(source.file, source.value, origin)
			}
			if err != nil {
				// One unreadable hook file does not take the package down.
				bundle.skip(origin, source.file, "skipping this hook file: %v", err)
				continue
			}
			highest := eventOffset
			for index := range parsed.Hooks {
				hook := &parsed.Hooks[index]
				if hook.Dialect == "" {
					hook.Dialect = source.dialect
				}
				// Spec files are named by event index within a package, so
				// each further hook file of the package continues the count.
				hook.Origin.EventIndex += eventOffset
				highest = max(highest, hook.Origin.EventIndex+1)
			}
			eventOffset = highest
			bundle.Hooks = append(bundle.Hooks, parsed.Hooks...)
			bundle.Skipped = append(bundle.Skipped, parsed.Skipped...)
		}
	}
	return nil
}

// hookSource is one place a package defines hooks: a file, or an object
// written inline in a plugin manifest.
type hookSource struct {
	dialect  Dialect
	relative string
	gate     string
	file     string
	value    any
}

// packageSources lists a package's hook definitions as the plugin formats
// give them: hooks/hooks.json, read as Cursor's format when its events are
// Cursor's and as Claude Code's otherwise, then what each plugin.json declares
// under hooks, as a path, inline, or an array of them. A file two manifests
// share is read once, for the first of them.
func packageSources(root string, pkg lockfile.Package, layer Layer, bundle *Bundle) []hookSource {
	var sources []hookSource
	read := make(map[string]bool)
	origin := Origin{Layer: layer, Module: pkg.Module}
	file := func(path, relative string, dialect Dialect) {
		resolved, err := filepath.EvalSymlinks(path)
		if err != nil || read[resolved] || !regularFile(resolved) {
			return
		}
		read[resolved] = true
		data, err := os.ReadFile(resolved)
		var value any
		if err == nil {
			err = json.Unmarshal(data, &value)
		}
		if err != nil {
			bundle.skip(origin, resolved, "skipping this hook file: %v", err)
			return
		}
		if dialect == "" {
			dialect = ClaudeDialect
			if LooksLikeCursor(value) {
				dialect = CursorDialect
			}
		}
		sources = append(sources, hookSource{dialect: dialect, relative: relative, gate: relative, file: resolved, value: value})
	}
	file(filepath.Join(root, "hooks", "hooks.json"), "hooks/hooks.json", "")
	dialects := map[string]Dialect{"claude": ClaudeDialect, "cursor": CursorDialect, "codex": CodexDialect}
	for _, manifest := range cache.ReadPluginManifests(root) {
		dialect := dialects[manifest.Vendor]
		paths, inline := manifest.Declared("hooks")
		for _, declared := range paths {
			resolved, err := cache.ResolveInside(root, declared)
			if err != nil {
				bundle.skip(origin, manifest.Relative, "skipping its hooks: %v", err)
				continue
			}
			file(resolved, strings.TrimPrefix(filepath.ToSlash(declared), "./"), dialect)
		}
		for index, raw := range inline {
			var events any
			if err := json.Unmarshal(raw, &events); err != nil {
				continue
			}
			// An inline value is the event map itself, without the wrapper a file has.
			if object, ok := asObject(events); ok {
				if _, wrapped := asObject(object["hooks"]); !wrapped {
					events = map[string]any{"hooks": object}
				}
			}
			sources = append(sources, hookSource{dialect: dialect, relative: fmt.Sprintf("%s#hooks[%d]", manifest.Relative, index), gate: manifest.Relative, file: filepath.Join(root, filepath.FromSlash(manifest.Relative)), value: events})
		}
	}
	return sources
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
