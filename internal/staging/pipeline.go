package staging

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/OlegHQ/agentpack/internal/cache"
	base "github.com/OlegHQ/agentpack/internal/harness"
	"github.com/OlegHQ/agentpack/internal/harness/registry"
	"github.com/OlegHQ/agentpack/internal/hooks"
	"github.com/OlegHQ/agentpack/internal/lockfile"
	"github.com/OlegHQ/agentpack/internal/manifest"
	"github.com/OlegHQ/agentpack/internal/mode"
	"github.com/OlegHQ/agentpack/internal/paths"
)

type Pipeline struct {
	ProjectRoot string
	Lock        lockfile.PackLock
	Manifest    *manifest.Manifest
	Mode        mode.Effective
	Target      *base.Target
}

func (pipeline Pipeline) Rebuild() ([]string, error) {
	ctx := pipeline.context()
	harnesses := registry.All()
	for _, candidate := range harnesses {
		if err := candidate.PreReset(ctx); err != nil {
			return nil, fmt.Errorf("pre-reset %s: %w", candidate.ID(), err)
		}
	}
	var reset []string
	for _, candidate := range harnesses {
		paths, err := candidate.ResetPaths(ctx)
		if err != nil {
			return nil, err
		}
		reset = append(reset, paths...)
	}
	sort.Strings(reset)
	reset = slices.Compact(reset)
	for _, path := range reset {
		if err := removeRebuildPath(path); err != nil {
			return nil, err
		}
	}
	for _, candidate := range harnesses {
		if err := candidate.Prepare(ctx); err != nil {
			return nil, fmt.Errorf("prepare %s: %w", candidate.ID(), err)
		}
	}
	roots := make([]HarnessRoot, 0, len(harnesses))
	for _, candidate := range harnesses {
		root, err := candidate.StagedRoot(ctx)
		if err != nil {
			return nil, err
		}
		roots = append(roots, HarnessRoot{candidate.ID(), root})
	}
	if err := StagePackOverlay(pipeline.Lock, roots, pipeline.Mode); err != nil {
		return nil, err
	}
	if err := pipeline.stageHooks(ctx, harnesses); err != nil {
		return nil, err
	}
	if err := StageDotAgents(pipeline.ProjectRoot, pipeline.Mode.Name(), pipeline.Mode); err != nil {
		return nil, err
	}
	merged, err := CollectMCP(pipeline.ProjectRoot, pipeline.Lock, pipeline.Manifest, &pipeline.Mode)
	if err != nil {
		return nil, err
	}
	if len(merged) != 0 {
		for _, candidate := range harnesses {
			if err := candidate.WriteMCP(merged, ctx); err != nil {
				return nil, fmt.Errorf("write MCP %s: %w", candidate.ID(), err)
			}
		}
	}
	guidance, err := CollectGuidance(pipeline.ProjectRoot, pipeline.Lock, pipeline.Mode)
	if err != nil {
		return nil, err
	}
	if guidance != "" {
		for _, candidate := range harnesses {
			if err := candidate.InjectGuidance(guidance, ctx); err != nil {
				return nil, fmt.Errorf("inject guidance %s: %w", candidate.ID(), err)
			}
		}
	}
	for _, candidate := range harnesses {
		if err := candidate.Finalize(merged, ctx); err != nil {
			return nil, fmt.Errorf("finalize %s: %w", candidate.ID(), err)
		}
	}
	if pipeline.Target != nil {
		candidate, err := registry.ByTarget(*pipeline.Target)
		if err != nil {
			return nil, err
		}
		if err := candidate.FinalizeWorkspaceOverlay(ctx); err != nil {
			return nil, err
		}
	}
	plugins, err := paths.StagingPluginsDirForMode(pipeline.ProjectRoot, pipeline.Mode.Name())
	if err != nil {
		return nil, err
	}
	return []string{filepath.Join(plugins, paths.StagedAgentpackBundleName)}, nil
}

