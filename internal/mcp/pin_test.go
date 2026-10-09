package mcp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/OlegHQ/agentpack/internal/lockfile"
)

func TestDescribeClassifiesLaunchersWithoutPretending(t *testing.T) {
	t.Parallel()
	command := func(program string, args ...string) Server { return Server{Command: &program, Args: args} }
	remote := "https://mcp.linear.app/mcp"
	for name, test := range map[string]struct {
		server Server
		want   lockfile.MCPServer
	}{
		"npx dist-tag":      {command("npx", "@playwright/mcp@latest"), lockfile.MCPServer{Launcher: LauncherNPM, Requested: "@playwright/mcp@latest", Package: "@playwright/mcp"}},
		"npx flags no tag":  {command("npx", "-y", "@modelcontextprotocol/server-filesystem", "/tmp"), lockfile.MCPServer{Launcher: LauncherNPM, Requested: "@modelcontextprotocol/server-filesystem", Package: "@modelcontextprotocol/server-filesystem"}},
		"npx.cmd exact":     {command(`C:\node\npx.cmd`, "--yes", "left-pad@1.3.0"), lockfile.MCPServer{Launcher: LauncherNPM, Requested: "left-pad@1.3.0", Package: "left-pad"}},
		"bunx":              {command("bunx", "some-mcp@next"), lockfile.MCPServer{Launcher: LauncherNPM, Requested: "some-mcp@next", Package: "some-mcp"}},
		"pnpm dlx":          {command("pnpm", "dlx", "some-mcp"), lockfile.MCPServer{Launcher: LauncherNPM, Requested: "some-mcp", Package: "some-mcp"}},
		"npx range":         {command("npx", "some-mcp@^1.2"), lockfile.MCPServer{Launcher: LauncherNPM, Status: lockfile.MCPUnpinned, Requested: "some-mcp@^1.2", Package: "some-mcp"}},
		"npx git spec":      {command("npx", "github:acme/mcp"), lockfile.MCPServer{Launcher: LauncherNPM, Status: lockfile.MCPUnpinned, Requested: "github:acme/mcp"}},
		"npx --package":     {command("npx", "--package", "some-mcp", "serve"), lockfile.MCPServer{Launcher: LauncherNPM, Status: lockfile.MCPUnpinned}},
		"uvx":               {command("uvx", "mcp-retrieval"), lockfile.MCPServer{Launcher: LauncherPyPI, Status: lockfile.MCPUnpinned, Requested: "mcp-retrieval", Package: "mcp-retrieval"}},
		"uvx exact":         {command("uvx", "mcp-retrieval==1.4.0"), lockfile.MCPServer{Launcher: LauncherPyPI, Status: lockfile.MCPUnpinned, Requested: "mcp-retrieval==1.4.0", Package: "mcp-retrieval", Version: "1.4.0"}},
		"uvx --from":        {command("uvx", "--from", "git+https://example.test/x", "serve"), lockfile.MCPServer{Launcher: LauncherPyPI, Status: lockfile.MCPUnpinned, Requested: "git+https://example.test/x"}},
		"uvx unknown flags": {command("uvx", "--python", "3.12", "mcp-retrieval"), lockfile.MCPServer{Launcher: LauncherPyPI, Status: lockfile.MCPUnpinned}},
		"uv run script":     {command("uv", "run", "server.py"), lockfile.MCPServer{Launcher: LauncherCommand, Status: lockfile.MCPUnpinnable, Command: "uv"}},
		"docker tag":        {command("docker", "run", "-i", "--rm", "-e", "TOKEN", "ghcr.io/acme/mcp:1"), lockfile.MCPServer{Launcher: LauncherDocker, Status: lockfile.MCPUnpinned, Requested: "ghcr.io/acme/mcp:1"}},
		"docker digest":     {command("docker", "run", "-i", "ghcr.io/acme/mcp@sha256:abc"), lockfile.MCPServer{Launcher: LauncherDocker, Status: lockfile.MCPPinned, Requested: "ghcr.io/acme/mcp@sha256:abc", Package: "ghcr.io/acme/mcp", Integrity: "sha256:abc"}},
		"docker odd flags":  {command("docker", "run", "--gpus", "all", "ghcr.io/acme/mcp:1"), lockfile.MCPServer{Launcher: LauncherDocker, Status: lockfile.MCPUnpinned}},
		"remote":            {Server{URL: &remote}, lockfile.MCPServer{Launcher: LauncherRemote, Status: lockfile.MCPUnpinnable, Host: "mcp.linear.app"}},
		"local command":     {command("/opt/tools/server", "--stdio"), lockfile.MCPServer{Launcher: LauncherCommand, Status: lockfile.MCPUnpinnable, Command: "/opt/tools/server"}},
	} {
		got := Describe("server", Entry{Server: test.server, Source: Manifest})
		if got.Name != "server" || got.Source != "manifest" || got.Definition != test.server.Definition() {
			t.Errorf("%s: identity fields = %#v", name, got)
		}
		if (got.Status == "") != (got.Reason == "") && got.Status != lockfile.MCPPinned {
			t.Errorf("%s: status %q without a reason %q", name, got.Status, got.Reason)
		}
		got.Name, got.Source, got.Definition, got.Reason = "", "", "", ""
		if got != test.want {
			t.Errorf("%s:\n got %#v\nwant %#v", name, got, test.want)
		}
	}
}

