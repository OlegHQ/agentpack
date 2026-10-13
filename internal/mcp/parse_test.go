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
	servers, warnings, err := ParseServers([]byte(`{"mcpServers":{"github":{"type":"http","url":"https://api.example.test/mcp","headers":{"Authorization":"Bearer ${TOKEN}"},"oauth":{"clientId":"x"}},"plain":{"command":"run","args":["a"],"env":{"K":"V"},"disabled":false}}}`))
	if err != nil || len(servers) != 2 {
		t.Fatalf("servers = %#v, %v", servers, err)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], `field(s) headers, oauth of MCP server "github"`) {
		t.Fatalf("warnings = %v", warnings)
	}
}
