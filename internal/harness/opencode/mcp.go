package opencode

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/OlegHQ/agentpack/internal/mcp"
	"github.com/tailscale/hujson"
)

func MergeMCP(path string, entries mcp.Entries) error {
	root := map[string]any{"$schema": "https://opencode.ai/config.json"}
	if data, err := os.ReadFile(path); err == nil {
		standard, err := hujson.Standardize(append(data, '\n'))
		if err != nil {
			return fmt.Errorf("parse %s: %w", path, err)
		}
		if err := json.Unmarshal(standard, &root); err != nil {
			return fmt.Errorf("parse %s: %w", path, err)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("read %s: %w", path, err)
	}
	servers, ok := root["mcp"].(map[string]any)
	if root["mcp"] != nil && !ok {
		return fmt.Errorf("%s: mcp must be a JSON object", path)
	}
	if servers == nil {
		servers = make(map[string]any)
		root["mcp"] = servers
	}
	for _, name := range entries.Names() {
		if _, exists := servers[name]; exists {
			continue
		}
		server := entries[name].Server
		value := map[string]any{"enabled": server.Disabled == nil || !*server.Disabled}
		if server.IsRemote() {
			value["type"], value["url"] = "remote", *server.URL
		} else {
			value["type"] = "local"
			command := append([]string(nil), server.Args...)
			if server.Command != nil {
				command = append([]string{*server.Command}, command...)
			}
			value["command"] = command
			if len(server.Env) != 0 {
				value["environment"] = server.Env
			}
		}
		servers[name] = value
	}
	data, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return fmt.Errorf("encode %s: %w", path, err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}
