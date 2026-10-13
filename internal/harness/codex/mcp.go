package codex

import (
	"fmt"
	"log"
	"os"

	"github.com/OlegHQ/agentpack/internal/mcp"
	"github.com/pelletier/go-toml/v2"
)

func MergeMCP(path string, entries mcp.Entries) error { return mergeMCP(path, entries) }

func mergeMCP(path string, entries mcp.Entries) error {
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
		var missing []string
		if server.IsRemote() {
			value["url"] = *server.URL
			if len(server.Headers) != 0 {
				value["http_headers"] = server.Headers
			}
			// Codex keeps the client under [mcp_servers.<name>.oauth] and the
			// scopes next to it; it has no place for a client secret.
			if oauth := server.OAuth; oauth != nil {
				client := make(map[string]any)
				if oauth.ClientID != "" {
					client["client_id"] = oauth.ClientID
				}
				if oauth.CallbackPort != 0 {
					client["callback_port"] = oauth.CallbackPort
				}
				if len(client) != 0 {
					value["oauth"] = client
				}
				if len(oauth.Scopes) != 0 {
					value["scopes"] = oauth.Scopes
				}
				if oauth.ClientSecret != "" {
					missing = append(missing, "an OAuth client secret")
				}
				if oauth.MetadataURL != "" {
					missing = append(missing, "an OAuth metadata URL")
				}
			}
		} else {
			if server.Cwd != nil {
				value["cwd"] = *server.Cwd
			}
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
		if len(missing) != 0 {
			log.Printf("warning: %s", mcp.NotCarried("Codex", name, missing...))
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
