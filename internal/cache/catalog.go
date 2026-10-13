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

// CatalogPlugin is where one named plugin of a marketplace catalog lives.
type CatalogPlugin struct {
	Name    string
	Catalog string
	// Local says the plugin is a directory of the marketplace repository
	// itself; Path is then relative to the repository root.
	Local bool
	// Owner, Repo and Ref name the GitHub repository and the commit, tag or
	// branch of an entry that points at another repository.
	Owner, Repo, Ref string
	Path             string
}

// ResolveCatalogPlugin finds a plugin by name in the marketplace catalogs
// (.claude-plugin/marketplace.json, .cursor-plugin/marketplace.json,
// .agents/plugins/marketplace.json) below directory in a repository archive.
// It understands every source kind of the Claude Code marketplace schema that
// names a directory or a git repository: a relative path, {"source":"local"},
// {"source":"github"}, {"source":"url"} and {"source":"git-subdir"}. An entry
// pinned with "sha" resolves to that commit, otherwise to its "ref".
func ResolveCatalogPlugin(archive []byte, directory, name string) (CatalogPlugin, error) {
	directory = strings.Trim(directory, "/")
	var catalogs []string
	// Claude Code's catalog first: it is the one that carries external sources.
	for index := len(marketplaceSpecs) - 1; index >= 0; index-- {
		relative := filepath.ToSlash(marketplaceSpecs[index].relativePath)
		if directory != "" {
			relative = directory + "/" + relative
		}
		catalogs = append(catalogs, relative)
	}
	files, err := githubsource.ReadRepoFiles(bytes.NewReader(archive), catalogs...)
	if err != nil {
		return CatalogPlugin{}, err
	}
	if len(files) == 0 {
		return CatalogPlugin{}, fmt.Errorf("no marketplace catalog (.claude-plugin/marketplace.json, .cursor-plugin/marketplace.json or .agents/plugins/marketplace.json) at this source, so --plugin has nothing to select from")
	}
	var known []string
	for _, catalog := range catalogs {
		data, found := files[catalog]
		if !found {
			continue
		}
		var document struct {
			Metadata struct {
				PluginRoot string `json:"pluginRoot"`
			} `json:"metadata"`
			Renames map[string]string            `json:"renames"`
			Plugins []map[string]json.RawMessage `json:"plugins"`
		}
		if err := json.Unmarshal(data, &document); err != nil {
			return CatalogPlugin{}, fmt.Errorf("parse marketplace catalog %s: %w", catalog, err)
		}
		wanted := name
		if renamed, found := document.Renames[name]; found {
			wanted = renamed
		}
		for _, entry := range document.Plugins {
			var entryName string
			_ = json.Unmarshal(entry["name"], &entryName)
			known = append(known, entryName)
			if entryName != wanted {
				continue
			}
			plugin := CatalogPlugin{Name: entryName, Catalog: catalog}
			var local string
			if json.Unmarshal(entry["source"], &local) == nil {
				plugin.Local = true
			} else {
				var source struct {
					Source, Repo, URL, Path, Ref, SHA string
				}
				if err := json.Unmarshal(entry["source"], &source); err != nil {
					return CatalogPlugin{}, fmt.Errorf("plugin %q in %s has a source that is neither a path nor an object", entryName, catalog)
				}
				switch source.Source {
				case "local":
					plugin.Local, local = true, source.Path
				case "github":
					owner, repo, found := strings.Cut(strings.Trim(source.Repo, "/"), "/")
					if !found {
						return CatalogPlugin{}, fmt.Errorf("plugin %q in %s has a github source without an owner/repo", entryName, catalog)
					}
					plugin.Owner, plugin.Repo, plugin.Path = owner, repo, source.Path
				case "url", "git-subdir", "git":
					parsed, err := githubsource.ParseURL(strings.TrimSuffix(source.URL, ".git"))
					if err != nil {
						return CatalogPlugin{}, fmt.Errorf("plugin %q in %s lives at %s; agentpack fetches packages from github.com only, so it cannot be added", entryName, catalog, source.URL)
					}
					plugin.Owner, plugin.Repo, plugin.Path = parsed.Owner, parsed.Repo, source.Path
				default:
					return CatalogPlugin{}, fmt.Errorf("plugin %q in %s has a %q source, which is not a git repository or a directory; agentpack cannot add it", entryName, catalog, source.Source)
				}
				plugin.Ref = source.Ref
				if source.SHA != "" {
					plugin.Ref = source.SHA
				}
			}
			if plugin.Local {
				clean, err := safeRelativePath(filepath.ToSlash(filepath.Join(document.Metadata.PluginRoot, local)), catalog, entryName)
				if err != nil {
					return CatalogPlugin{}, err
				}
				plugin.Path = filepath.ToSlash(filepath.Join(directory, clean))
			}
			plugin.Path = strings.Trim(filepath.ToSlash(plugin.Path), "/")
			return plugin, nil
		}
	}
	sort.Strings(known)
	shown := known
	if len(shown) > 12 {
		shown = shown[:12]
	}
	return CatalogPlugin{}, fmt.Errorf("the marketplace lists no plugin named %q; it lists %d, among them %s", name, len(known), strings.Join(shown, ", "))
}
