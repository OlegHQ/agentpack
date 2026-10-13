package mcp

import (
	"sort"
	"strings"
)

// Server is the canonical MCP server definition shared by manifest loading,
// source merging, and harness-specific renderers.
type Server struct {
	Type     *string           `toml:"type,omitempty" json:"type,omitempty"`
	Command  *string           `toml:"command,omitempty" json:"command,omitempty"`
	Args     []string          `toml:"args,omitempty" json:"args,omitempty"`
	Env      map[string]string `toml:"env,omitempty" json:"env,omitempty"`
	URL      *string           `toml:"url,omitempty" json:"url,omitempty"`
	Disabled *bool             `toml:"disabled,omitempty" json:"disabled,omitempty"`
	// Headers are sent with every request to a remote server. Like Env, they
	// may hold credentials and never reach pack.lock.
	Headers map[string]string `toml:"headers,omitempty" json:"headers,omitempty"`
	// Cwd is the directory a local server is started in.
	Cwd *string `toml:"cwd,omitempty" json:"cwd,omitempty"`
	// OAuth holds the client settings of a remote server. Each agent spells
	// them its own way, so the harness writers render them.
	OAuth *OAuth `toml:"oauth,omitempty" json:"-"`
}

// OAuth is the OAuth client a remote MCP server is used with.
type OAuth struct {
	ClientID     string   `toml:"client_id,omitempty"`
	ClientSecret string   `toml:"client_secret,omitempty"`
	CallbackPort int      `toml:"callback_port,omitempty"`
	Scopes       []string `toml:"scopes,omitempty"`
	MetadataURL  string   `toml:"metadata_url,omitempty"`
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
	// Module and CacheKey name the package a plugin's server came from.
	Module   string
	CacheKey string
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

// JSONObject is the server in the JSON shape Claude Code, Cursor and
// Antigravity share: type, command, args, env, url, headers, disabled. Each
// writer adds or renames what its agent spells differently.
func (server Server) JSONObject() map[string]any {
	object := make(map[string]any)
	if server.Type != nil {
		object["type"] = *server.Type
	}
	if server.Command != nil {
		object["command"] = *server.Command
	}
	if len(server.Args) != 0 {
		object["args"] = server.Args
	}
	if len(server.Env) != 0 {
		object["env"] = server.Env
	}
	if server.URL != nil {
		object["url"] = *server.URL
	}
	if len(server.Headers) != 0 && server.IsRemote() {
		object["headers"] = server.Headers
	}
	if server.Disabled != nil {
		object["disabled"] = *server.Disabled
	}
	return object
}

// NotCarried names, for a warning, the settings of a server that an agent's
// MCP format has no place for.
func NotCarried(agent, name string, settings ...string) string {
	return "MCP server " + name + ": " + agent + " has no setting for " + strings.Join(settings, ", ") + "; the server is staged for it without " + map[bool]string{true: "it", false: "them"}[len(settings) == 1]
}
