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
	servers, warnings, err := ParseServers([]byte(`{"good":{"command":"run"},"note":"text","empty":{},"args":{"command":"x","args":"oops"}}`))
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
	if _, _, err := ParseServers([]byte("{")); err == nil {
		t.Fatal("malformed document accepted")
	}
}
