package staging

import (
	"bytes"
	"encoding/json"
	"errors"
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
	"github.com/OlegHQ/agentpack/internal/mcp"
	"github.com/OlegHQ/agentpack/internal/mode"
	"github.com/OlegHQ/agentpack/internal/paths"
)

type Pipeline struct {
	// ProjectRoot is the definition root (manifest, lock, staging identity).
	ProjectRoot string
	// WorkspaceRoot is the checkout for .agents inputs; empty means ProjectRoot.
	WorkspaceRoot string
	Lock          lockfile.PackLock
	Manifest      *manifest.Manifest
	Mode          mode.Effective
	Target        *base.Target
	// StrictExternal refuses workspace overlay materialization.
	StrictExternal bool
}

func (pipeline Pipeline) workspace() string {
	if pipeline.WorkspaceRoot != "" {
		return pipeline.WorkspaceRoot
	}
	return pipeline.ProjectRoot
}

func (pipeline Pipeline) Rebuild() (_ []string, rebuildErr error) {
	ctx := pipeline.context()
	harnesses := registry.All()
	managed, err := pipeline.managedRebuildPaths(ctx, harnesses)
	if err != nil {
		return nil, err
	}
	journalPath, err := pipeline.rebuildJournalPath()
	if err != nil {
		return nil, err
	}
	if err := recoverRebuildJournal(journalPath, managed); err != nil {
		return nil, fmt.Errorf("recover interrupted staging: %w", err)
	}
	for _, candidate := range harnesses {
		if err := candidate.PreReset(ctx); err != nil {
			return nil, fmt.Errorf("pre-reset %s: %w", candidate.ID(), err)
		}
	}
	var transactions []base.RebuildTransaction
	defer func() {
		for index := len(transactions) - 1; index >= 0; index-- {
			if err := transactions[index].Abort(); err != nil {
				rebuildErr = errors.Join(rebuildErr, err)
			}
		}
	}()
	ctx.StagedRoots = make(map[base.Target]string)
	for _, candidate := range harnesses {
		transaction, err := candidate.BeginRebuild(ctx)
		if err != nil {
			return nil, fmt.Errorf("begin rebuild %s: %w", candidate.ID(), err)
		}
		if transaction != nil {
			transactions = append(transactions, transaction)
			ctx.StagedRoots[candidate.ID()] = transaction.Root()
		}
	}
	backups, err := backupRebuildPaths(managed, journalPath)
	if err != nil {
		return nil, err
	}
	published := false
	defer func() { rebuildErr = errors.Join(rebuildErr, finishRebuildJournal(journalPath, backups, published)) }()
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
	codexRoot, err := registryRoot(harnesses, base.Codex, ctx)
	if err != nil {
		return nil, err
	}
	if err := StageDotAgents(pipeline.ProjectRoot, pipeline.workspace(), pipeline.Mode.Name(), pipeline.Mode, codexRoot); err != nil {
		return nil, err
	}
	plugins, err := paths.StagingPluginsDirForMode(pipeline.ProjectRoot, pipeline.Mode.Name())
	if err != nil {
		return nil, err
	}
	bundle := filepath.Join(plugins, paths.StagedAgentpackBundleName)
	if err := OmitProjectClaudeSkillDuplicates(pipeline.workspace(), bundle); err != nil {
		return nil, err
	}
	merged, err := CollectMCP(pipeline.workspace(), pipeline.Lock, pipeline.Manifest, &pipeline.Mode)
	if err != nil {
		return nil, err
	}
	merged = mcp.ApplyPins(merged, pipeline.Lock.MCPServers)
	if len(merged) != 0 {
		for _, candidate := range harnesses {
			if err := candidate.WriteMCP(merged, ctx); err != nil {
				return nil, fmt.Errorf("write MCP %s: %w", candidate.ID(), err)
			}
		}
	}
	guidance, err := CollectGuidance(pipeline.workspace(), pipeline.Lock, pipeline.Mode)
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
		if pipeline.StrictExternal && pipeline.Target.UsesWorkspaceOverlay() {
			return nil, fmt.Errorf("%s requires a workspace overlay write; refuse under --strict-external (use claude/opencode, or omit --strict-external)", *pipeline.Target)
		}
		candidate, err := registry.ByTarget(*pipeline.Target)
		if err != nil {
			return nil, err
		}
		if err := candidate.FinalizeWorkspaceOverlay(ctx); err != nil {
			return nil, err
		}
	}
	if err := pipeline.verify(ctx); err != nil {
		return nil, err
	}
	if err := pipeline.recordStaged(ctx); err != nil {
		return nil, fmt.Errorf("record staged content: %w", err)
	}
	for _, transaction := range transactions {
		if err := transaction.Commit(); err != nil {
			return nil, fmt.Errorf("publish staged home: %w", err)
		}
	}
	published = true
	return []string{filepath.Join(plugins, paths.StagedAgentpackBundleName)}, nil
}

func (pipeline Pipeline) Verify() error {
	return pipeline.verify(pipeline.context())
}

