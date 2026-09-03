package mcp

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
