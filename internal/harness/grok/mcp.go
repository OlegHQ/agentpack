package grok

import (
	"fmt"
	"os"

	"github.com/OlegHQ/agentpack/internal/mcp"
	"github.com/pelletier/go-toml/v2"
)

// MergeMCP is deliberately harness-owned even though Codex currently uses the same wire format.
func MergeMCP(path string, entries mcp.Entries) error {
	root := make(map[string]any)
	if data, err := os.ReadFile(path); err == nil {
		if err := toml.Unmarshal(data, &root); err != nil {
			return fmt.Errorf("parse %s: %w", path, err)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("read %s: %w", path, err)
	}
	servers, ok := root["mcp_servers"].(map[string]any)
	if root["mcp_servers"] != nil && !ok {
		return fmt.Errorf("%s: mcp_servers must be a TOML table", path)
	}
	if servers == nil {
		servers = make(map[string]any)
		root["mcp_servers"] = servers
	}
	for _, name := range entries.Names() {
		if _, exists := servers[name]; exists {
			continue
		}
		server := entries[name].Server
		value := make(map[string]any)
		if server.IsRemote() {
			value["url"] = *server.URL
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
		if server.Disabled != nil && *server.Disabled {
			value["enabled"] = false
		}
		servers[name] = value
	}
	data, err := toml.Marshal(root)
	if err != nil {
		return fmt.Errorf("encode %s: %w", path, err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}