func (pipeline Pipeline) verify(ctx base.StageContext) error {
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
	projectClaudeSkills, err := ProjectClaudeSkillNames(pipeline.workspace())
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
			if harnesses[index].ID() == base.Claude {
				if _, omitted := projectClaudeSkills[strings.ToLower(name)]; omitted {
					continue
				}
			}
			path := filepath.Join(root, "skills", name, "SKILL.md")
			if _, err := os.Stat(path); err != nil {
				return fmt.Errorf("%s staging missing skill SKILL.md %s", harnesses[index].ID(), path)
			}
		}
	}
	return nil
}

func registryRoot(harnesses []base.Harness, target base.Target, ctx base.StageContext) (string, error) {
	for _, candidate := range harnesses {
		if candidate.ID() == target {
			return candidate.StagedRoot(ctx)
		}
	}
	return "", fmt.Errorf("harness %s is not registered", target)
}

func (pipeline Pipeline) context() base.StageContext {
	return base.StageContext{
		ProjectRoot:    pipeline.ProjectRoot,
		WorkspaceRoot:  pipeline.workspace(),
		Mode:           pipeline.Mode,
		LaunchTarget:   pipeline.Target,
		StrictExternal: pipeline.StrictExternal,
	}
}

// HookDiagnostics returns renderer findings without writing hook files.
func (pipeline Pipeline) HookDiagnostics() ([]hooks.Diagnostic, hooks.RenderSummary, error) {
	bundle, err := hooks.Collect(pipeline.workspace(), pipeline.Lock, "", pipeline.Mode)
	if err != nil {
		return nil, hooks.RenderSummary{}, err
	}
	var diagnostics []hooks.Diagnostic
	var summary hooks.RenderSummary
	for _, target := range base.AllTargets() {
		renderer := registry.Renderer(target)
		if renderer == nil || len(bundle.Hooks) == 0 {
			continue
		}
		root, err := paths.StagingRootForMode(pipeline.ProjectRoot, pipeline.Mode.Name())
		if err != nil {
			return nil, hooks.RenderSummary{}, err
		}
		output, err := renderer.Render(bundle, hooks.RenderContext{ProjectRoot: pipeline.ProjectRoot, TargetRoot: filepath.Join(root, string(target)), StagedPackages: map[string]string{}})
		if err != nil {
			return nil, hooks.RenderSummary{}, err
		}
		diagnostics = append(diagnostics, output.Diagnostics...)
		summary.Native += output.Summary.Native
		summary.Emulated += output.Summary.Emulated
		summary.Degraded += output.Summary.Degraded
		summary.Omitted += output.Summary.Omitted
	}
	return diagnostics, summary, nil
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
	bundle, err := hooks.Collect(pipeline.workspace(), pipeline.Lock, filepath.Join(codexRoot, "hooks.json"), pipeline.Mode)
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
		for _, diagnostic := range output.Diagnostics {
			fmt.Fprintf(os.Stderr, "warning: %s: %s\n", diagnostic.Source, diagnostic.Message)
		}
		if err := hooks.WriteRenderedFiles(output); err != nil {
			return err
		}
	}
	return nil
}

// A durable journal records original paths before any rename. Production callers
// hold the rebuild-exclusive lock; launch fast paths refuse pending journals.
type rebuildBackup struct {
	Path      string `json:"path"`
	Directory string `json:"directory,omitempty"`
	Existed   bool   `json:"existed"`
}
type rebuildJournal struct {
	SchemaVersion int             `json:"schema_version"`
	Committed     bool            `json:"committed"`
	Backups       []rebuildBackup `json:"backups"`
}

func (pipeline Pipeline) rebuildJournalPath() (string, error) {
	return paths.ProjectStateFile(pipeline.ProjectRoot, "rebuild-"+paths.ModePathComponent(pipeline.Mode.Name())+".json")
}

// RebuildPending tells launchers to restore before using staging after an interruption.
func (pipeline Pipeline) RebuildPending() (bool, error) {
	path, err := pipeline.rebuildJournalPath()
	if err != nil {
		return false, err
	}
	_, err = os.Lstat(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	return err == nil, err
}

func (pipeline Pipeline) managedRebuildPaths(ctx base.StageContext, harnesses []base.Harness) ([]string, error) {
	var managed []string
	for _, candidate := range harnesses {
		if candidate.ID() == base.Codex {
			continue
		}
		reset, err := candidate.ResetPaths(ctx)
		if err != nil {
			return nil, err
		}
		managed = append(managed, reset...)
		root, err := candidate.StagedRoot(ctx)
		if err != nil {
			return nil, err
		}
		managed = append(managed, root)
	}
	grokHome, err := paths.StagingGrokHomeDirForMode(pipeline.ProjectRoot, pipeline.Mode.Name())
	if err != nil {
		return nil, err
	}
	managed = append(managed, grokHome)
	manifestPath, err := paths.StagedManifestPath(pipeline.ProjectRoot, pipeline.Mode.Name())
	if err != nil {
		return nil, err
	}
	managed = append(managed, manifestPath)
	return normalizeRebuildPaths(managed), nil
}

func normalizeRebuildPaths(values []string) []string {
	for index, path := range values {
		values[index] = filepath.Clean(path)
	}
	sort.Strings(values)
	var result []string
	for _, path := range slices.Compact(values) {
		nested := false
		for _, parent := range result {
			if strings.HasPrefix(path, parent+string(filepath.Separator)) {
				nested = true
				break
			}
		}
		if !nested {
			result = append(result, path)
		}
	}
	return result
}

func writeRebuildJournal(path string, journal rebuildJournal) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.Marshal(journal)
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".rebuild-journal-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err := file.Write(data); err != nil {
		file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}

