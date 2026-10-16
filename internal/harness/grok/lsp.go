package grok

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	base "github.com/olegpustovit/agentpack/internal/harness"
	"github.com/olegpustovit/agentpack/internal/paths"
	"github.com/pelletier/go-toml/v2"
)

// writeLSP writes $GROK_HOME/lsp.json, Grok's user-level language server
// file. A plugin's .lsp.json is read only once the plugin is trusted, and a
// project's .grok/lsp.json only in a trusted folder; the home file needs
// neither. Grok gives its model the lsp tool only with [features]
// lsp_tools = true (its README, "LSP"), which Claude Code does unasked, so
// that is set too unless the user's configuration says otherwise.
func writeLSP(servers map[string]json.RawMessage, ctx base.StageContext) error {
	home, err := paths.StagingGrokHomeDirForMode(ctx.ProjectRoot, ctx.Mode.Name())
	if err != nil {
		return err
	}
	if err := base.WriteLSPFile(filepath.Join(home, "lsp.json"), servers); err != nil {
		return err
	}
	return enableLSPTools(filepath.Join(home, "config.toml"))
}

func enableLSPTools(path string) error {
	root := make(map[string]any)
	if data, err := os.ReadFile(path); err == nil {
		if err := toml.Unmarshal(data, &root); err != nil {
			return fmt.Errorf("parse %s: %w", path, err)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("read %s: %w", path, err)
	}
	features, ok := root["features"].(map[string]any)
	if root["features"] != nil && !ok {
		return fmt.Errorf("%s: features must be a TOML table", path)
	}
	if features == nil {
		features = make(map[string]any)
		root["features"] = features
	}
	if _, set := features["lsp_tools"]; set {
		return nil
	}
	features["lsp_tools"] = true
	data, err := toml.Marshal(root)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}
