package staging

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/OlegHQ/agentpack/internal/cache"
	"github.com/OlegHQ/agentpack/internal/lockfile"
	"github.com/OlegHQ/agentpack/internal/manifest"
	"github.com/OlegHQ/agentpack/internal/mcp"
	"github.com/OlegHQ/agentpack/internal/mode"
	"github.com/OlegHQ/agentpack/internal/paths"
)

// CollectMCP applies the canonical precedence: plugins, manifest, then .agents.
func CollectMCP(projectRoot string, lock lockfile.PackLock, projectManifest *manifest.Manifest, effective *mode.Effective) (mcp.Entries, error) {
	merged := make(mcp.Entries)
	plugins := lock.Plugins()
	sort.Slice(plugins, func(i, j int) bool { return plugins[i].CacheKey < plugins[j].CacheKey })
	for _, plugin := range plugins {
		if plugin.CacheKey == "" || disabledPlugin(lock, plugin.CacheKey) {
			continue
		}
		if effective != nil {
			allowed, err := effective.AllowsPackagePath(plugin.Module, "mcp.json")
			if err != nil {
				return nil, err
			}
			if !allowed {
				continue
			}
		}
		root, err := cache.EntryDir(plugin.CacheKey)
		if err != nil {
			continue
		}
		mergePluginMCP(root, plugin, effective, merged)
	}
	if projectManifest != nil {
		for name, server := range projectManifest.MCP.Servers {
			if effective == nil || effective.AllowsMCP(name) {
				merged[name] = mcp.Entry{Server: server, Source: mcp.Manifest}
			}
		}
	}
	dotPath := filepath.Join(paths.ProjectDotAgentsDir(projectRoot), "mcp.json")
	if effective != nil {
		allowed, err := effective.AllowsDotAgentsPath("mcp.json")
		if err != nil {
			return nil, err
		}
		if !allowed {
			return merged, nil
		}
	}
	mergeMCPFile(dotPath, mcp.Entry{Source: mcp.DotAgents}, dotPath, effective, merged)
	return merged, nil
}

func disabledPlugin(lock lockfile.PackLock, key string) bool {
	short := key
	if len(short) > 16 {
		short = short[:16]
	}
	for _, disabled := range lock.Config.DisabledPlugins {
		if disabled == key || disabled == short {
			return true
		}
	}
	return false
}

// mergeMCPFile reads one MCP definition file, in the wrapped or the bare form.
// A file or a server that cannot be read is skipped with a warning naming it.
func mergeMCPFile(path string, origin mcp.Entry, label string, effective *mode.Effective, merged mcp.Entries) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return
	}
	if err != nil {
		log.Printf("warning: skipping MCP servers in %s: %v", label, err)
		return
	}
	servers, warnings, err := mcp.ParseServers(data)
	if err != nil {
		log.Printf("warning: skipping MCP servers in %s: %v", label, err)
		return
	}
	for _, warning := range warnings {
		log.Printf("warning: %s: skipping %s", label, warning)
	}
	mergeServers(servers, origin, effective, merged)
}

func mergeServers(servers map[string]mcp.Server, origin mcp.Entry, effective *mode.Effective, merged mcp.Entries) {
	for name, server := range servers {
		if effective == nil || effective.AllowsMCP(name) {
			origin.Server = server
			merged[name] = origin
		}
	}
}

// mergePluginMCP reads every place a plugin may define MCP servers, as the
// vendors' plugin formats give them: the root mcp.json (Cursor) and .mcp.json
// (Claude Code, Codex), then what each plugin.json declares under mcpServers,
// as a path or inline. Codex is read first, then Cursor, then Claude Code; a
// later definition of the same server name wins.
func mergePluginMCP(root string, plugin lockfile.Package, effective *mode.Effective, merged mcp.Entries) {
	origin := mcp.Entry{Source: mcp.Plugin, Module: plugin.Module, CacheKey: plugin.CacheKey}
	read := make(map[string]bool)
	file := func(path, relative string) {
		resolved, err := filepath.EvalSymlinks(path)
		if err != nil || read[resolved] {
			return
		}
		read[resolved] = true
		mergeMCPFile(resolved, origin, plugin.Module+" "+relative, effective, merged)
	}
	// The cache keeps a copy of .mcp.json as mcp.json; reading the original
	// last lets the Claude Code and Codex file win where a package has both.
	file(filepath.Join(root, "mcp.json"), "mcp.json")
	file(filepath.Join(root, ".mcp.json"), ".mcp.json")
	for _, pluginManifest := range cache.ReadPluginManifests(root) {
		label := plugin.Module + " " + pluginManifest.Relative + " mcpServers"
		declared, inline := pluginManifest.Declared("mcpServers")
		for _, path := range declared {
			if strings.Contains(path, "://") || !strings.EqualFold(filepath.Ext(path), ".json") {
				log.Printf("warning: skipping %s %q: only .json definitions inside the package are read, not bundles or URLs", label, path)
				continue
			}
			resolved, err := cache.ResolveInside(root, path)
			if err != nil {
				log.Printf("warning: skipping %s: %v", label, err)
				continue
			}
			file(resolved, strings.TrimPrefix(path, "./"))
		}
		for _, value := range inline {
			var raw map[string]json.RawMessage
			if err := json.Unmarshal(value, &raw); err != nil {
				log.Printf("warning: skipping %s: it is not an object of servers", label)
				continue
			}
			servers, warnings := mcp.DecodeServers(raw)
			for _, warning := range warnings {
				log.Printf("warning: %s: skipping %s", label, warning)
			}
			mergeServers(servers, origin, effective, merged)
		}
	}
}
