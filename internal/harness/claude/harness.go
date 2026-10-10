package claude

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	base "github.com/OlegHQ/agentpack/internal/harness"
	"github.com/OlegHQ/agentpack/internal/mcp"
	"github.com/OlegHQ/agentpack/internal/paths"
)

func New() base.Harness {
	return base.Definition{Target: base.Claude, Root: stagedRoot, Reset: resetPaths, Setup: prepare, MCP: writeMCP, Guidance: injectGuidance, AfterStage: finalize, Check: verify, Launch: launch}
}

func launch(ctx base.LaunchContext) (*exec.Cmd, error) {
	binary, err := base.ResolveBinary("CLAUDE_CODE_PATH", "claude")
	if err != nil {
		return nil, err
	}
	arguments := append([]string(nil), ctx.Arguments...)
	if ctx.Yolo {
		arguments = base.PrependOnce(arguments, "--dangerously-skip-permissions")
	}
	command := exec.Command(binary)
	settings, err := SettingsPath(base.StageContext{ProjectRoot: ctx.ProjectRoot, WorkspaceRoot: ctx.WorkspaceRoot, Mode: ctx.Mode})
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(settings)
	if err != nil {
		return nil, fmt.Errorf("read staged Claude settings: %w", err)
	}
	var value map[string]any
	if err := json.Unmarshal(data, &value); err != nil {
		return nil, fmt.Errorf("parse staged Claude settings: %w", err)
	}
	// Inline settings avoid a project-dependent config filename in Claude's credential namespace.
	command.Args = append(command.Args, "--settings", string(data))
	plugins, err := paths.StagingPluginsDirForMode(ctx.ProjectRoot, ctx.Mode.Name())
	if err != nil {
		return nil, err
	}
	entries, _ := os.ReadDir(plugins)
	for _, entry := range entries {
		path := filepath.Join(plugins, entry.Name())
		if _, err := os.Stat(filepath.Join(path, ".claude-plugin", "plugin.json")); err == nil {
			command.Args = append(command.Args, "--plugin-dir", path)
		}
	}
	command.Args = append(command.Args, arguments...)
	return command, nil
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
	return MaterializeSettings(ctx)
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
func finalize(entries mcp.Entries, ctx base.StageContext) error {
	var names []string
	for _, name := range entries.Names() {
		disabled := entries[name].Server.Disabled
		if disabled == nil || !*disabled {
			names = append(names, name)
		}
	}
	return SetMCPAllowlist(ctx, names)
}
func verify(ctx base.StageContext) error {
	bundle, err := stagedRoot(ctx)
	if err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(bundle, ".claude-plugin", "plugin.json")); err != nil {
		return fmt.Errorf("bundle missing manifest %s: %w", bundle, err)
	}
	{
		overlay, err := SettingsPath(ctx)
		if err != nil {
			return err
		}
		if _, err := os.Stat(overlay); err != nil {
			return fmt.Errorf("claude settings overlay missing %s: %w", overlay, err)
		}
	}
	return nil
}
