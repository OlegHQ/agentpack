package mcp

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/tailscale/hujson"
)

// ParseServers reads the MCP servers of a JSON (or JSONC) document in either
// published form: wrapped in "mcpServers", or the bare name-to-server map that
// Claude Code also accepts in a plugin's .mcp.json. A server that cannot be
// read is left out and named in the returned warnings; the others are kept.
func ParseServers(data []byte) (map[string]Server, []string, error) {
	standard, err := hujson.Standardize(append(append([]byte(nil), data...), '\n'))
	if err != nil {
		return nil, nil, err
	}
	var document map[string]json.RawMessage
	if err := json.Unmarshal(standard, &document); err != nil {
		return nil, nil, err
	}
	if wrapped, found := document["mcpServers"]; found {
		var inner map[string]json.RawMessage
		if err := json.Unmarshal(wrapped, &inner); err != nil {
			return nil, nil, fmt.Errorf("mcpServers must be an object of servers: %w", err)
		}
		servers, warnings := DecodeServers(inner)
		return servers, warnings, nil
	}
	servers, warnings := DecodeServers(document)
	return servers, warnings, nil
}

// DecodeServers decodes a name-to-server map one server at a time.
func DecodeServers(raw map[string]json.RawMessage) (map[string]Server, []string) {
	servers := make(map[string]Server, len(raw))
	var warnings []string
	names := make([]string, 0, len(raw))
	for name := range raw {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if strings.HasPrefix(name, "$") {
			// "$schema" and the like, next to the servers of a bare map.
			continue
		}
		var server Server
		if err := json.Unmarshal(raw[name], &server); err != nil {
			warnings = append(warnings, fmt.Sprintf("MCP server %q: it is not a server definition (%v)", name, err))
			continue
		}
		if server.Command == nil && server.URL == nil {
			warnings = append(warnings, fmt.Sprintf("MCP server %q: it has neither command nor url", name))
			continue
		}
		var fields map[string]json.RawMessage
		if json.Unmarshal(raw[name], &fields) == nil {
			server.OAuth = decodeOAuth(fields)
		}
		servers[name] = server
		if dropped := unsupportedFields(raw[name]); len(dropped) != 0 {
			warnings = append(warnings, fmt.Sprintf("field(s) %s of MCP server %q, which agentpack does not carry over; the server is staged without them", strings.Join(dropped, ", "), name))
		}
	}
	return servers, warnings
}

// unsupportedFields names the fields of a server definition that Server has
// no place for, so that leaving them out is said.
func unsupportedFields(raw json.RawMessage) []string {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return nil
	}
	var dropped []string
	for field := range fields {
		switch field {
		case "type", "command", "args", "env", "url", "disabled", "headers", "cwd", "oauth", "auth", "scopes":
		default:
			dropped = append(dropped, field)
		}
	}
	sort.Strings(dropped)
	return dropped
}

// decodeOAuth reads a server's OAuth client settings in any of the published
// spellings: Claude Code's oauth {clientId, callbackPort,
// authServerMetadataUrl}, Codex's oauth {client_id, client_secret,
// callback_port}, OpenCode's oauth {clientId, clientSecret, scope}, Cursor's
// auth {CLIENT_ID, CLIENT_SECRET, scopes}, and a scopes list next to them.
func decodeOAuth(fields map[string]json.RawMessage) *OAuth {
	var oauth OAuth
	found := false
	for _, key := range []string{"oauth", "auth"} {
		var object map[string]any
		if json.Unmarshal(fields[key], &object) != nil {
			continue
		}
		for name, value := range object {
			switch strings.ToLower(strings.ReplaceAll(name, "_", "")) {
			case "clientid":
				oauth.ClientID, _ = value.(string)
			case "clientsecret":
				oauth.ClientSecret, _ = value.(string)
			case "callbackport":
				if port, ok := value.(float64); ok {
					oauth.CallbackPort = int(port)
				}
			case "scope", "scopes":
				oauth.Scopes = scopeList(value)
			case "authservermetadataurl", "metadataurl":
				oauth.MetadataURL, _ = value.(string)
			default:
				continue
			}
			found = true
		}
	}
	if raw, present := fields["scopes"]; present {
		var value any
		if json.Unmarshal(raw, &value) == nil {
			if scopes := scopeList(value); len(scopes) != 0 {
				oauth.Scopes, found = scopes, true
			}
		}
	}
	if !found {
		return nil
	}
	return &oauth
}

func scopeList(value any) []string {
	switch typed := value.(type) {
	case string:
		return strings.Fields(typed)
	case []any:
		var scopes []string
		for _, item := range typed {
			if text, ok := item.(string); ok {
				scopes = append(scopes, text)
			}
		}
		return scopes
	}
	return nil
}
