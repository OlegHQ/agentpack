package claude

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/OlegHQ/agentpack/internal/paths"
)

func KeepAttribution() bool {
	switch strings.ToLower(os.Getenv("AGENTPACK_KEEP_ATTRIBUTION")) {
	case "1", "true", "yes":
		return true
	default:
		return false
	}
}

func MaterializeSettings() error {
	path, err := paths.AgentpackClaudeSettingsPath()
	if err != nil {
		return err
	}
	if KeepAttribution() {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	return writeSettings(path, map[string]any{"includeCoAuthoredBy": false, "attribution": map[string]any{"commit": "", "pr": ""}})
}

func SetMCPAllowlist(names []string) error {
	path, err := paths.AgentpackClaudeSettingsPath()
	if err != nil {
		return err
	}
	settings := make(map[string]any)
	if data, readErr := os.ReadFile(path); readErr == nil {
		if err := json.Unmarshal(data, &settings); err != nil {
			return fmt.Errorf("parse %s: %w", path, err)
		}
	} else if !os.IsNotExist(readErr) {
		return readErr
	}
	settings["enabledMcpjsonServers"] = names
	return writeSettings(path, settings)
}

func writeSettings(path string, settings map[string]any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}
