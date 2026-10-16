package cache

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	githubsource "github.com/olegpustovit/agentpack/internal/github"
)

// CatalogOnlyError reports a marketplace catalog entry that defines a plugin
// in the catalog itself (Claude Code's "strict": false) and names component
// paths that its directory does not hold.
type CatalogOnlyError struct {
	Spec    string
	Name    string
	Catalog string
	Missing []string
}

func (err *CatalogOnlyError) Error() string {
	return fmt.Sprintf("cannot install %s: it is the marketplace catalog entry %q, which %s defines in the catalog itself, and its directory has no %s; agentpack installs a plugin from its own directory and does not follow catalog paths outside it; add the directories the entry points to instead",
		err.Spec, err.Name, err.Catalog, strings.Join(err.Missing, ", "))
}

// pluginManifestFields are the keys of a catalog entry that Claude Code's
// plugin.json also has. The others (source, category, tags, strict) only
// describe the entry in the catalog.
var pluginManifestFields = []string{"name", "version", "description", "author", "homepage", "repository", "license", "keywords",
	"commands", "agents", "skills", "hooks", "mcpServers", "lspServers", "outputStyles"}

// componentFields hold paths, relative to the plugin directory, that must exist.
var componentFields = map[string]bool{"commands": true, "agents": true, "skills": true, "hooks": true, "outputStyles": true}

// writeCatalogManifest handles a repository directory that is a plugin only
// through its marketplace catalog entry, as anthropics/claude-plugins-official
// does for its language server plugins (pyright-lsp holds a LICENSE and a
// README; the catalog entry carries lspServers). Claude Code then takes the
// entry as the plugin's manifest; so does agentpack, by writing the entry's
// manifest fields to .claude-plugin/plugin.json in out. It returns false when
// no catalog lists the directory.
func writeCatalogManifest(archive []byte, source githubsource.Source, out string) (bool, error) {
	catalogs := make([]string, 0, len(marketplaceManifestPaths))
	for _, relative := range marketplaceManifestPaths {
		catalogs = append(catalogs, filepath.ToSlash(relative))
	}
	files, err := githubsource.ReadRepoFiles(bytes.NewReader(archive), catalogs...)
	if err != nil {
		return false, nil
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
			var name string
			_ = json.Unmarshal(entry["name"], &name)
			manifest := make(map[string]json.RawMessage)
			var missing []string
			for _, field := range pluginManifestFields {
				value, found := entry[field]
				if !found {
					continue
				}
				manifest[field] = value
				if !componentFields[field] {
					continue
				}
				var one string
				var several []string
				if json.Unmarshal(value, &one) == nil {
					several = []string{one}
				} else if json.Unmarshal(value, &several) != nil {
					continue // an inline object (hooks), nothing to look for
				}
				for _, relative := range several {
					if _, err := os.Stat(filepath.Join(out, filepath.FromSlash(relative))); err != nil {
						missing = append(missing, field+" "+relative)
					}
				}
			}
			if len(missing) != 0 {
				sort.Strings(missing)
				return false, &CatalogOnlyError{Spec: source.Owner + "/" + source.Repo + "/" + wanted, Name: name, Catalog: catalog, Missing: missing}
			}
			data, err := json.MarshalIndent(manifest, "", "  ")
			if err != nil {
				return false, err
			}
			if err := os.MkdirAll(filepath.Join(out, ".claude-plugin"), 0o755); err != nil {
				return false, err
			}
			return true, os.WriteFile(filepath.Join(out, ".claude-plugin", "plugin.json"), append(data, '\n'), 0o644)
		}
	}
	return false, nil
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
