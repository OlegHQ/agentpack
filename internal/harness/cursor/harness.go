package cursor

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	base "github.com/OlegHQ/agentpack/internal/harness"
	"github.com/OlegHQ/agentpack/internal/mcp"
	"github.com/OlegHQ/agentpack/internal/paths"
)

func New() base.Harness {
	return base.Definition{Target: base.Cursor, Root: stagedRoot, Reset: resetPaths, Setup: prepare, MCP: writeMCP, AfterStage: finalize, WorkspaceOverlay: materializeAgentsOverlay, Check: verify, Launch: launch}
}

func launch(ctx base.LaunchContext) (*exec.Cmd, error) {
	arguments := append([]string(nil), ctx.Arguments...)
	if !base.HasFlagValue(arguments, "--workspace") {
		arguments = append([]string{"--workspace", base.WorkspaceRoot(ctx.ProjectRoot)}, arguments...)
	}
	if ctx.Yolo {
		arguments = base.PrependOnce(arguments, "--force", "--yolo")
	}
	if allowsTrust(arguments) && !base.HasAny(arguments, "--trust") {
		arguments = append([]string{"--trust"}, arguments...)
	}
	binary, err := base.ResolveBinary("CURSOR_AGENT_PATH", "cursor-agent")
	if err != nil {
		return nil, err
	}
	home, err := paths.StagingCursorHomeDirForMode(ctx.ProjectRoot, ctx.Mode.Name())
	if err != nil {
		return nil, err
	}
	command := exec.Command(binary, arguments...)
	environment := append(os.Environ(), "HOME="+home, "CURSOR_CONFIG_DIR="+filepath.Join(home, ".cursor"))
	if runtime.GOOS == "windows" {
		environment = append(environment, "USERPROFILE="+home, "APPDATA="+filepath.Join(home, "AppData", "Roaming"), "LOCALAPPDATA="+filepath.Join(home, "AppData", "Local"))
	} else if runtime.GOOS == "linux" {
		environment = append(environment, "XDG_CONFIG_HOME="+filepath.Join(home, ".config"), "XDG_DATA_HOME="+filepath.Join(home, ".local", "share"))
	}
	if realHome, err := os.UserHomeDir(); err == nil {
		for key, value := range map[string]string{"CARGO_HOME": filepath.Join(realHome, ".cargo"), "RUSTUP_HOME": filepath.Join(realHome, ".rustup"), "DOCKER_CONFIG": filepath.Join(realHome, ".docker"), "CURSOR_DATA_DIR": filepath.Join(realHome, ".cursor")} {
			if os.Getenv(key) == "" {
				environment = append(environment, key+"="+value)
			}
		}
	}
	command.Env = environment
	return command, nil
}
func allowsTrust(arguments []string) bool {
	for index, argument := range arguments {
		if argument == "-p" || argument == "--print" || strings.HasPrefix(argument, "--output-format=") || (argument == "--output-format" && index+1 < len(arguments)) {
			return true
		}
	}
	return false
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