func backupRebuildPaths(values []string, journalPath string) ([]rebuildBackup, error) {
	var backups []rebuildBackup
	for _, path := range normalizeRebuildPaths(values) {
		backup := rebuildBackup{Path: path}
		_, err := os.Lstat(path)
		if err != nil && !os.IsNotExist(err) {
			return nil, errors.Join(err, finishRebuildBackups(backups, true))
		}
		if err == nil {
			backup.Existed = true
			backup.Directory, err = os.MkdirTemp(filepath.Dir(path), ".agentpack-rebuild-backup-*")
			if err != nil {
				return nil, errors.Join(err, finishRebuildBackups(backups, true))
			}
		}
		backups = append(backups, backup)
	}
	if err := writeRebuildJournal(journalPath, rebuildJournal{SchemaVersion: 1, Backups: backups}); err != nil {
		return nil, errors.Join(err, finishRebuildBackups(backups, true))
	}
	for _, backup := range backups {
		if backup.Existed {
			if err := os.Rename(backup.Path, filepath.Join(backup.Directory, "previous")); err != nil {
				return nil, errors.Join(err, finishRebuildJournal(journalPath, backups, false))
			}
		}
	}
	return backups, nil
}

func recoverRebuildJournal(path string, managed []string) error {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() > 1<<20 {
		return fmt.Errorf("invalid staging recovery journal")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var journal rebuildJournal
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&journal); err != nil {
		return err
	}
	if journal.SchemaVersion != 1 {
		return fmt.Errorf("unsupported rebuild journal schema")
	}
	allowed := map[string]bool{}
	for _, value := range managed {
		allowed[value] = true
	}
	seen := map[string]bool{}
	if len(journal.Backups) != len(allowed) {
		return fmt.Errorf("recovery journal does not enumerate every managed path")
	}
	for _, backup := range journal.Backups {
		if !allowed[backup.Path] || seen[backup.Path] {
			return fmt.Errorf("recovery journal references an unexpected managed path")
		}
		seen[backup.Path] = true
		if backup.Existed {
			if filepath.Dir(backup.Directory) != filepath.Dir(backup.Path) || !strings.HasPrefix(filepath.Base(backup.Directory), ".agentpack-rebuild-backup-") {
				return fmt.Errorf("invalid staging backup location")
			}
			if info, err := os.Lstat(backup.Directory); err == nil {
				if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
					return fmt.Errorf("staging backup must be a private directory")
				}
			} else if !os.IsNotExist(err) {
				return err
			}
		} else if backup.Directory != "" {
			return fmt.Errorf("unexpected staging backup for absent path")
		}
	}
	return finishRebuildJournal(path, journal.Backups, journal.Committed)
}

func finishRebuildJournal(path string, backups []rebuildBackup, published bool) error {
	if published {
		if err := writeRebuildJournal(path, rebuildJournal{SchemaVersion: 1, Committed: true, Backups: backups}); err != nil {
			return fmt.Errorf("staging published but recovery marker could not be saved: %w", err)
		}
	}
	if err := finishRebuildBackups(backups, published); err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func finishRebuildBackups(backups []rebuildBackup, published bool) error {
	var failures []error
	for index := len(backups) - 1; index >= 0; index-- {
		backup := backups[index]
		if !published {
			restore := !backup.Existed
			if backup.Existed {
				if _, err := os.Lstat(filepath.Join(backup.Directory, "previous")); err == nil {
					restore = true
				} else if !os.IsNotExist(err) {
					failures = append(failures, err)
					continue
				} else if _, originalErr := os.Lstat(backup.Path); originalErr != nil {
					failures = append(failures, fmt.Errorf("prior staging and backup are missing for %s; journal retained", backup.Path))
					continue
				}
			}
			// A missing prior tree means interruption occurred before its rename, or
			// rollback already restored it. Never remove that surviving original.
			if restore {
				if err := os.RemoveAll(backup.Path); err != nil {
					failures = append(failures, fmt.Errorf("rollback %s (backup retained at %s): %w", backup.Path, backup.Directory, err))
					continue
				}
				if backup.Existed {
					if err := os.Rename(filepath.Join(backup.Directory, "previous"), backup.Path); err != nil {
						failures = append(failures, fmt.Errorf("restore %s (backup retained at %s): %w", backup.Path, backup.Directory, err))
						continue
					}
				}
			}
		}
		if backup.Directory != "" {
			if err := os.RemoveAll(backup.Directory); err != nil {
				failures = append(failures, err)
			}
		}
	}
	return errors.Join(failures...)
}
