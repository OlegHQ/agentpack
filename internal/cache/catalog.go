package cache

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	githubsource "github.com/OlegHQ/agentpack/internal/github"
)

// CatalogOnlyError reports a repository directory that is not a package by
// itself: the repository's marketplace catalog lists it as a plugin and
// defines the plugin in the catalog entry.
type CatalogOnlyError struct {
	Spec     string
	Name     string
	Catalog  string
	Fields   []string
	Language []string
}

func (err *CatalogOnlyError) Error() string {
	if len(err.Language) != 0 {
		return fmt.Sprintf("cannot install %s: it is the marketplace catalog entry %q, a language server configuration for Claude Code (%s defines lspServers: %s) with no plugin files of its own; its directory holds no skills, commands, agents, rules, hooks or MCP servers, and agentpack has no language server setting to write for the agents, so there is nothing to stage; to use it, install the language server and add the plugin in Claude Code from that marketplace",
			err.Spec, err.Name, err.Catalog, strings.Join(err.Language, "; "))
	}
	return fmt.Sprintf("cannot install %s: it is the marketplace catalog entry %q, which %s defines in the catalog itself (%s) and whose directory has no plugin manifest or SKILL.md; agentpack installs a plugin from its own directory and does not build one from a catalog entry; add the directories the entry points to instead",
		err.Spec, err.Name, err.Catalog, strings.Join(err.Fields, ", "))
}

// catalogOnlyEntry looks the fetched directory up in the repository's
// marketplace catalogs. It returns a CatalogOnlyError when a catalog lists
// the directory as a plugin, and nil when no catalog mentions it.
func catalogOnlyEntry(archive []byte, source githubsource.Source) error {
	catalogs := make([]string, 0, len(marketplaceManifestPaths))
	for _, relative := range marketplaceManifestPaths {
		catalogs = append(catalogs, filepath.ToSlash(relative))
	}
	files, err := githubsource.ReadRepoFiles(bytes.NewReader(archive), catalogs...)
	if err != nil {
		return nil
	}
	wanted := strings.Trim(source.Path, "/")
	for _, catalog := range catalogs {
		var document struct {
			Metadata struct {
				PluginRoot string `json:"pluginRoot"`
			} `json:"metadata"`
			Plugins []map[string]json.RawMessage `json:"plugins"`
		}
		if json.Unmarshal(files[catalog], &document) != nil {
			continue
		}
		for _, entry := range document.Plugins {
			var local string
			if json.Unmarshal(entry["source"], &local) != nil {
				var object struct{ Source, Path string }
				if json.Unmarshal(entry["source"], &object) != nil || object.Source != "local" {
					continue
				}
				local = object.Path
			}
			local = strings.Trim(strings.TrimPrefix(filepath.ToSlash(filepath.Join(document.Metadata.PluginRoot, local)), "./"), "/")
			if local != wanted {
				continue
			}
			result := &CatalogOnlyError{Spec: source.Owner + "/" + source.Repo + "/" + wanted, Catalog: catalog}
			_ = json.Unmarshal(entry["name"], &result.Name)
			for _, field := range []string{"lspServers", "mcpServers", "skills", "commands", "agents", "hooks", "outputStyles", "strict"} {
				if _, found := entry[field]; found {
					result.Fields = append(result.Fields, field)
				}
			}
			var servers map[string]struct {
				Command string   `json:"command"`
				Args    []string `json:"args"`
			}
			if json.Unmarshal(entry["lspServers"], &servers) == nil {
				for name, server := range servers {
					result.Language = append(result.Language, fmt.Sprintf("%s runs `%s`", name, strings.TrimSpace(server.Command+" "+strings.Join(server.Args, " "))))
				}
				sort.Strings(result.Language)
			}
			return result
		}
	}
	return nil
}
