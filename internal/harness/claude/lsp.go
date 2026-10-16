package claude

import (
	"encoding/json"
	"path/filepath"

	base "github.com/olegpustovit/agentpack/internal/harness"
)

// writeLSP puts the language servers in .lsp.json at the bundle root, where
// Claude Code reads a plugin's language servers.
func writeLSP(servers map[string]json.RawMessage, ctx base.StageContext) error {
	root, err := stagedRoot(ctx)
	if err != nil {
		return err
	}
	return base.WriteLSPFile(filepath.Join(root, ".lsp.json"), servers)
}
