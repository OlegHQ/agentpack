package claude

import (
	"fmt"
	"os"
	"path/filepath"

	base "github.com/OlegHQ/agentpack/internal/harness"
	"github.com/OlegHQ/agentpack/internal/mcp"
	"github.com/OlegHQ/agentpack/internal/paths"
)

func New() base.Harness {
	return base.Definition{Target: base.Claude, Root: stagedRoot, Reset: resetPaths, Setup: prepare, MCP: writeMCP, Guidance: injectGuidance, AfterStage: finalize, Check: verify}
}

func stagedRoot(ctx base.StageContext) (string, error) {
	plugins, err := paths.StagingPluginsDirForMode(ctx.ProjectRoot, ctx.Mode.Name())
	if err != nil {
		return "", err
	}
	return filepath.Join(plugins, paths.StagedAgentpackBundleName), nil
}
func resetPaths(ctx base.StageContext) ([]string, error) {
	plugins, err := paths.StagingPluginsDirForMode(ctx.ProjectRoot, ctx.Mode.Name())
	if err != nil {
		return nil, err
	}
	return []string{plugins}, nil
}

func prepare(ctx base.StageContext) error {
	bundle, err := stagedRoot(ctx)
	if err != nil {
		return err
	}
	manifestDirectory := filepath.Join(bundle, ".claude-plugin")
	if err := os.MkdirAll(manifestDirectory, 0o755); err != nil {
		return err
	}
	manifest := `{"name":"agentpack-bundle","version":"1.0.0","description":"Merged pack.lock plugins/skills; optional user settings.json and .claude.json"}`
	if err := os.WriteFile(filepath.Join(manifestDirectory, "plugin.json"), []byte(manifest), 0o644); err != nil {
		return err
	}
	return MaterializeSettings()
}

func writeMCP(entries mcp.Entries, ctx base.StageContext) error {
	root, err := stagedRoot(ctx)
	if err != nil {
		return err
	}
	return WriteMCP(filepath.Join(root, ".mcp.json"), entries)
}
func injectGuidance(blob string, ctx base.StageContext) error {
	root, err := stagedRoot(ctx)
	if err != nil {
		return err
	}
	return InjectGuidance(root, blob)
}
func finalize(entries mcp.Entries, _ base.StageContext) error {
	var names []string
	for _, name := range entries.Names() {
		disabled := entries[name].Server.Disabled
		if disabled == nil || !*disabled {
			names = append(names, name)
		}
	}
	if len(names) != 0 {
		return SetMCPAllowlist(names)
	}
	return nil
}
func verify(ctx base.StageContext) error {
	bundle, err := stagedRoot(ctx)
	if err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(bundle, ".claude-plugin", "plugin.json")); err != nil {
		return fmt.Errorf("bundle missing manifest %s: %w", bundle, err)
	}
	if !KeepAttribution() {
		overlay, err := paths.AgentpackClaudeSettingsPath()
		if err != nil {
			return err
		}
		if _, err := os.Stat(overlay); err != nil {
			return fmt.Errorf("claude settings overlay missing %s: %w", overlay, err)
		}
	}
	return nil
}
