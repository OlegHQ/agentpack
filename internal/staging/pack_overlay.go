package staging

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/OlegHQ/agentpack/internal/artifacts"
	"github.com/OlegHQ/agentpack/internal/cache"
	"github.com/OlegHQ/agentpack/internal/harness"
	"github.com/OlegHQ/agentpack/internal/lockfile"
	"github.com/OlegHQ/agentpack/internal/mode"
)

type HarnessRoot struct {
	Target harness.Target
	Path   string
}

func StagePackOverlay(lock lockfile.PackLock, roots []HarnessRoot, effective mode.Effective) error {
	plugins := lock.Plugins()
	sort.Slice(plugins, func(i, j int) bool { return plugins[i].CacheKey < plugins[j].CacheKey })
	for _, plugin := range plugins {
		if plugin.CacheKey == "" || disabledPlugin(lock, plugin.CacheKey) {
			continue
		}
		cacheRoot, err := cache.EntryDir(plugin.CacheKey)
		if err != nil {
			continue
		}
		if !cache.HasPluginManifest(cacheRoot) {
			log.Printf("warning: skip plugin staging: cache missing manifest at %s", cacheRoot)
			continue
		}
		if err := copyRawSupport(cacheRoot, roots, plugin.Module, effective); err != nil {
			return err
		}
		if err := stageSourceTree(cacheRoot, roots, "", plugin.Module, effective); err != nil {
			return err
		}
	}
	skills := lock.Skills()
	sort.Slice(skills, func(i, j int) bool { return skills[i].CacheKey < skills[j].CacheKey })
	for _, skill := range skills {
		if skill.CacheKey == "" || disabledPlugin(lock, skill.CacheKey) || SkillIsShadowed(skill, plugins) {
			continue
		}
		cacheRoot, err := cache.EntryDir(skill.CacheKey)
		if err != nil {
			continue
		}
		if info, err := os.Stat(filepath.Join(cacheRoot, "SKILL.md")); err != nil || !info.Mode().IsRegular() {
			log.Printf("warning: skip skill staging: SKILL.md missing at %s", cacheRoot)
			continue
		}
		if err := stageSourceTree(cacheRoot, roots, SkillFolderName(skill), skill.Module, effective); err != nil {
			return err
		}
	}
	return nil
}

func stageSourceTree(sourceRoot string, roots []HarnessRoot, bareSkillName, module string, effective mode.Effective) error {
	return filepath.WalkDir(sourceRoot, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		relative, err := filepath.Rel(sourceRoot, path)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		allowed, err := effective.AllowsPackagePath(module, relative)
		if err != nil || !allowed {
			return err
		}
		if destination, ok := artifacts.StagedSkillSupportPath(relative, bareSkillName); ok {
			for _, root := range roots {
				if err := copyFile(path, filepath.Join(root.Path, filepath.FromSlash(destination))); err != nil {
					return err
				}
			}
			return nil
		}
		extension := strings.ToLower(filepath.Ext(path))
		if extension != ".md" && extension != ".mdc" {
			return nil
		}
		contents, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		artifact, err := artifacts.Parse(relative, string(contents), bareSkillName)
		if err != nil {
			log.Printf("warning: skipping markdown artifact %s: %v", relative, err)
			return nil
		}
		if artifact == nil {
			return nil
		}
		for _, root := range roots {
			rendered := artifact.Render(root.Target)
			if err := writeFile(filepath.Join(root.Path, filepath.FromSlash(rendered.RelativePath)), []byte(rendered.Contents)); err != nil {
				return err
			}
		}
		return nil
	})
}

func copyRawSupport(sourceRoot string, roots []HarnessRoot, module string, effective mode.Effective) error {
	destinations := make(map[string][]string)
	for _, root := range roots {
		for _, subdirectory := range root.Target.RawPluginSubdirs() {
			destinations[subdirectory] = append(destinations[subdirectory], root.Path)
		}
	}
	subdirectories := make([]string, 0, len(destinations))
	for subdirectory := range destinations {
		subdirectories = append(subdirectories, subdirectory)
	}
	sort.Strings(subdirectories)
	for _, subdirectory := range subdirectories {
		source := filepath.Join(sourceRoot, subdirectory)
		if info, err := os.Stat(source); err != nil || !info.IsDir() {
			continue
		}
		err := filepath.WalkDir(source, func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() {
				return nil
			}
			relative, err := filepath.Rel(source, path)
			if err != nil {
				return err
			}
			fullRelative := filepath.ToSlash(filepath.Join(subdirectory, relative))
			allowed, err := effective.AllowsPackagePath(module, fullRelative)
			if err != nil || !allowed {
				return err
			}
			for _, destinationRoot := range destinations[subdirectory] {
				if err := copyFile(path, filepath.Join(destinationRoot, filepath.FromSlash(fullRelative))); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func SkillFolderName(pkg lockfile.Package) string {
	if pkg.Path == "" {
		return pkg.Repo
	}
	name := filepath.Base(filepath.FromSlash(strings.TrimRight(pkg.Path, "/")))
	if name == "." || name == string(filepath.Separator) {
		return pkg.Repo
	}
	return name
}

func SkillIsShadowed(skill lockfile.Package, plugins []lockfile.Package) bool {
	for _, plugin := range plugins {
		if plugin.Kind != lockfile.PackagePlugin || plugin.CacheKey == "" || plugin.Commit == "" || plugin.Owner == "" || plugin.Repo == "" {
			continue
		}
		if skill.Owner != plugin.Owner || skill.Repo != plugin.Repo || skill.Commit != plugin.Commit {
			continue
		}
		pluginPath, skillPath := strings.TrimRight(plugin.Path, "/"), strings.TrimRight(skill.Path, "/")
		if pluginPath == "" || skillPath == pluginPath || strings.HasPrefix(skillPath, pluginPath+"/") {
			return true
		}
	}
	return false
}

func copyFile(source, destination string) error {
	data, err := os.ReadFile(source)
	if err != nil {
		return fmt.Errorf("read %s: %w", source, err)
	}
	return writeFile(destination, data)
}

func writeFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create parent for %s: %w", path, err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}
