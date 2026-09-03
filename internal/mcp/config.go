package mcp

import "sort"

// Server is the canonical MCP server definition shared by manifest loading,
// source merging, and harness-specific renderers.
type Server struct {
	Type     *string           `toml:"type,omitempty" json:"type,omitempty"`
	Command  *string           `toml:"command,omitempty" json:"command,omitempty"`
	Args     []string          `toml:"args,omitempty" json:"args,omitempty"`
	Env      map[string]string `toml:"env,omitempty" json:"env,omitempty"`
	URL      *string           `toml:"url,omitempty" json:"url,omitempty"`
	Disabled *bool             `toml:"disabled,omitempty" json:"disabled,omitempty"`
}

func (server Server) IsRemote() bool { return server.URL != nil && server.Command == nil }

type Config struct {
	Servers map[string]Server `json:"mcpServers"`
}

type Source string

const (
	Plugin    Source = "plugin"
	Manifest  Source = "manifest"
	DotAgents Source = ".agents"
)

type Entry struct {
	Server Server
	Source Source
}

type Entries map[string]Entry

func (entries Entries) Names() []string {
	names := make([]string, 0, len(entries))
	for name := range entries {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (entries Entries) Bare() map[string]Server {
	servers := make(map[string]Server, len(entries))
	for name, entry := range entries {
		servers[name] = entry.Server
	}
	return servers
}
