package mcp

import (
	"strings"
	"testing"
)

func TestParseServersAcceptsWrappedAndBareMaps(t *testing.T) {
	t.Parallel()
	for name, raw := range map[string]string{
		"wrapped": `{"mcpServers":{"playwright":{"command":"npx","args":["@playwright/mcp@latest"]}}}`,
		"bare":    `{"playwright":{"command":"npx","args":["@playwright/mcp@latest"]}} // JSONC`,
	} {
		servers, warnings, err := ParseServers([]byte(raw))
		if err != nil || len(warnings) != 0 {
			t.Fatalf("%s: %v, %v", name, err, warnings)
		}
		server, found := servers["playwright"]
		if !found || server.Command == nil || *server.Command != "npx" || len(server.Args) != 1 {
			t.Fatalf("%s: servers = %#v", name, servers)
		}
	}
	remote, _, err := ParseServers([]byte(`{"gitlab":{"type":"http","url":"https://gitlab.com/api/v4/mcp"}}`))
	if err != nil || !remote["gitlab"].IsRemote() {
		t.Fatalf("remote = %#v, %v", remote, err)
	}
}

func TestParseServersSkipsOnlyTheUnreadableServer(t *testing.T) {
	t.Parallel()
	servers, warnings, err := ParseServers([]byte(`{"$schema":"https://example.test/mcp.schema.json","good":{"command":"run"},"note":"text","empty":{},"args":{"command":"x","args":"oops"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(servers) != 1 || servers["good"].Command == nil {
		t.Fatalf("servers = %#v", servers)
	}
	joined := strings.Join(warnings, "\n")
	for _, name := range []string{`"note"`, `"empty"`, `"args"`} {
		if !strings.Contains(joined, name) {
			t.Fatalf("warnings do not name %s: %v", name, warnings)
		}
	}
	if len(warnings) != 3 {
		t.Fatalf("warnings = %v", warnings)
	}
	if _, _, err := ParseServers([]byte("{")); err == nil {
		t.Fatal("malformed document accepted")
	}
}

func TestParseServersNamesTheFieldsItCannotCarryOver(t *testing.T) {
	t.Parallel()
	servers, warnings, err := ParseServers([]byte(`{"mcpServers":{"github":{"type":"http","url":"https://api.example.test/mcp","headersHelper":"print-headers","envFile":".env"},"plain":{"command":"run","args":["a"],"env":{"K":"V"},"disabled":false}}}`))
	if err != nil || len(servers) != 2 {
		t.Fatalf("servers = %#v, %v", servers, err)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], `field(s) envFile, headersHelper of MCP server "github"`) {
		t.Fatalf("warnings = %v", warnings)
	}
}

// Headers, a working directory and OAuth client settings are part of a
// server; each agent's writer spells them its own way.
func TestParseServersReadsHeadersCwdAndEveryOAuthSpelling(t *testing.T) {
	t.Parallel()
	servers, warnings, err := ParseServers([]byte(`{"mcpServers":{
		"github":{"type":"http","url":"https://api.githubcopilot.com/mcp/","headers":{"Authorization":"Bearer ${GITHUB_PERSONAL_ACCESS_TOKEN}"}},
		"claude":{"url":"https://a.test/mcp","oauth":{"clientId":"c","callbackPort":8080,"authServerMetadataUrl":"https://a.test/.well-known","scopes":"read write"}},
		"codex":{"url":"https://b.test/mcp","scopes":["mail.read"],"oauth":{"client_id":"x","client_secret":"s","callback_port":12798}},
		"cursor":{"url":"https://c.test/mcp","auth":{"CLIENT_ID":"y","CLIENT_SECRET":"t","scopes":["a","b"]}},
		"local":{"command":"node","args":["./start.mjs"],"cwd":".","default_tools_approval_mode":"approve"}}}`))
	if err != nil {
		t.Fatal(err)
	}
	if servers["github"].Headers["Authorization"] != "Bearer ${GITHUB_PERSONAL_ACCESS_TOKEN}" || *servers["local"].Cwd != "." {
		t.Fatalf("servers = %#v", servers)
	}
	for name, want := range map[string]OAuth{
		"claude": {ClientID: "c", CallbackPort: 8080, MetadataURL: "https://a.test/.well-known", Scopes: []string{"read", "write"}},
		"codex":  {ClientID: "x", ClientSecret: "s", CallbackPort: 12798, Scopes: []string{"mail.read"}},
		"cursor": {ClientID: "y", ClientSecret: "t", Scopes: []string{"a", "b"}},
	} {
		got := servers[name].OAuth
		if got == nil || got.ClientID != want.ClientID || got.ClientSecret != want.ClientSecret || got.CallbackPort != want.CallbackPort || got.MetadataURL != want.MetadataURL || strings.Join(got.Scopes, " ") != strings.Join(want.Scopes, " ") {
			t.Fatalf("%s oauth = %#v, want %#v", name, got, want)
		}
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], `default_tools_approval_mode of MCP server "local"`) {
		t.Fatalf("warnings = %v", warnings)
	}
	// Credentials are not part of what pack.lock records; the working directory is.
	plain, withSecrets, moved := servers["codex"], servers["codex"], servers["local"]
	withSecrets.Headers, withSecrets.OAuth = map[string]string{"X": "secret"}, &OAuth{ClientSecret: "other"}
	elsewhere := "/elsewhere"
	moved.Cwd = &elsewhere
	if plain.Definition() != withSecrets.Definition() || moved.Definition() == servers["local"].Definition() {
		t.Fatal("definition digest covers the wrong fields")
	}
}
