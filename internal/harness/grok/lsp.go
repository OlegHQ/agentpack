package grok

import (
	"encoding/json"
	"path/filepath"

	base "github.com/OlegHQ/agentpack/internal/harness"
	"github.com/OlegHQ/agentpack/internal/paths"
)

// writeLSP writes $GROK_HOME/lsp.json, Grok's user-level language server
// file. A plugin's .lsp.json is read only once the plugin is trusted, and a
// project's .grok/lsp.json only in a trusted folder; the home file needs
// neither.
func writeLSP(servers map[string]json.RawMessage, ctx base.StageContext) error {
	home, err := paths.StagingGrokHomeDirForMode(ctx.ProjectRoot, ctx.Mode.Name())
	if err != nil {
		return err
	}
	return base.WriteLSPFile(filepath.Join(home, "lsp.json"), servers)
}
