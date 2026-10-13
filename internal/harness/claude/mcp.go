package claude

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/OlegHQ/agentpack/internal/mcp"
)

// WriteMCP writes the servers as Claude Code's .mcp.json. A remote server
// keeps its headers and its OAuth client (oauth.clientId, callbackPort,
// authServerMetadataUrl, scopes). Claude Code documents no working directory
// for a stdio server and takes an OAuth client secret from the keychain, not
// from the file, so those two are left out with a warning.
func WriteMCP(path string, entries mcp.Entries) error {
	servers := make(map[string]map[string]any)
	for _, name := range entries.Names() {
		server := entries[name].Server
		if server.Disabled != nil && *server.Disabled {
			continue
		}
		server.Disabled = nil
		if server.IsRemote() && server.Type == nil {
			kind := "http"
			server.Type = &kind
		}
		object := server.JSONObject()
		var missing []string
		if server.Cwd != nil && !server.IsRemote() {
			missing = append(missing, "a working directory (cwd)")
		}
		if oauth := server.OAuth; oauth != nil && server.IsRemote() {
			value := make(map[string]any)
			if oauth.ClientID != "" {
				value["clientId"] = oauth.ClientID
			}
			if oauth.CallbackPort != 0 {
				value["callbackPort"] = oauth.CallbackPort
			}
			if oauth.MetadataURL != "" {
				value["authServerMetadataUrl"] = oauth.MetadataURL
			}
			if len(oauth.Scopes) != 0 {
				value["scopes"] = strings.Join(oauth.Scopes, " ")
			}
			if len(value) != 0 {
				object["oauth"] = value
			}
			if oauth.ClientSecret != "" {
				missing = append(missing, "an OAuth client secret in the file (set MCP_CLIENT_SECRET or use claude mcp add --client-secret)")
			}
		}
		if len(missing) != 0 {
			log.Printf("warning: %s", mcp.NotCarried("Claude Code", name, missing...))
		}
		servers[name] = object
	}
	return writeJSON(path, map[string]any{"mcpServers": servers})
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
