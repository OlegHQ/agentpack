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
	files, err := PackageFiles(sourceRoot, module, bareSkillName == "")
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
	names := NewConvertedNames(skillSources)
	// An agent kept in a subdirectory is staged at the top of agents/ where the
	// target reads only that level; a second agent of the same file name keeps
	// its subdirectory rather than replace the first.
	flattened := make(map[string]map[string]string)
	for _, artifact := range parsed {
		for _, root := range roots {
			staged := names.For(artifact, root.Target)
			if staged.StorageName != artifact.StorageName {
				log.Printf("warning: %s: %s stages %ss as skills and the skill name %s is taken; %s %s is staged as skill %s", module, root.Target, artifact.Kind.Word(), artifact.StorageName, artifact.Kind.Word(), artifact.StorageName, staged.StorageName)
			}
			rendered := staged.Render(root.Target)
			if nested := staged.NestedPath(); staged.Kind == artifacts.Agent && rendered.RelativePath != nested {
				if flattened[root.Path] == nil {
					flattened[root.Path] = make(map[string]string)
				}
				if first, taken := flattened[root.Path][rendered.RelativePath]; taken {
					log.Printf("warning: %s: agents %s and %s have the same file name; %s reads agents only at the top of agents/, so it finds the first", module, first, nested, harness.DisplayName(root.Target))
					rendered.RelativePath = nested
				} else {
					flattened[root.Path][rendered.RelativePath] = nested
				}
			}
			for _, warning := range rendered.Warnings {
				log.Printf("warning: %s: %s", module, warning)
			}
			if err := writeFile(filepath.Join(root.Path, filepath.FromSlash(rendered.RelativePath)), []byte(rendered.Contents)); err != nil {
				return err
			}
			// Codex has custom agents of its own; a subagent is staged as one
			// as well as the skill that older Codex versions find it under.
			if native, ok := artifact.CodexAgent(); ok && root.Target == harness.Codex {
				if err := writeFile(filepath.Join(root.Path, filepath.FromSlash(native.RelativePath)), []byte(native.Contents)); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// ConvertedNames names the skills that a package's commands, agents and rules
// become on a target without those kinds. An item keeps its own name when it
// is free; otherwise it gets the name followed by its kind (review-command),
// then by a number. Items must be offered in staging order.
type ConvertedNames struct {
	skills map[string]string
	taken  map[harness.Target]map[string]bool
}

// NewConvertedNames takes the package's own skills, by storage name.
func NewConvertedNames(skills map[string]string) *ConvertedNames {
	return &ConvertedNames{skills: skills, taken: make(map[harness.Target]map[string]bool)}
}

// For returns the artifact as it is staged for target.
func (names *ConvertedNames) For(artifact *artifacts.Markdown, target harness.Target) artifacts.Markdown {
	if artifact.Kind == artifacts.Skill || artifacts.RenderedKind(target, artifact.Kind) != artifacts.Skill {
		return *artifact
	}
	if names.taken[target] == nil {
		names.taken[target] = make(map[string]bool)
	}
	free := func(name string) bool {
		_, skill := names.skills[name]
		return !skill && !names.taken[target][name]
	}
	name := artifact.StorageName
	if !free(name) {
		name = artifact.StorageName + "-" + artifact.Kind.Word()
		for suffix := 2; !free(name); suffix++ {
			name = fmt.Sprintf("%s-%s-%d", artifact.StorageName, artifact.Kind.Word(), suffix)
		}
	}
	names.taken[target][name] = true
	if name == artifact.StorageName {
		return *artifact
	}
	return artifact.Renamed(name)
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