func (pipeline Pipeline) Verify() error {
	ctx := pipeline.context()
	harnesses := registry.All()
	for _, candidate := range harnesses {
		if err := candidate.Verify(ctx); err != nil {
			return fmt.Errorf("verify %s: %w", candidate.ID(), err)
		}
	}
	plugins, err := paths.StagingPluginsDirForMode(pipeline.ProjectRoot, pipeline.Mode.Name())
	if err != nil {
		return err
	}
	bundle := filepath.Join(plugins, paths.StagedAgentpackBundleName)
	entries, err := os.ReadDir(plugins)
	if err != nil {
		return err
	}
	count := 0
	for _, entry := range entries {
		if entry.IsDir() {
			if _, err := os.Stat(filepath.Join(plugins, entry.Name(), ".claude-plugin", "plugin.json")); err == nil {
				count++
			}
		}
	}
	if count != 1 {
		return fmt.Errorf("expected exactly one merged plugin dir (agentpack-bundle), got %d", count)
	}
	var skillRoots, markdownRoots []string
	for _, candidate := range harnesses {
		root, err := candidate.StagedRoot(ctx)
		if err != nil {
			return err
		}
		skillRoots = append(skillRoots, root)
		if candidate.ID() != base.Codex {
			markdownRoots = append(markdownRoots, root)
		}
	}
	home, _ := os.UserHomeDir()
	removed, err := ResolveCollisionsWithHome(bundle, skillRoots, markdownRoots, home)
	if err != nil {
		return err
	}
	pluginPackages := pipeline.Lock.Plugins()
	for _, skill := range pipeline.Lock.Skills() {
		if disabledPlugin(pipeline.Lock, skill.CacheKey) || SkillIsShadowed(skill, pluginPackages) {
			continue
		}
		allowed, err := pipeline.Mode.AllowsPackagePath(skill.Module, "SKILL.md")
		if err != nil {
			return err
		}
		if !allowed {
			continue
		}
		cacheRoot, err := cache.EntryDir(skill.CacheKey)
		if err != nil {
			continue
		}
		if _, err := os.Stat(filepath.Join(cacheRoot, "SKILL.md")); err != nil {
			continue
		}
		name := SkillFolderName(skill)
		if _, collided := removed.SkillSlugs[strings.ToLower(name)]; collided {
			continue
		}
		for index, root := range skillRoots {
			path := filepath.Join(root, "skills", name, "SKILL.md")
			if _, err := os.Stat(path); err != nil {
				return fmt.Errorf("%s staging missing skill SKILL.md %s", harnesses[index].ID(), path)
			}
		}
	}
	return nil
}
func (pipeline Pipeline) context() base.StageContext {
	return base.StageContext{ProjectRoot: pipeline.ProjectRoot, Mode: pipeline.Mode, LaunchTarget: pipeline.Target}
}
func (pipeline Pipeline) stageHooks(ctx base.StageContext, harnesses []base.Harness) error {
	codexHarness, err := registry.ByTarget(base.Codex)
	if err != nil {
		return err
	}
	codexRoot, err := codexHarness.StagedRoot(ctx)
	if err != nil {
		return err
	}
	bundle, err := hooks.Collect(pipeline.ProjectRoot, pipeline.Lock, filepath.Join(codexRoot, "hooks.json"), pipeline.Mode)
	if err != nil {
		return err
	}
	if len(bundle.Hooks) == 0 {
		return nil
	}
	for _, candidate := range harnesses {
		renderer := registry.Renderer(candidate.ID())
		if renderer == nil {
			continue
		}
		root, err := candidate.StagedRoot(ctx)
		if err != nil {
			return err
		}
		packages, err := hooks.StageOriginPackages(bundle, candidate.ID(), root, pipeline.Mode)
		if err != nil {
			return err
		}
		output, err := renderer.Render(bundle, hooks.RenderContext{ProjectRoot: pipeline.ProjectRoot, TargetRoot: root, StagedPackages: packages})
		if err != nil {
			return err
		}
		if err := hooks.WriteRenderedFiles(output); err != nil {
			return err
		}
	}
	return nil
}
func removeRebuildPath(path string) error {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return os.Remove(path)
	}
	trash := fmt.Sprintf("%s.agentpack-reset-%d", path, os.Getpid())
	_ = os.RemoveAll(trash)
	if err := os.Rename(path, trash); err != nil {
		return err
	}
	return os.RemoveAll(trash)
}
