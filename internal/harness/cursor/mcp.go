package cursor

import (
	"encoding/json"
	"fmt"
	"log"
	"os"

	"github.com/OlegHQ/agentpack/internal/mcp"
)

// WriteMCP writes the servers as Cursor's mcp.json. A remote server keeps
// its headers and its OAuth client, which Cursor spells auth.CLIENT_ID,
// auth.CLIENT_SECRET and auth.scopes. Cursor documents no working directory
// for a stdio server and a fixed OAuth callback, so a cwd and a callback port
// are left out with a warning.
func WriteMCP(path string, entries mcp.Entries) error {
	servers := make(map[string]map[string]any, len(entries))
	for _, name := range entries.Names() {
		server := entries[name].Server
		object := server.JSONObject()
		var missing []string
		if server.Cwd != nil && !server.IsRemote() {
			missing = append(missing, "a working directory (cwd)")
		}
		if oauth := server.OAuth; oauth != nil && server.IsRemote() {
			if oauth.ClientID != "" {
				value := map[string]any{"CLIENT_ID": oauth.ClientID}
				if oauth.ClientSecret != "" {
					value["CLIENT_SECRET"] = oauth.ClientSecret
				}
				if len(oauth.Scopes) != 0 {
					value["scopes"] = oauth.Scopes
				}
				object["auth"] = value
			} else if len(oauth.Scopes) != 0 {
				missing = append(missing, "OAuth scopes without a client id")
			}
			if oauth.CallbackPort != 0 {
				missing = append(missing, "an OAuth callback port")
			}
			if oauth.MetadataURL != "" {
				missing = append(missing, "an OAuth metadata URL")
			}
		}
		if len(missing) != 0 {
			log.Printf("warning: %s", mcp.NotCarried("Cursor", name, missing...))
		}
		servers[name] = object
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
