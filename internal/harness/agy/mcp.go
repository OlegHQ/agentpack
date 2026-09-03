package agy

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/OlegHQ/agentpack/internal/mcp"
)

func WriteMCP(path string, entries mcp.Entries) error {
	servers := make(map[string]map[string]any, len(entries))
	for name, entry := range entries {
		server, value := entry.Server, make(map[string]any)
		if server.IsRemote() {
			value["serverUrl"] = *server.URL
		} else {
			if server.Command != nil {
				value["command"] = *server.Command
			}
			if len(server.Args) != 0 {
				value["args"] = server.Args
			}
			if len(server.Env) != 0 {
				value["env"] = server.Env
			}
		}
		if server.Disabled != nil {
			value["disabled"] = *server.Disabled
		}
		servers[name] = value
	}
	data, err := json.MarshalIndent(map[string]any{"mcpServers": servers}, "", "  ")
	if err != nil {
		return fmt.Errorf("encode %s: %w", path, err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}
