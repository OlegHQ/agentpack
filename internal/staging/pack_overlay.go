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
	files, err := packageFiles(sourceRoot, module, bareSkillName == "")
	if err != nil {
		return err
	}
	var parsed []*artifacts.Markdown
	skillSources := make(map[string]string)
	for _, file := range files {
		allowed, err := effective.AllowsPackagePath(module, file.Real)
		if err != nil {
			return err
		}
		if !allowed {
			continue
		}
		if destination, ok := artifacts.StagedSkillSupportPath(file.Virtual, bareSkillName); ok {
			for _, root := range roots {
				if err := copyFile(file.Path, filepath.Join(root.Path, filepath.FromSlash(destination))); err != nil {
					return err
				}
			}
			continue
		}
		extension := strings.ToLower(filepath.Ext(file.Virtual))
		if extension != ".md" && extension != ".mdc" {
			continue
		}
		contents, err := os.ReadFile(file.Path)
		if err != nil {
			return err
		}
		artifact, err := artifacts.Parse(file.Virtual, string(contents), bareSkillName)
		if err != nil {
			log.Printf("warning: %s: skipping markdown artifact %s: %v", module, file.Real, err)
			continue
		}
		if artifact == nil {
			continue
		}
		if artifact.Kind == artifacts.Skill {
			if first, duplicate := skillSources[artifact.StorageName]; duplicate && first != file.Real {
				log.Printf("warning: %s: skill %s is published twice (%s and %s); the second is staged", module, artifact.StorageName, first, file.Real)
			}
			skillSources[artifact.StorageName] = file.Real
		}
		parsed = append(parsed, artifact)
	}
	// A command, agent or rule that a target only has as a skill must not
	// take the place of a skill of the same name, or of another converted item.
	converted := make(map[harness.Target]map[string]bool)
	for _, artifact := range parsed {
		for _, root := range roots {
			staged := *artifact
			if artifact.Kind != artifacts.Skill && artifacts.RenderedKind(root.Target, artifact.Kind) == artifacts.Skill {
				if converted[root.Target] == nil {
					converted[root.Target] = make(map[string]bool)
				}
				name := convertedSkillName(artifact, skillSources, converted[root.Target])
				converted[root.Target][name] = true
				if name != artifact.StorageName {
					log.Printf("warning: %s: %s stages %ss as skills and the skill name %s is taken; %s %s is staged as skill %s", module, root.Target, artifact.Kind.Word(), artifact.StorageName, artifact.Kind.Word(), artifact.StorageName, name)
					staged = artifact.Renamed(name)
				}
			}
			rendered := staged.Render(root.Target)
			for _, warning := range rendered.Warnings {
				log.Printf("warning: %s: %s", module, warning)
			}
			if err := writeFile(filepath.Join(root.Path, filepath.FromSlash(rendered.RelativePath)), []byte(rendered.Contents)); err != nil {
				return err
			}
		}
	}
	return nil
}

// convertedSkillName names the skill a command, agent or rule becomes on a
// target without that kind: its own name when it is free, otherwise the name
// followed by its kind (review-command), then by a number.
func convertedSkillName(artifact *artifacts.Markdown, skills map[string]string, taken map[string]bool) string {
	free := func(name string) bool {
		_, skill := skills[name]
		return !skill && !taken[name]
	}
	if free(artifact.StorageName) {
		return artifact.StorageName
	}
	name := artifact.StorageName + "-" + artifact.Kind.Word()
	for suffix := 2; !free(name); suffix++ {
		name = fmt.Sprintf("%s-%s-%d", artifact.StorageName, artifact.Kind.Word(), suffix)
	}
	return name
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
			// Links are followed by stageSourceTree, inside the package only.
			if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
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
