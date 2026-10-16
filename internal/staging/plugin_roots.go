package staging

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/olegpustovit/agentpack/internal/cache"
	"github.com/olegpustovit/agentpack/internal/mcp"
	"github.com/olegpustovit/agentpack/internal/paths"
	"github.com/olegpustovit/agentpack/internal/slug"
)

// The names the plugin formats give a plugin's own directory, and its data
// directory, inside an MCP server definition. Claude Code expands the first
// of each and Cursor the first two of the root names; no agent can expand
// them for a package that agentpack merged into one bundle, because the
// bundle is not the plugin's directory.
var (
	pluginRootVariables = []string{"${CLAUDE_PLUGIN_ROOT}", "${CURSOR_PLUGIN_ROOT}", "${PLUGIN_ROOT}"}
	pluginDataVariables = []string{"${CLAUDE_PLUGIN_DATA}", "${PLUGIN_DATA}"}
)

// stagePluginRoots replaces the plugin directory variables in the MCP servers
// that plugins define. A server such as
//
//	bun run --cwd ${CLAUDE_PLUGIN_ROOT} start
//
// runs from the plugin's own files and may write next to them, so each such
// package gets one whole copy under the mode's plugin-roots directory, and
// every harness's configuration names that copy. The verified cache entry is
// never handed to a server.
func (pipeline Pipeline) stagePluginRoots(entries mcp.Entries) (mcp.Entries, error) {
	roots, err := paths.StagingPluginRootsDirForMode(pipeline.ProjectRoot, pipeline.Mode.Name())
	if err != nil {
		return nil, err
	}
	staged := make(map[string]string)
	resolved := make(mcp.Entries, len(entries))
	for _, name := range entries.Names() {
		entry := entries[name]
		resolved[name] = entry
		entry = withPluginCwd(entry)
		if entry.CacheKey == "" || !referencesAny(entry.Server, append(pluginRootVariables, pluginDataVariables...)) {
			continue
		}
		replacements := make(map[string]string)
		if referencesAny(entry.Server, pluginRootVariables) {
			root, found := staged[entry.CacheKey]
			if !found {
				root = filepath.Join(roots, pluginRootName(entry.CacheKey))
				source, err := cache.EntryDir(entry.CacheKey)
				if err != nil {
					return nil, err
				}
				if err := pipeline.copyPluginRoot(source, root, entry.Module); err != nil {
					return nil, fmt.Errorf("stage plugin directory of %s for MCP server %s: %w", entry.Module, name, err)
				}
				staged[entry.CacheKey] = root
			}
			for _, variable := range pluginRootVariables {
				replacements[variable] = root
			}
		}
		if referencesAny(entry.Server, pluginDataVariables) {
			data, err := paths.PluginDataDir(pipeline.ProjectRoot, slug.DashedLower(entry.Module))
			if err != nil {
				return nil, err
			}
			if err := os.MkdirAll(data, 0o755); err != nil {
				return nil, err
			}
			for _, variable := range pluginDataVariables {
				replacements[variable] = data
			}
		}
		entry.Server = substituteServer(entry.Server, replacements)
		resolved[name] = entry
	}
	return resolved, nil
}

// verifyPluginRoots checks that every plugin directory the staged MCP
// configuration names is still there, so a launch does not reuse staging
// from which it was removed.
func (pipeline Pipeline) verifyPluginRoots() error {
	entries, err := CollectMCP(pipeline.workspace(), pipeline.Lock, pipeline.Manifest, &pipeline.Mode)
	if err != nil {
		return err
	}
	roots, err := paths.StagingPluginRootsDirForMode(pipeline.ProjectRoot, pipeline.Mode.Name())
	if err != nil {
		return err
	}
	for _, name := range entries.Names() {
		entry := withPluginCwd(entries[name])
		if entry.CacheKey == "" || !referencesAny(entry.Server, pluginRootVariables) {
			continue
		}
		root := filepath.Join(roots, pluginRootName(entry.CacheKey))
		if info, err := os.Stat(root); err != nil || !info.IsDir() {
			return fmt.Errorf("staged plugin directory for MCP server %s is missing: %s", name, root)
		}
	}
	return nil
}

// withPluginCwd makes a plugin server's relative working directory relative
// to the plugin, which is what "cwd": "." means in a plugin's own file.
func withPluginCwd(entry mcp.Entry) mcp.Entry {
	if cwd := entry.Server.Cwd; entry.CacheKey != "" && cwd != nil && !filepath.IsAbs(*cwd) && !strings.Contains(*cwd, "${") {
		inPlugin := filepath.ToSlash(filepath.Join("${PLUGIN_ROOT}", *cwd))
		entry.Server.Cwd = &inPlugin
	}
	return entry
}

func pluginRootName(cacheKey string) string {
	if len(cacheKey) > 16 {
		return cacheKey[:16]
	}
	return cacheKey
}

func referencesAny(server mcp.Server, variables []string) bool {
	contains := func(value string) bool {
		for _, variable := range variables {
			if strings.Contains(value, variable) {
				return true
			}
		}
		return false
	}
	if server.Command != nil && contains(*server.Command) {
		return true
	}
	for _, argument := range server.Args {
		if contains(argument) {
			return true
		}
	}
	for _, value := range server.Env {
		if contains(value) {
			return true
		}
	}
	for _, value := range server.Headers {
		if contains(value) {
			return true
		}
	}
	return server.Cwd != nil && contains(*server.Cwd)
}

func substituteServer(server mcp.Server, replacements map[string]string) mcp.Server {
	replace := func(value string) string {
		for variable, path := range replacements {
			value = strings.ReplaceAll(value, variable, path)
		}
		return value
	}
	if server.Command != nil {
		command := replace(*server.Command)
		server.Command = &command
	}
	if len(server.Args) != 0 {
		arguments := make([]string, len(server.Args))
		for index, argument := range server.Args {
			arguments[index] = replace(argument)
		}
		server.Args = arguments
	}
	if len(server.Env) != 0 {
		environment := make(map[string]string, len(server.Env))
		for key, value := range server.Env {
			environment[key] = replace(value)
		}
		server.Env = environment
	}
	if len(server.Headers) != 0 {
		headers := make(map[string]string, len(server.Headers))
		for key, value := range server.Headers {
			headers[key] = replace(value)
		}
		server.Headers = headers
	}
	if server.Cwd != nil {
		directory := replace(*server.Cwd)
		server.Cwd = &directory
	}
	return server
}

// copyPluginRoot copies a package out of the cache as it is: every file the
// mode allows, with its permission bits, and the links that stay inside it.
func (pipeline Pipeline) copyPluginRoot(source, destination, module string) error {
	if err := os.RemoveAll(destination); err != nil {
		return err
	}
	source, err := filepath.EvalSymlinks(source)
	if err != nil {
		return err
	}
	return filepath.WalkDir(source, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		output := filepath.Join(destination, relative)
		if entry.IsDir() {
			return os.MkdirAll(output, 0o755)
		}
		allowed, err := pipeline.Mode.AllowsPackagePath(module, filepath.ToSlash(relative))
		if err != nil || !allowed {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			if resolved, err := filepath.EvalSymlinks(path); err != nil || !cache.Inside(source, resolved) {
				return nil
			}
			return os.Symlink(target, output)
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		input, err := os.Open(path)
		if err != nil {
			return err
		}
		defer input.Close()
		file, err := os.OpenFile(output, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, info.Mode().Perm())
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(file, input)
		if closeErr := file.Close(); copyErr == nil {
			copyErr = closeErr
		}
		return copyErr
	})
}
