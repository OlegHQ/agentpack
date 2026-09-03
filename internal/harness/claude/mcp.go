package claude

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/OlegHQ/agentpack/internal/mcp"
)

func WriteMCP(path string, entries mcp.Entries) error {
	servers := make(map[string]mcp.Server)
	for name, entry := range entries {
		server := entry.Server
		if server.Disabled != nil && *server.Disabled {
			continue
		}
		server.Disabled = nil
		if server.IsRemote() && server.Type == nil {
			kind := "http"
			server.Type = &kind
		}
		servers[name] = server
	}
	return writeJSON(path, mcp.Config{Servers: servers})
}

func writeJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("encode %s: %w", path, err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}
