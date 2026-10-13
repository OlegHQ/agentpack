package mcp

import (
	"encoding/json"
	"fmt"
	"sort"

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
		var server Server
		if err := json.Unmarshal(raw[name], &server); err != nil {
			warnings = append(warnings, fmt.Sprintf("MCP server %q is not a server definition (%v)", name, err))
			continue
		}
		if server.Command == nil && server.URL == nil {
			warnings = append(warnings, fmt.Sprintf("MCP server %q has neither command nor url", name))
			continue
		}
		servers[name] = server
	}
	return servers, warnings
}
