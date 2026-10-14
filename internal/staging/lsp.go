package staging

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/OlegHQ/agentpack/internal/cache"
	"github.com/OlegHQ/agentpack/internal/lockfile"
	"github.com/OlegHQ/agentpack/internal/mode"
	"github.com/OlegHQ/agentpack/internal/paths"
	"github.com/OlegHQ/agentpack/internal/slug"
	"github.com/tailscale/hujson"
)

// LSPEntry is one language server and the package that declares it.
type LSPEntry struct {
	Server   json.RawMessage
	Module   string
	CacheKey string
}

// CollectLSP gathers the language servers that plugins declare, in Claude
// Code's format (name -> {command, args, extensionToLanguage, ...}): a
// .lsp.json at the plugin root, then each plugin.json's lspServers, as a path
// or inline. A later definition of the same server name wins, as for MCP.
func CollectLSP(lock lockfile.PackLock, effective *mode.Effective) (map[string]LSPEntry, error) {
	merged := make(map[string]LSPEntry)
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

func mergePluginLSP(root string, plugin lockfile.Package, merged map[string]LSPEntry) {
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
		mergeLSPObject(data, plugin, plugin.Module+" "+label, merged)
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
			mergeLSPObject(value, plugin, plugin.Module+" "+label, merged)
		}
	}
}

func mergeLSPObject(data []byte, plugin lockfile.Package, label string, merged map[string]LSPEntry) {
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
		merged[name] = LSPEntry{Server: server, Module: plugin.Module, CacheKey: plugin.CacheKey}
	}
}

// stageLSPRoots replaces the plugin directory variables in language server
// definitions, as stagePluginRoots does for MCP servers: a server that ships
// in its package (node ${CLAUDE_PLUGIN_ROOT}/server.js) runs from the same
// staged copy of the package, and its data variable names the package's data
// directory.
func (pipeline Pipeline) stageLSPRoots(entries map[string]LSPEntry) (map[string]json.RawMessage, error) {
	roots, err := paths.StagingPluginRootsDirForMode(pipeline.ProjectRoot, pipeline.Mode.Name())
	if err != nil {
		return nil, err
	}
	quoted := func(path string) string {
		data, _ := json.Marshal(path)
		return strings.Trim(string(data), `"`)
	}
	resolved := make(map[string]json.RawMessage, len(entries))
	for name, entry := range entries {
		text := string(entry.Server)
		uses := func(variables []string) bool {
			for _, variable := range variables {
				if strings.Contains(text, variable) {
					return true
				}
			}
			return false
		}
		if entry.CacheKey != "" && uses(pluginRootVariables) {
			root := filepath.Join(roots, pluginRootName(entry.CacheKey))
			if _, err := os.Stat(root); err != nil {
				source, err := cache.EntryDir(entry.CacheKey)
				if err != nil {
					return nil, err
				}
				if err := pipeline.copyPluginRoot(source, root, entry.Module); err != nil {
					return nil, fmt.Errorf("stage plugin directory of %s for language server %s: %w", entry.Module, name, err)
				}
			}
			for _, variable := range pluginRootVariables {
				text = strings.ReplaceAll(text, variable, quoted(root))
			}
		}
		if entry.CacheKey != "" && uses(pluginDataVariables) {
			data, err := paths.PluginDataDir(pipeline.ProjectRoot, slug.DashedLower(entry.Module))
			if err != nil {
				return nil, err
			}
			if err := os.MkdirAll(data, 0o755); err != nil {
				return nil, err
			}
			for _, variable := range pluginDataVariables {
				text = strings.ReplaceAll(text, variable, quoted(data))
			}
		}
		resolved[name] = json.RawMessage(text)
	}
	return resolved, nil
}
