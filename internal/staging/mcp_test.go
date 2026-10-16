package staging

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/olegpustovit/agentpack/internal/cache"

	"github.com/olegpustovit/agentpack/internal/lockfile"
	"github.com/olegpustovit/agentpack/internal/manifest"
	"github.com/olegpustovit/agentpack/internal/mcp"
)

func TestCollectMCPPrecedenceAndJSONC(t *testing.T) {
	project := t.TempDir()
	if err := os.Mkdir(filepath.Join(project, ".agents"), 0o755); err != nil {
		t.Fatal(err)
	}
	dot := `{"mcpServers":{"shared":{"command":"dot"},"url":{"url":"https://example.test/mcp"}}} // comment`
	if err := os.WriteFile(filepath.Join(project, ".agents", "mcp.json"), []byte(dot), 0o644); err != nil {
		t.Fatal(err)
	}
	manifestCommand := "manifest"
	projectManifest := &manifest.Manifest{MCP: manifest.MCPSection{Servers: map[string]mcp.Server{"shared": {Command: &manifestCommand}}}}
	entries, err := CollectMCP(project, lockfile.PackLock{}, projectManifest, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := *entries["shared"].Server.Command; got != "dot" {
		t.Fatalf("precedence command = %q", got)
	}
	if entries["shared"].Source != mcp.DotAgents || entries["url"].Source != mcp.DotAgents {
		t.Fatalf("wrong provenance: %#v", entries)
	}
}

func TestCollectMCPSkipsMalformedDotAgentsFile(t *testing.T) {
	project := t.TempDir()
	if err := os.Mkdir(filepath.Join(project, ".agents"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, ".agents", "mcp.json"), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	entries, err := CollectMCP(project, lockfile.PackLock{}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("entries = %#v", entries)
	}
}

func TestCollectMCPReadsEveryPublishedPluginLocation(t *testing.T) {
	t.Setenv("AGENTPACK_HOME", t.TempDir())
	write := func(key, relative, body string) {
		t.Helper()
		root, err := cache.EntryDir(key)
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(root, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// The bare map of the official playwright, firebase, gitlab and laravel-boost plugins.
	write("a-bare", ".claude-plugin/plugin.json", `{"name":"playwright"}`)
	write("a-bare", ".mcp.json", `{"playwright":{"command":"npx","args":["@playwright/mcp@latest"]},"gitlab":{"type":"http","url":"https://gitlab.com/api/v4/mcp"}}`)
	// Inline in plugin.json, and a file the Codex manifest names; Claude Code's definition wins.
	write("b-inline", ".claude-plugin/plugin.json", `{"name":"context-mode","mcpServers":{"context-mode":{"command":"node","args":["${CLAUDE_PLUGIN_ROOT}/start.mjs"]}}}`)
	write("b-inline", ".cursor-plugin/plugin.json", `{"name":"context-mode","mcpServers":{"context-mode":{"command":"npx","args":["-y","context-mode"]},"cursor-only":{"url":"https://example.test/mcp"}}}`)
	write("b-inline", ".codex-plugin/plugin.json", `{"name":"context-mode","mcpServers":"./.codex-plugin/mcp.json"}`)
	write("b-inline", ".codex-plugin/mcp.json", `{"mcpServers":{"context-mode":{"command":"node","args":["./start.mjs"]},"codex-only":{"command":"codex-server"}}}`)
	// A declaration that leaves the package is skipped; the rest of the package still loads.
	write("c-escape", ".claude-plugin/plugin.json", `{"name":"escape","mcpServers":["../b-inline/.codex-plugin/mcp.json","./servers.json","https://example.test/bundle.mcpb"]}`)
	write("c-escape", "servers.json", `{"kept":{"command":"kept"},"broken":{"args":["no command"]}}`)
	lock := lockfile.PackLock{Packages: []lockfile.Package{
		{Module: "github.com/o/bare", Kind: lockfile.PackagePlugin, CacheKey: "a-bare"},
		{Module: "github.com/o/inline", Kind: lockfile.PackagePlugin, CacheKey: "b-inline"},
		{Module: "github.com/o/escape", Kind: lockfile.PackagePlugin, CacheKey: "c-escape"},
	}}
	entries, err := CollectMCP(t.TempDir(), lock, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	names := entries.Names()
	if want := "[codex-only context-mode cursor-only gitlab kept playwright]"; fmt.Sprint(names) != want {
		t.Fatalf("servers = %v, want %s", names, want)
	}
	if !entries["gitlab"].Server.IsRemote() || *entries["playwright"].Server.Command != "npx" {
		t.Fatalf("bare map entries = %#v", entries)
	}
	winner := entries["context-mode"]
	if *winner.Server.Command != "node" || winner.Server.Args[0] != "${CLAUDE_PLUGIN_ROOT}/start.mjs" || winner.Module != "github.com/o/inline" || winner.CacheKey != "b-inline" || winner.Source != mcp.Plugin {
		t.Fatalf("context-mode = %#v", winner)
	}
}
