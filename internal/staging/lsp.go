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
	"github.com/OlegHQ/agentpack/internal/mode"
	"github.com/tailscale/hujson"
)

// CollectLSP gathers the language servers that plugins declare, in Claude
// Code's format (name -> {command, args, extensionToLanguage, ...}): a
// .lsp.json at the plugin root, then each plugin.json's lspServers, as a path
// or inline. A later definition of the same server name wins, as for MCP.
func CollectLSP(lock lockfile.PackLock, effective *mode.Effective) (map[string]json.RawMessage, error) {
	merged := make(map[string]json.RawMessage)
	plugins := lock.Plugins()
	sort.Slice(plugins, func(i, j int) bool { return plugins[i].CacheKey < plugins[j].CacheKey })
	for _, plugin := range plugins {
		if plugin.CacheKey == "" || disabledPlugin(lock, plugin.CacheKey) {
			continue
		}
		if effective != nil {
			allowed, err := effective.AllowsPackagePath(plugin.Module, ".lsp.json")
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
		mergePluginLSP(root, plugin, merged)
	}
	return merged, nil
}

func mergePluginLSP(root string, plugin lockfile.Package, merged map[string]json.RawMessage) {
	read := make(map[string]bool)
	file := func(path, label string) {
		resolved, err := filepath.EvalSymlinks(path)
		if err != nil || read[resolved] {
			return
		}
		read[resolved] = true
		data, err := os.ReadFile(resolved)
		if err != nil {
			log.Printf("warning: skipping language servers in %s: %v", label, err)
			return
		}
		mergeLSPObject(data, plugin.Module+" "+label, merged)
	}
	file(filepath.Join(root, ".lsp.json"), ".lsp.json")
	for _, pluginManifest := range cache.ReadPluginManifests(root) {
		label := pluginManifest.Relative + " lspServers"
		declared, inline := pluginManifest.Declared("lspServers")
		for _, path := range declared {
			resolved, err := cache.ResolveInside(root, path)
			if err != nil {
				log.Printf("warning: skipping %s %s: %v", plugin.Module, label, err)
				continue
			}
			file(resolved, strings.TrimPrefix(path, "./"))
		}
		for _, value := range inline {
			mergeLSPObject(value, plugin.Module+" "+label, merged)
		}
	}
}

func mergeLSPObject(data []byte, label string, merged map[string]json.RawMessage) {
	standard, err := hujson.Standardize(append([]byte(nil), data...))
	var servers map[string]json.RawMessage
	if err == nil {
		err = json.Unmarshal(standard, &servers)
	}
	if err != nil {
		log.Printf("warning: skipping language servers in %s: not an object of servers", label)
		return
	}
	for name, server := range servers {
		var fields struct {
			Command             string            `json:"command"`
			ExtensionToLanguage map[string]string `json:"extensionToLanguage"`
		}
		if json.Unmarshal(server, &fields) != nil || fields.Command == "" || len(fields.ExtensionToLanguage) == 0 {
			log.Printf("warning: %s: skipping language server %q: it needs a command and extensionToLanguage", label, name)
			continue
		}
		merged[name] = server
	}
}
