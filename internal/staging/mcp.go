package staging

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"sort"

	"github.com/OlegHQ/agentpack/internal/cache"
	"github.com/OlegHQ/agentpack/internal/lockfile"
	"github.com/OlegHQ/agentpack/internal/manifest"
	"github.com/OlegHQ/agentpack/internal/mcp"
	"github.com/OlegHQ/agentpack/internal/mode"
	"github.com/OlegHQ/agentpack/internal/paths"
	"github.com/tailscale/hujson"
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
		mergeMCPFile(filepath.Join(root, "mcp.json"), mcp.Plugin, effective, merged)
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
	mergeMCPFile(dotPath, mcp.DotAgents, effective, merged)
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

func mergeMCPFile(path string, source mcp.Source, effective *mode.Effective, merged mcp.Entries) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return
	}
	if err != nil {
		log.Printf("warning: skipping %s: %v", path, err)
		return
	}
	standard, err := hujson.Standardize(append(data, '\n'))
	if err != nil {
		log.Printf("warning: skipping %s: %v", path, err)
		return
	}
	var config mcp.Config
	if err := json.Unmarshal(standard, &config); err != nil {
		log.Printf("warning: skipping %s: %v", path, err)
		return
	}
	for name, server := range config.Servers {
		if effective == nil || effective.AllowsMCP(name) {
			merged[name] = mcp.Entry{Server: server, Source: source}
		}
	}
}
