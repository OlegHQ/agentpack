package opencode

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	base "github.com/OlegHQ/agentpack/internal/harness"
	"github.com/OlegHQ/agentpack/internal/mcp"
	"github.com/OlegHQ/agentpack/internal/paths"
	"github.com/tailscale/hujson"
)

const instructionsFile = "agentpack-no-attribution.md"

func New() base.Harness {
	return base.Definition{Target: base.OpenCode, Root: stagedRoot, Setup: prepare, MCP: writeMCP, Guidance: injectGuidance, Check: verify, Launch: launch}
}

func launch(ctx base.LaunchContext) (*exec.Cmd, error) {
	root, err := paths.StagingOpenCodeDirForMode(ctx.ProjectRoot, ctx.Mode.Name())
	if err != nil {
		return nil, err
	}
	if ctx.Yolo {
		path := filepath.Join(root, "opencode.json")
		value := make(map[string]any)
		if data, readErr := os.ReadFile(path); readErr == nil {
			standard, err := hujson.Standardize(append(data, '\n'))
			if err != nil {
				return nil, err
			}
			if err := json.Unmarshal(standard, &value); err != nil {
				return nil, err
			}
		}
		value["permission"] = "allow"
		if err := writeConfig(path, value); err != nil {
			return nil, err
		}
	}
	binary, err := base.ResolveBinary("OPENCODE_PATH", "opencode")
	if err != nil {
		return nil, err
	}
	command := exec.Command(binary, ctx.Arguments...)
	command.Env = append(os.Environ(), "OPENCODE_CONFIG_DIR="+root)
	return command, nil
}

func stagedRoot(ctx base.StageContext) (string, error) {
	return paths.StagingOpenCodeDirForMode(ctx.ProjectRoot, ctx.Mode.Name())
}

func prepare(ctx base.StageContext) error {
	root, err := stagedRoot(ctx)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return err
	}
	home, err := os.UserHomeDir()
	if err == nil {
		if err := base.CopySelectedEntries(filepath.Join(home, ".config", "opencode"), root, []string{"opencode.json", "agents", "commands", "modes", "plugins", "skills"}); err != nil {
			return err
		}
	}
	configPath := filepath.Join(root, "opencode.json")
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		if err := writeConfig(configPath, map[string]any{"$schema": "https://opencode.ai/config.json"}); err != nil {
			return err
		}
	}
	if !keepAttribution() {
		return forceAttributionOff(root)
	}
	return nil
}

func forceAttributionOff(root string) error {
	if err := os.WriteFile(filepath.Join(root, instructionsFile), []byte(base.NoAttributionBody), 0o644); err != nil {
		return err
	}
	path := filepath.Join(root, "opencode.json")
	value := make(map[string]any)
	if data, err := os.ReadFile(path); err == nil {
		standard, standardErr := hujson.Standardize(append(data, '\n'))
		if standardErr != nil || json.Unmarshal(standard, &value) != nil {
			value = make(map[string]any)
		}
	}
	instructions, _ := value["instructions"].([]any)
	found := false
	for _, instruction := range instructions {
		if instruction == instructionsFile {
			found = true
		}
	}
	if !found {
		instructions = append(instructions, instructionsFile)
	}
	value["instructions"] = instructions
	return writeConfig(path, value)
}

func writeMCP(entries mcp.Entries, ctx base.StageContext) error {
	root, err := stagedRoot(ctx)
	if err != nil {
		return err
	}
	return MergeMCP(filepath.Join(root, "opencode.json"), entries)
}
func injectGuidance(blob string, ctx base.StageContext) error {
	root, err := stagedRoot(ctx)
	if err != nil {
		return err
	}
	return base.WriteGuidance(filepath.Join(root, "AGENTS.md"), blob)
}
func verify(ctx base.StageContext) error {
	root, err := stagedRoot(ctx)
	if err != nil {
		return err
	}
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return fmt.Errorf("opencode staging missing %s", root)
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

func writeConfig(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}
