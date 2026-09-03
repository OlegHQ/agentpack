package cursor

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	base "github.com/OlegHQ/agentpack/internal/harness"
	"github.com/OlegHQ/agentpack/internal/mcp"
	"github.com/OlegHQ/agentpack/internal/paths"
)

func New() base.Harness {
	return base.Definition{Target: base.Cursor, Root: stagedRoot, Reset: resetPaths, Setup: prepare, MCP: writeMCP, AfterStage: finalize, WorkspaceOverlay: materializeAgentsOverlay, Check: verify}
}
func stagedRoot(ctx base.StageContext) (string, error) {
	return paths.StagingCursorPackPluginDirForMode(ctx.ProjectRoot, ctx.Mode.Name())
}
func resetPaths(ctx base.StageContext) ([]string, error) {
	bundle, err := paths.StagingCursorBundleDirForMode(ctx.ProjectRoot, ctx.Mode.Name())
	if err != nil {
		return nil, err
	}
	home, err := paths.StagingCursorHomeDirForMode(ctx.ProjectRoot, ctx.Mode.Name())
	if err != nil {
		return nil, err
	}
	return []string{bundle, home}, nil
}
func prepare(ctx base.StageContext) error {
	if err := cleanupOverlay(ctx.ProjectRoot); err != nil {
		return err
	}
	bundle, err := paths.StagingCursorBundleDirForMode(ctx.ProjectRoot, ctx.Mode.Name())
	if err != nil {
		return err
	}
	if err := os.MkdirAll(bundle, 0o755); err != nil {
		return err
	}
	if home, err := os.UserHomeDir(); err == nil {
		if err := base.CopySelectedEntries(filepath.Join(home, ".cursor"), bundle, []string{"cli-config.json", "mcp.json"}); err != nil {
			return err
		}
	}
	if err := writeManifests(bundle); err != nil {
		return err
	}
	pack, err := stagedRoot(ctx)
	if err != nil {
		return err
	}
	if !keepAttribution() {
		if err := forceAttribution(bundle); err != nil {
			return err
		}
		if err := forceAttribution(pack); err != nil {
			return err
		}
	}
	return nil
}
func forceAttribution(root string) error {
	path := filepath.Join(root, "cli-config.json")
	value := make(map[string]any)
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, &value)
	}
	return writeJSON(path, patchAttribution(value))
}
func writeMCP(entries mcp.Entries, ctx base.StageContext) error {
	root, err := stagedRoot(ctx)
	if err != nil {
		return err
	}
	return WriteMCP(filepath.Join(root, "mcp.json"), entries)
}
func finalize(_ mcp.Entries, ctx base.StageContext) error {
	pack, err := stagedRoot(ctx)
	if err != nil {
		return err
	}
	if err := writeReadme(pack); err != nil {
		return err
	}
	return materializeFakeHome(ctx)
}
func verify(ctx base.StageContext) error {
	bundle, err := paths.StagingCursorBundleDirForMode(ctx.ProjectRoot, ctx.Mode.Name())
	if err != nil {
		return err
	}
	pack, err := stagedRoot(ctx)
	if err != nil {
		return err
	}
	home, err := paths.StagingCursorHomeDirForMode(ctx.ProjectRoot, ctx.Mode.Name())
	if err != nil {
		return err
	}
	for _, path := range []string{filepath.Join(bundle, ".cursor-plugin", "marketplace.json"), filepath.Join(pack, ".cursor-plugin", "plugin.json"), filepath.Join(home, ".cursor")} {
		if _, err := os.Stat(path); err != nil {
			return fmt.Errorf("cursor staging missing %s: %w", path, err)
		}
	}
	if ctx.LaunchTarget != nil && *ctx.LaunchTarget == base.Cursor {
		entries, err := readOverlayManifest(ctx.ProjectRoot)
		if err != nil {
			return err
		}
		for _, path := range entries {
			if _, err := os.Lstat(path); err != nil {
				return fmt.Errorf("cursor workspace overlay missing at %s: %w", path, err)
			}
		}
	}
	return nil
}
func keepAttribution() bool {
	switch os.Getenv("AGENTPACK_KEEP_ATTRIBUTION") {
	case "1", "true", "yes":
		return true
	default:
		return false
	}
}
