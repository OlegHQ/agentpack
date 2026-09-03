package agy

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	base "github.com/OlegHQ/agentpack/internal/harness"
	"github.com/OlegHQ/agentpack/internal/mcp"
	"github.com/OlegHQ/agentpack/internal/paths"
)

const workspaceOverlay = ".agents/plugins/agentpack-bundle"

func New() base.Harness {
	return base.Definition{Target: base.Agy, Root: stagedRoot, Reset: resetPaths, Setup: prepare, MCP: writeMCP, WorkspaceOverlay: finalizeWorkspace, Check: verify, Launch: launch}
}

func launch(ctx base.LaunchContext) (*exec.Cmd, error) {
	arguments := append([]string(nil), ctx.Arguments...)
	if !base.HasFlagValue(arguments, "--add-dir") {
		arguments = append([]string{"--add-dir", base.WorkspaceRoot(ctx.ProjectRoot)}, arguments...)
	}
	if ctx.Yolo {
		arguments = base.PrependOnce(arguments, "--dangerously-skip-permissions")
	}
	binary, err := base.ResolveBinary("AGY_PATH", "agy")
	if err != nil {
		return nil, err
	}
	return exec.Command(binary, arguments...), nil
}
func stagedRoot(ctx base.StageContext) (string, error) {
	return paths.StagingAgyBundleDirForMode(ctx.ProjectRoot, ctx.Mode.Name())
}
func resetPaths(ctx base.StageContext) ([]string, error) {
	root, err := paths.StagingAgyDirForMode(ctx.ProjectRoot, ctx.Mode.Name())
	if err != nil {
		return nil, err
	}
	return []string{root}, nil
}
func prepare(ctx base.StageContext) error {
	if err := cleanupOverlay(ctx.ProjectRoot); err != nil {
		return err
	}
	bundle, err := stagedRoot(ctx)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(bundle, 0o755); err != nil {
		return err
	}
	data, _ := json.MarshalIndent(map[string]string{"name": "agentpack-bundle"}, "", "  ")
	if err := os.WriteFile(filepath.Join(bundle, "plugin.json"), data, 0o644); err != nil {
		return err
	}
	if keepAttribution() {
		return nil
	}
	rule := "---\ndescription: Disable AI attribution footers\nalwaysApply: true\n---\n\n" + strings.TrimSpace(base.NoAttributionBody) + "\n"
	path := filepath.Join(bundle, "rules", "agentpack-no-attribution.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(rule), 0o644)
}
func writeMCP(entries mcp.Entries, ctx base.StageContext) error {
	root, err := stagedRoot(ctx)
	if err != nil {
		return err
	}
	return WriteMCP(filepath.Join(root, "mcp_config.json"), entries)
}
func verify(ctx base.StageContext) error {
	root, err := stagedRoot(ctx)
	if err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(root, "plugin.json")); err != nil {
		return fmt.Errorf("agy bundle missing %s: %w", root, err)
	}
	if ctx.LaunchTarget != nil && *ctx.LaunchTarget == base.Agy {
		entries, err := readOverlayManifest(ctx.ProjectRoot)
		if err != nil {
			return err
		}
		for _, path := range entries {
			if _, err := os.Lstat(path); err != nil {
				return fmt.Errorf("agy workspace overlay missing at %s: %w", path, err)
			}
		}
	}
	return nil
}
func finalizeWorkspace(ctx base.StageContext) error {
	bundle, err := stagedRoot(ctx)
	if err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(bundle, "plugin.json")); err != nil {
		return nil
	}
	workspace, err := os.Getwd()
	if err != nil {
		workspace = ctx.ProjectRoot
	}
	overlay := filepath.Join(workspace, filepath.FromSlash(workspaceOverlay))
	if info, err := os.Lstat(overlay); err == nil {
		if info.IsDir() && info.Mode()&os.ModeSymlink == 0 || info.Mode().IsRegular() {
			return nil
		}
		if err := os.Remove(overlay); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(overlay), 0o755); err != nil {
		return err
	}
	target, err := filepath.Abs(bundle)
	if err != nil {
		return err
	}
	if err := os.Symlink(target, overlay); err != nil {
		if err := base.CopySelectedEntries(bundle, overlay, entriesIn(bundle)); err != nil {
			return err
		}
	}
	return writeOverlayManifest(ctx.ProjectRoot, []string{overlay})
}
func entriesIn(root string) []string {
	entries, _ := os.ReadDir(root)
	values := make([]string, 0, len(entries))
	for _, entry := range entries {
		values = append(values, entry.Name())
	}
	return values
}
func keepAttribution() bool {
	switch os.Getenv("AGENTPACK_KEEP_ATTRIBUTION") {
	case "1", "true", "yes":
		return true
	default:
		return false
	}
}

func readOverlayManifest(projectRoot string) ([]string, error) {
	path, err := paths.AgyOverlayManifestPath(projectRoot)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var result []string
	for _, line := range strings.Split(string(data), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			result = append(result, line)
		}
	}
	return result, nil
}
func writeOverlayManifest(projectRoot string, entries []string) error {
	path, err := paths.AgyOverlayManifestPath(projectRoot)
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	sort.Strings(entries)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(strings.Join(entries, "\n")+"\n"), 0o644)
}
func cleanupOverlay(projectRoot string) error {
	entries, err := readOverlayManifest(projectRoot)
	if err != nil {
		return err
	}
	for _, path := range entries {
		info, err := os.Lstat(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || info.Mode().IsRegular() {
			if err := os.Remove(path); err != nil {
				return err
			}
		}
	}
	return writeOverlayManifest(projectRoot, nil)
}