func TestDefinitionCoversWhatRunsButNotEnvironment(t *testing.T) {
	t.Parallel()
	npx := "npx"
	base := Server{Command: &npx, Args: []string{"-y", "some-mcp"}, Env: map[string]string{"API_KEY": "one"}}
	rotated := base
	rotated.Env = map[string]string{"API_KEY": "two"}
	if base.Definition() != rotated.Definition() {
		t.Fatal("environment values changed the definition digest")
	}
	changed := base
	changed.Args = []string{"-y", "some-mcp@2"}
	if base.Definition() == changed.Definition() {
		t.Fatal("argument change did not change the definition digest")
	}
	if (Server{Command: &npx, Args: []string{"ab"}}).Definition() == (Server{Command: &npx, Args: []string{"a", "b"}}).Definition() {
		t.Fatal("argument boundaries are not part of the digest")
	}
}

func TestResolveNPMReadsVersionAndIntegrity(t *testing.T) {
	var requested []string
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		requested = append(requested, request.URL.EscapedPath())
		switch request.URL.EscapedPath() {
		case "/@playwright%2Fmcp/latest":
			_, _ = response.Write([]byte(`{"version":"0.0.41","dist":{"integrity":"sha512-abc","shasum":"00ff"}}`))
		case "/old-package/1.0.0":
			_, _ = response.Write([]byte(`{"version":"1.0.0","dist":{"shasum":"00ff"}}`))
		case "/no-version/latest":
			_, _ = response.Write([]byte(`{"dist":{"integrity":"sha512-abc"}}`))
		default:
			response.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	version, integrity, err := ResolveNPM(context.Background(), server.Client(), server.URL+"/", "@playwright/mcp", "latest")
	if err != nil || version != "0.0.41" || integrity != "sha512-abc" {
		t.Fatalf("ResolveNPM(dist-tag) = %q, %q, %v; requests=%v", version, integrity, err, requested)
	}
	if version, integrity, err = ResolveNPM(context.Background(), server.Client(), server.URL, "old-package", "1.0.0"); err != nil || version != "1.0.0" || integrity != "sha1-AP8=" {
		t.Fatalf("ResolveNPM(shasum only) = %q, %q, %v", version, integrity, err)
	}
	if _, _, err = ResolveNPM(context.Background(), server.Client(), server.URL, "missing", "latest"); err == nil || !strings.Contains(err.Error(), "has no missing@latest") {
		t.Fatalf("ResolveNPM(missing) error = %v", err)
	}
	if _, _, err = ResolveNPM(context.Background(), server.Client(), server.URL, "no-version", "latest"); err == nil {
		t.Fatal("ResolveNPM accepted an answer without a version")
	}
	server.Close()
	if _, _, err = ResolveNPM(context.Background(), server.Client(), server.URL, "@playwright/mcp", "latest"); err == nil || !strings.Contains(err.Error(), "unreachable") {
		t.Fatalf("ResolveNPM(closed registry) error = %v", err)
	}
}

func TestApplyPinsRewritesOnlyCurrentPinnedNPMServers(t *testing.T) {
	t.Parallel()
	npx := "npx"
	server := Server{Command: &npx, Args: []string{"-y", "@playwright/mcp@latest", "--headless"}}
	entries := Entries{"playwright": {Server: server, Source: Plugin}}
	record := lockfile.MCPServer{Name: "playwright", Launcher: LauncherNPM, Status: lockfile.MCPPinned, Definition: server.Definition(), Requested: "@playwright/mcp@latest", Package: "@playwright/mcp", Version: "0.0.41"}
	pinned := ApplyPins(entries, []lockfile.MCPServer{record})
	if got := strings.Join(pinned["playwright"].Server.Args, " "); got != "-y @playwright/mcp@0.0.41 --headless" {
		t.Fatalf("pinned args = %q", got)
	}
	if server.Args[1] != "@playwright/mcp@latest" {
		t.Fatal("ApplyPins mutated the collected definition")
	}
	stale, unpinned := record, record
	stale.Definition = "sha256:older-definition"
	unpinned.Status = lockfile.MCPUnpinned
	for name, records := range map[string][]lockfile.MCPServer{"stale": {stale}, "unpinned": {unpinned}, "absent": nil} {
		if got := ApplyPins(entries, records)["playwright"].Server.Args[1]; got != "@playwright/mcp@latest" {
			t.Errorf("%s record rewrote the server to %q", name, got)
		}
	}
	if names := Unrecorded(entries, []lockfile.MCPServer{stale}); len(names) != 1 || names[0] != "playwright" {
		t.Fatalf("Unrecorded(stale) = %v", names)
	}
	if names := Unrecorded(entries, []lockfile.MCPServer{record}); len(names) != 0 {
		t.Fatalf("Unrecorded(current) = %v", names)
	}
}
