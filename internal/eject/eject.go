// Package eject writes what Agentpack stages into the project itself, where
// each agent's own front ends (IDE extensions, desktop apps, the agents' CLIs
// started without Agentpack) read project configuration. The staged tree is
// copied to <workspace>/.agentpack/eject, its paths are rewritten to the copy,
// and each agent is pointed at it through its project-scope settings. Every
// file and key written outside that directory is recorded in a manifest so
// that Remove takes out exactly what Eject added.
package eject

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"

	"github.com/pelletier/go-toml/v2"
	"github.com/tailscale/hujson"

	"github.com/olegpustovit/agentpack/internal/artifacts"
	base "github.com/olegpustovit/agentpack/internal/harness"
	"github.com/olegpustovit/agentpack/internal/harness/codex"
	"github.com/olegpustovit/agentpack/internal/paths"
)

// Dir is the project-relative directory that holds the ejected copy.
const Dir = ".agentpack/eject"

const (
	manifestName  = "manifest.json"
	blockBegin    = "<!-- agentpack:eject:begin -->"
	blockEnd      = "<!-- agentpack:eject:end -->"
	guidanceBegin = "<!-- agentpack:guidance:begin -->"
	guidanceEnd   = "<!-- agentpack:guidance:end -->"
)

// Supported lists the agents eject writes for. OpenCode and Grok are used
// from the terminal, where the launcher is the way in.
func Supported() []base.Target { return []base.Target{base.Claude, base.Cursor, base.Codex, base.Agy} }

// Options selects the staged tree to eject and the agents to write it for.
type Options struct {
	ProjectRoot   string
	WorkspaceRoot string
	Mode          string
	Targets       []base.Target
}

// Result reports what an Eject or Remove did, in project-relative paths.
type Result struct {
	Dir     string
	Written []string
	Removed []string
	Notes   []string
}

// Manifest records everything written outside Dir.
type Manifest struct {
	Version     int         `json:"version"`
	Mode        string      `json:"mode"`
	Targets     []string    `json:"targets"`
	Files       []string    `json:"files"`
	JSONKeys    []jsonKey   `json:"json_keys,omitempty"`
	JSONEntries []jsonEntry `json:"json_entries,omitempty"`
	TOMLKeys    []tomlKey   `json:"toml_keys,omitempty"`
	Blocks      []string    `json:"blocks,omitempty"`
	Created     []string    `json:"created,omitempty"`
}

type jsonKey struct {
	File string   `json:"file"`
	Path []string `json:"path"`
}

type jsonEntry struct {
	File  string          `json:"file"`
	Path  []string        `json:"path"`
	Value json.RawMessage `json:"value"`
}

type tomlKey struct {
	File string   `json:"file"`
	Path []string `json:"path"`
}

// plan is the set of writes one Eject makes; it is checked in full before
// anything outside Dir is touched.
type plan struct {
	workspace string
	copies    map[string]string // project-relative destination -> source
	generated map[string][]byte // project-relative destination -> content
	notes     []string
	keys      []keyWrite
	entries   []entryWrite
	tomls     []tomlWrite
	blocks    map[string]string // project-relative file -> block body
}

type keyWrite struct {
	file  string
	path  []string
	value any
}

type entryWrite struct {
	file  string
	path  []string
	value any
}

type tomlWrite struct {
	file  string
	path  []string
	value any
}

// Eject copies the staged tree of opts.Mode into the project and registers it
// with each target. A previous eject is removed first, so running it again
// after a sync replaces the content.
func Eject(opts Options) (Result, error) {
	workspace := opts.WorkspaceRoot
	if workspace == "" {
		workspace = opts.ProjectRoot
	}
	result := Result{Dir: Dir}
	if _, err := os.Stat(filepath.Join(workspace, Dir, manifestName)); err == nil {
		removed, err := Remove(workspace)
		if err != nil {
			return result, fmt.Errorf("remove the previous eject: %w", err)
		}
		result.Removed = removed.Removed
	}
	staged, err := paths.StagingRootForMode(opts.ProjectRoot, opts.Mode)
	if err != nil {
		return result, err
	}
	if info, err := os.Stat(staged); err != nil || !info.IsDir() {
		return result, fmt.Errorf("nothing is staged for mode %q; run `agentpack sync` first", opts.Mode)
	}
	root := filepath.Join(workspace, Dir)
	stage := filepath.Join(root, "stage")
	if err := os.RemoveAll(root); err != nil {
		return result, err
	}
	rewrites := []rewrite{{from: staged, to: stage}}
	if err := copyTree(staged, stage, skipStaged); err != nil {
		return result, err
	}
	wantCodex := slices.Contains(opts.Targets, base.Codex)
	codexCopy := filepath.Join(root, "codex")
	if wantCodex {
		home, err := codex.CurrentHome(opts.ProjectRoot, opts.Mode)
		if err != nil {
			return result, err
		}
		for _, name := range []string{"skills", "agents", "hooks", "hooks.json", "AGENTS.md", "config.toml"} {
			source := filepath.Join(home, name)
			if _, err := os.Stat(source); err != nil {
				continue
			}
			if err := copyTree(source, filepath.Join(codexCopy, name), skipCodex); err != nil {
				return result, err
			}
		}
		rewrites = append(rewrites, rewrite{from: home, to: codexCopy})
	}
	if err := rewritePaths(root, rewrites); err != nil {
		return result, err
	}
	p := plan{workspace: workspace, copies: map[string]string{}, generated: map[string][]byte{}, blocks: map[string]string{}}
	for _, target := range opts.Targets {
		var err error
		switch target {
		case base.Claude:
			err = planClaude(&p, stage)
		case base.Cursor:
			err = planCursor(&p, stage)
		case base.Codex:
			err = planCodex(&p, codexCopy)
		case base.Agy:
			err = planAgy(&p, stage)
		default:
			err = fmt.Errorf("eject does not write for %s; start it with `agentpack %s`", target, target)
		}
		if err != nil {
			_ = os.RemoveAll(root)
			return result, err
		}
	}
	if err := p.check(); err != nil {
		_ = os.RemoveAll(root)
		return result, err
	}
	manifest := Manifest{Version: 1, Mode: opts.Mode}
	for _, target := range opts.Targets {
		manifest.Targets = append(manifest.Targets, string(target))
	}
	if err := p.apply(&manifest); err != nil {
		// leave a manifest for what was written, so Remove can still take it out
		_ = writeManifest(root, manifest)
		return result, err
	}
	if err := writeManifest(root, manifest); err != nil {
		return result, err
	}
	result.Notes = p.notes
	result.Written = append(append([]string(nil), manifest.Files...), manifest.Blocks...)
	for _, key := range manifest.JSONKeys {
		result.Written = append(result.Written, key.File+" "+strings.Join(key.Path, "."))
	}
	for _, key := range manifest.TOMLKeys {
		result.Written = append(result.Written, key.File+" "+strings.Join(key.Path, "."))
	}
	for _, entry := range manifest.JSONEntries {
		result.Written = append(result.Written, entry.File+" "+strings.Join(entry.Path, ".")+"[]")
	}
	sort.Strings(result.Written)
	result.Written = slices.Compact(result.Written)
	return result, nil
}

// Remove takes out everything the last Eject recorded, then the ejected copy.
func Remove(workspace string) (Result, error) {
	root := filepath.Join(workspace, Dir)
	result := Result{Dir: Dir}
	data, err := os.ReadFile(filepath.Join(root, manifestName))
	if os.IsNotExist(err) {
		return result, fmt.Errorf("nothing to remove: %s has no manifest", filepath.Join(Dir, manifestName))
	}
	if err != nil {
		return result, err
	}
	var manifest Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return result, fmt.Errorf("parse %s: %w", filepath.Join(Dir, manifestName), err)
	}
	var errs []error
	for _, file := range manifest.Files {
		if err := os.Remove(filepath.Join(workspace, file)); err != nil && !os.IsNotExist(err) {
			errs = append(errs, err)
			continue
		}
		result.Removed = append(result.Removed, file)
		pruneEmptyParents(workspace, filepath.Dir(file))
	}
	for index := len(manifest.JSONEntries) - 1; index >= 0; index-- {
		entry := manifest.JSONEntries[index]
		if err := removeJSONEntry(filepath.Join(workspace, entry.File), entry.Path, entry.Value); err != nil {
			errs = append(errs, err)
		}
	}
	for index := len(manifest.JSONKeys) - 1; index >= 0; index-- {
		key := manifest.JSONKeys[index]
		if err := removeJSONKey(filepath.Join(workspace, key.File), key.Path); err != nil {
			errs = append(errs, err)
		}
	}
	for index := len(manifest.TOMLKeys) - 1; index >= 0; index-- {
		key := manifest.TOMLKeys[index]
		if err := removeTOMLKey(filepath.Join(workspace, key.File), key.Path); err != nil {
			errs = append(errs, err)
		}
	}
	for _, file := range manifest.Blocks {
		if err := removeBlock(filepath.Join(workspace, file)); err != nil {
			errs = append(errs, err)
		}
	}
	// files eject created only to hold its keys go when nothing else is left in them
	for _, file := range manifest.Created {
		if removeIfEmpty(filepath.Join(workspace, file)) {
			result.Removed = append(result.Removed, file)
			pruneEmptyParents(workspace, filepath.Dir(file))
		}
	}
	if err := errors.Join(errs...); err != nil {
		return result, err
	}
	if err := os.RemoveAll(root); err != nil {
		return result, err
	}
	pruneEmptyParents(workspace, filepath.Dir(Dir))
	return result, nil
}

// ------------------------------------------------------------- per agent

// planClaude writes the bundle as plain project configuration, which the CLI,
// the IDE extensions and the desktop app all read. A project marketplace in
// .claude/settings.json is not installed until a person accepts it in an
// interactive session, so the bundle is not registered as a plugin.
func planClaude(p *plan, stage string) error {
	bundle := filepath.Join(stage, "plugins", paths.StagedAgentpackBundleName)
	if _, err := os.Stat(filepath.Join(bundle, ".claude-plugin", "plugin.json")); err != nil {
		return fmt.Errorf("no Claude Code bundle is staged: %w", err)
	}
	for _, kind := range []string{"skills", "commands", "agents"} {
		if err := p.copyDir(filepath.Join(bundle, kind), filepath.Join(".claude", kind)); err != nil {
			return err
		}
	}
	if err := p.claudeRules(filepath.Join(bundle, "rules")); err != nil {
		return err
	}
	if err := p.mergeServers(filepath.Join(bundle, ".mcp.json"), ".mcp.json"); err != nil {
		return err
	}
	settings := filepath.Join(".claude", "settings.json")
	// Claude Code starts a project's .mcp.json servers only once they are approved. The servers the pack
	// declares are approved by name, as the launcher does; enableAllProjectMcpServers would also approve
	// servers the user adds to .mcp.json later, so it is not set.
	if data, err := os.ReadFile(filepath.Join(bundle, "agentpack-settings.json")); err == nil {
		var staged struct {
			Servers []string `json:"enabledMcpjsonServers"`
		}
		if err := json.Unmarshal(data, &staged); err != nil {
			return fmt.Errorf("parse staged Claude Code settings: %w", err)
		}
		for _, name := range staged.Servers {
			p.entries = append(p.entries, entryWrite{settings, []string{"enabledMcpjsonServers"}, name})
		}
	}
	if _, err := os.Stat(filepath.Join(bundle, ".lsp.json")); err == nil {
		p.notes = append(p.notes, "language servers are not ejected for Claude Code: only a plugin can declare them; start it with `agentpack claude` to use them")
	}
	// the rules are files now, so the hook that injects them at session start is left out
	return p.mergeHooks(filepath.Join(bundle, "hooks", "hooks.json"), settings, func(item any) bool {
		data, _ := json.Marshal(item)
		return bytes.Contains(data, []byte("hook-exec inject-guidance"))
	})
}

// claudeRules writes each rule where Claude Code reads project rules: an
// always-on rule as it is, a rule for some files with their paths. A rule the
// model applies by its description is already in the bundle as a skill.
func (p *plan) claudeRules(source string) error {
	entries, err := os.ReadDir(source)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		extension := strings.ToLower(filepath.Ext(entry.Name()))
		if entry.IsDir() || extension != ".md" && extension != ".mdc" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(source, entry.Name()))
		if err != nil {
			return err
		}
		rule, err := artifacts.Parse("rules/"+entry.Name(), string(data), "")
		if err != nil || rule == nil || rule.Kind != artifacts.Rule {
			continue
		}
		var text strings.Builder
		switch {
		case rule.AlwaysApply:
		case len(rule.Globs) != 0:
			text.WriteString("---\npaths:\n")
			for _, glob := range rule.Globs {
				quoted, _ := json.Marshal(glob)
				fmt.Fprintf(&text, "  - %s\n", quoted)
			}
			text.WriteString("---\n\n")
		default:
			continue
		}
		text.WriteString(strings.TrimSpace(rule.Body) + "\n")
		p.generated[filepath.Join(".claude", "rules", rule.Name+".md")] = []byte(text.String())
	}
	return nil
}

func planCursor(p *plan, stage string) error {
	bundle := filepath.Join(stage, "cursor", paths.StagedAgentpackBundleName)
	if _, err := os.Stat(bundle); err != nil {
		return fmt.Errorf("no Cursor bundle is staged: %w", err)
	}
	for _, kind := range []string{"skills", "commands", "agents", "rules"} {
		if err := p.copyDir(filepath.Join(bundle, kind), filepath.Join(".cursor", kind)); err != nil {
			return err
		}
	}
	if err := p.mergeServers(filepath.Join(bundle, "mcp.json"), filepath.Join(".cursor", "mcp.json")); err != nil {
		return err
	}
	return p.mergeHooks(filepath.Join(stage, "cursor-home", ".cursor", "hooks.json"), filepath.Join(".cursor", "hooks.json"), nil)
}

func planCodex(p *plan, copied string) error {
	if _, err := os.Stat(copied); err != nil {
		return fmt.Errorf("no Codex home is staged: %w", err)
	}
	skills, _ := os.ReadDir(filepath.Join(copied, "skills"))
	for _, entry := range skills {
		if entry.IsDir() && !strings.HasPrefix(entry.Name(), ".") {
			if err := p.copyDir(filepath.Join(copied, "skills", entry.Name()), filepath.Join(".agents", "skills", entry.Name())); err != nil {
				return err
			}
		}
	}
	if err := p.copyDir(filepath.Join(copied, "agents"), filepath.Join(".codex", "agents")); err != nil {
		return err
	}
	if data, err := os.ReadFile(filepath.Join(copied, "config.toml")); err == nil {
		var config map[string]any
		if err := toml.Unmarshal(data, &config); err != nil {
			return fmt.Errorf("parse staged Codex config: %w", err)
		}
		servers, _ := config["mcp_servers"].(map[string]any)
		for _, name := range sortedKeys(servers) {
			p.tomls = append(p.tomls, tomlWrite{filepath.Join(".codex", "config.toml"), []string{"mcp_servers", name}, servers[name]})
		}
	}
	if err := p.mergeHooks(filepath.Join(copied, "hooks.json"), filepath.Join(".codex", "hooks.json"), nil); err != nil {
		return err
	}
	// Rules Codex gets as always-on guidance; other agents reading AGENTS.md see them too.
	if data, err := os.ReadFile(filepath.Join(copied, "AGENTS.md")); err == nil {
		text := string(data)
		begin, end := strings.Index(text, guidanceBegin), strings.Index(text, guidanceEnd)
		if begin >= 0 && end > begin {
			p.blocks["AGENTS.md"] = strings.TrimSpace(text[begin+len(guidanceBegin) : end])
		}
	}
	return nil
}

func planAgy(p *plan, stage string) error {
	bundle := filepath.Join(stage, "agy", paths.StagedAgentpackBundleName)
	if _, err := os.Stat(filepath.Join(bundle, "plugin.json")); err != nil {
		return fmt.Errorf("no Antigravity bundle is staged: %w", err)
	}
	overlay := filepath.Join(".agents", "plugins", paths.StagedAgentpackBundleName)
	// The launcher links this path to its staging; that link is Agentpack's own and is replaced.
	if info, err := os.Lstat(filepath.Join(p.workspace, overlay)); err == nil && info.Mode()&os.ModeSymlink != 0 {
		if err := os.Remove(filepath.Join(p.workspace, overlay)); err != nil {
			return err
		}
	}
	return p.copyDir(bundle, overlay)
}

// ------------------------------------------------------------- plan helpers

func (p *plan) copyDir(source, destination string) error {
	if _, err := os.Stat(source); os.IsNotExist(err) {
		return nil
	}
	return filepath.WalkDir(source, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() || !entry.Type().IsRegular() {
			return err
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		p.copies[filepath.Join(destination, relative)] = path
		return nil
	})
}

func (p *plan) mergeServers(source, destination string) error {
	data, err := os.ReadFile(source)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var document struct {
		Servers map[string]any `json:"mcpServers"`
	}
	if err := json.Unmarshal(data, &document); err != nil {
		return fmt.Errorf("parse %s: %w", source, err)
	}
	for _, name := range sortedKeys(document.Servers) {
		p.keys = append(p.keys, keyWrite{destination, []string{"mcpServers", name}, document.Servers[name]})
	}
	return nil
}

// mergeHooks appends each staged hook entry to the same event's list.
func (p *plan) mergeHooks(source, destination string, skip func(any) bool) error {
	data, err := os.ReadFile(source)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var document map[string]any
	if err := json.Unmarshal(data, &document); err != nil {
		return fmt.Errorf("parse %s: %w", source, err)
	}
	events, _ := document["hooks"].(map[string]any)
	for _, event := range sortedKeys(events) {
		list, _ := events[event].([]any)
		for _, item := range list {
			if skip != nil && skip(item) {
				continue
			}
			p.entries = append(p.entries, entryWrite{destination, []string{"hooks", event}, item})
		}
	}
	if version, ok := document["version"]; ok && len(events) != 0 {
		p.keys = append(p.keys, keyWrite{destination, []string{"version"}, version})
	}
	return nil
}

// check refuses the whole eject when any write would replace something the
// user wrote.
func (p *plan) check() error {
	var conflicts []string
	for _, destination := range append(sortedKeys(p.copies), sortedKeys(p.generated)...) {
		if _, err := os.Lstat(filepath.Join(p.workspace, destination)); err == nil {
			conflicts = append(conflicts, destination+" exists")
		}
	}
	for _, key := range p.keys {
		current, found, err := readJSONPath(filepath.Join(p.workspace, key.file), key.path)
		if err != nil {
			conflicts = append(conflicts, err.Error())
		} else if found && !sameJSON(current, key.value) {
			if key.path[0] == "version" {
				continue // a hooks file's format version already set by the user is kept
			}
			conflicts = append(conflicts, fmt.Sprintf("%s already sets %s", key.file, strings.Join(key.path, ".")))
		}
	}
	for _, key := range p.tomls {
		current, found, err := readTOMLPath(filepath.Join(p.workspace, key.file), key.path)
		if err != nil {
			conflicts = append(conflicts, err.Error())
		} else if found && !sameJSON(current, key.value) {
			conflicts = append(conflicts, fmt.Sprintf("%s already sets %s", key.file, strings.Join(key.path, ".")))
		}
	}
	for _, entry := range p.entries {
		if _, _, err := readJSONPath(filepath.Join(p.workspace, entry.file), entry.path); err != nil {
			conflicts = append(conflicts, err.Error())
		}
	}
	for file := range p.blocks {
		if data, err := os.ReadFile(filepath.Join(p.workspace, file)); err == nil && bytes.Contains(data, []byte(blockBegin)) {
			conflicts = append(conflicts, file+" already holds an agentpack eject block")
		}
	}
	if len(conflicts) != 0 {
		sort.Strings(conflicts)
		return fmt.Errorf("eject would change files agentpack did not write; nothing was written:\n  %s", strings.Join(slices.Compact(conflicts), "\n  "))
	}
	return nil
}

func (p *plan) apply(manifest *Manifest) error {
	created := map[string]bool{}
	noteCreated := func(file string) {
		if _, err := os.Stat(filepath.Join(p.workspace, file)); os.IsNotExist(err) && !created[file] {
			created[file] = true
			manifest.Created = append(manifest.Created, file)
		}
	}
	for _, destination := range sortedKeys(p.copies) {
		data, err := os.ReadFile(p.copies[destination])
		if err != nil {
			return err
		}
		if err := writeFile(filepath.Join(p.workspace, destination), data); err != nil {
			return err
		}
		manifest.Files = append(manifest.Files, destination)
	}
	for _, destination := range sortedKeys(p.generated) {
		if err := writeFile(filepath.Join(p.workspace, destination), p.generated[destination]); err != nil {
			return err
		}
		manifest.Files = append(manifest.Files, destination)
	}
	for _, key := range p.keys {
		noteCreated(key.file)
		current, found, err := readJSONPath(filepath.Join(p.workspace, key.file), key.path)
		if err != nil {
			return err
		}
		if found && sameJSON(current, key.value) {
			continue // already so; not ours to remove later
		}
		if err := setJSONPath(filepath.Join(p.workspace, key.file), key.path, key.value); err != nil {
			return err
		}
		manifest.JSONKeys = append(manifest.JSONKeys, jsonKey{key.file, key.path})
	}
	for _, entry := range p.entries {
		noteCreated(entry.file)
		raw, err := json.Marshal(entry.value)
		if err != nil {
			return err
		}
		if err := appendJSONEntry(filepath.Join(p.workspace, entry.file), entry.path, entry.value); err != nil {
			return err
		}
		manifest.JSONEntries = append(manifest.JSONEntries, jsonEntry{entry.file, entry.path, raw})
	}
	for _, key := range p.tomls {
		noteCreated(key.file)
		if err := setTOMLPath(filepath.Join(p.workspace, key.file), key.path, key.value); err != nil {
			return err
		}
		manifest.TOMLKeys = append(manifest.TOMLKeys, tomlKey{key.file, key.path})
	}
	for _, file := range sortedKeys(p.blocks) {
		noteCreated(file)
		if err := insertBlock(filepath.Join(p.workspace, file), p.blocks[file]); err != nil {
			return err
		}
		manifest.Blocks = append(manifest.Blocks, file)
	}
	return nil
}

// ------------------------------------------------------------- copying

type rewrite struct{ from, to string }

func skipStaged(relative string, entry fs.DirEntry) bool {
	// user settings Cursor keeps beside its login, and the launcher's own lock, stay out of the project
	return entry.Name() == ".rebuild.lock" || entry.Name() == "cli-config.json"
}

func skipCodex(relative string, entry fs.DirEntry) bool {
	return entry.IsDir() && entry.Name() == ".system"
}

func copyTree(source, destination string, skip func(string, fs.DirEntry) bool) error {
	info, err := os.Stat(source)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		data, err := os.ReadFile(source)
		if err != nil {
			return err
		}
		return writeFile(destination, data)
	}
	return filepath.WalkDir(source, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		if relative != "." && skip(relative, entry) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		target := filepath.Join(destination, relative)
		switch {
		case entry.IsDir():
			return os.MkdirAll(target, 0o755)
		case entry.Type()&os.ModeSymlink != 0:
			// links into staging are followed: the copy must outlive it
			resolved, err := filepath.EvalSymlinks(path)
			if err != nil {
				return nil
			}
			return copyTree(resolved, target, skip)
		case entry.Type().IsRegular():
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			fileInfo, err := entry.Info()
			if err != nil {
				return err
			}
			if err := writeFile(target, data); err != nil {
				return err
			}
			return os.Chmod(target, fileInfo.Mode().Perm())
		}
		return nil
	})
}

// rewritePaths points every staged path in the copied text files at the copy.
func rewritePaths(root string, rewrites []rewrite) error {
	var pairs []string
	add := func(from, to string) {
		// JSON writes a Windows path with each backslash escaped; that form is
		// rewritten with an escaped replacement so the file stays valid JSON.
		if escaped := strings.ReplaceAll(from, `\`, `\\`); escaped != from {
			pairs = append(pairs, escaped, strings.ReplaceAll(to, `\`, `\\`))
		}
		pairs = append(pairs, from, to)
	}
	for _, r := range rewrites {
		add(r.from, r.to)
		if resolved, err := filepath.EvalSymlinks(r.from); err == nil && resolved != r.from {
			add(resolved, r.to)
		}
	}
	replacer := strings.NewReplacer(pairs...)
	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || !entry.Type().IsRegular() {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil || bytes.IndexByte(data, 0) >= 0 {
			return err
		}
		replaced := replacer.Replace(string(data))
		if replaced == string(data) {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		return os.WriteFile(path, []byte(replaced), info.Mode().Perm())
	})
}

func writeFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func writeManifest(root string, manifest Manifest) error {
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	return writeFile(filepath.Join(root, manifestName), append(data, '\n'))
}

func pruneEmptyParents(workspace, relative string) {
	for relative != "." && relative != "" && relative != string(filepath.Separator) {
		if os.Remove(filepath.Join(workspace, relative)) != nil {
			return
		}
		relative = filepath.Dir(relative)
	}
}

// ------------------------------------------------------------- JSON files

func readJSON(path string) (map[string]any, bool, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	standard, err := hujson.Standardize(append(data, '\n'))
	if err != nil {
		return nil, true, fmt.Errorf("%s is not valid JSON: %w", path, err)
	}
	document := map[string]any{}
	if len(bytes.TrimSpace(standard)) != 0 {
		if err := json.Unmarshal(standard, &document); err != nil {
			return nil, true, fmt.Errorf("%s is not a JSON object: %w", path, err)
		}
	}
	return document, true, nil
}

func readJSONPath(path string, keys []string) (any, bool, error) {
	document, _, err := readJSON(path)
	if err != nil || document == nil {
		return nil, false, err
	}
	var current any = document
	for _, key := range keys {
		object, ok := current.(map[string]any)
		if !ok {
			return nil, false, fmt.Errorf("%s: %s is not an object", path, key)
		}
		if current, ok = object[key]; !ok {
			return nil, false, nil
		}
	}
	return current, true, nil
}

// patch applies JSON Patch operations, keeping the file's comments and layout.
func patch(path string, operations []map[string]any) error {
	data, err := os.ReadFile(path)
	fresh := os.IsNotExist(err) || err == nil && len(bytes.TrimSpace(data)) == 0
	if fresh {
		data = []byte("{}\n")
	} else if err != nil {
		return err
	}
	value, err := hujson.Parse(data)
	if err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}
	ops, err := json.Marshal(operations)
	if err != nil {
		return err
	}
	if err := value.Patch(ops); err != nil {
		return fmt.Errorf("update %s: %w", path, err)
	}
	if fresh || !hasComments(data) {
		// a file with no comments is written the way json writes it, two-space indented
		value.Standardize()
		var indented bytes.Buffer
		if err := json.Indent(&indented, value.Pack(), "", "  "); err != nil {
			return err
		}
		return writeFile(path, append(indented.Bytes(), '\n'))
	}
	value.Format()
	return writeFile(path, value.Pack())
}

func hasComments(data []byte) bool {
	value, err := hujson.Parse(data)
	if err != nil {
		return true
	}
	// standardizing blanks out comments and trailing commas, so any change means there were some
	standard := value.Clone()
	standard.Standardize()
	return !bytes.Equal(value.Pack(), standard.Pack())
}

func pointer(keys []string) string {
	escaped := make([]string, len(keys))
	for index, key := range keys {
		escaped[index] = strings.ReplaceAll(strings.ReplaceAll(key, "~", "~0"), "/", "~1")
	}
	return "/" + strings.Join(escaped, "/")
}

// ensureParents adds the missing objects above keys[:len(keys)-1].
func ensureParents(path string, keys []string) error {
	var operations []map[string]any
	for depth := 1; depth < len(keys); depth++ {
		if _, found, err := readJSONPath(path, keys[:depth]); err != nil {
			return err
		} else if !found {
			operations = append(operations, map[string]any{"op": "add", "path": pointer(keys[:depth]), "value": map[string]any{}})
		}
	}
	if len(operations) == 0 {
		return nil
	}
	return patch(path, operations)
}

func setJSONPath(path string, keys []string, value any) error {
	if err := ensureParents(path, keys); err != nil {
		return err
	}
	return patch(path, []map[string]any{{"op": "add", "path": pointer(keys), "value": value}})
}

func appendJSONEntry(path string, keys []string, value any) error {
	if current, found, err := readJSONPath(path, keys); err != nil {
		return err
	} else if !found {
		return setJSONPath(path, keys, []any{value})
	} else if _, ok := current.([]any); !ok {
		return fmt.Errorf("%s: %s is not a list", path, strings.Join(keys, "."))
	}
	return patch(path, []map[string]any{{"op": "add", "path": pointer(keys) + "/-", "value": value}})
}

func removeJSONKey(path string, keys []string) error {
	if _, found, err := readJSONPath(path, keys); err != nil || !found {
		return err
	}
	if err := patch(path, []map[string]any{{"op": "remove", "path": pointer(keys)}}); err != nil {
		return err
	}
	return pruneEmptyJSON(path, keys[:len(keys)-1])
}

func removeJSONEntry(path string, keys []string, raw json.RawMessage) error {
	current, found, err := readJSONPath(path, keys)
	if err != nil || !found {
		return err
	}
	list, ok := current.([]any)
	if !ok {
		return nil
	}
	var want any
	if err := json.Unmarshal(raw, &want); err != nil {
		return err
	}
	for index := len(list) - 1; index >= 0; index-- {
		if sameJSON(list[index], want) {
			if err := patch(path, []map[string]any{{"op": "remove", "path": fmt.Sprintf("%s/%d", pointer(keys), index)}}); err != nil {
				return err
			}
			if len(list) == 1 {
				if err := patch(path, []map[string]any{{"op": "remove", "path": pointer(keys)}}); err != nil {
					return err
				}
				return pruneEmptyJSON(path, keys[:len(keys)-1])
			}
			return nil
		}
	}
	return nil
}

// pruneEmptyJSON removes objects that the removal left empty, innermost first.
func pruneEmptyJSON(path string, keys []string) error {
	for depth := len(keys); depth >= 1; depth-- {
		current, found, err := readJSONPath(path, keys[:depth])
		if err != nil || !found {
			return err
		}
		if object, ok := current.(map[string]any); !ok || len(object) != 0 {
			return nil
		}
		if err := patch(path, []map[string]any{{"op": "remove", "path": pointer(keys[:depth])}}); err != nil {
			return err
		}
	}
	return nil
}

func removeIfEmpty(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	if len(bytes.TrimSpace(data)) != 0 {
		switch filepath.Ext(path) {
		case ".json":
			if document, _, err := readJSON(path); err != nil || len(document) != 0 {
				return false
			}
		case ".toml":
			if config, err := readTOML(path); err != nil || len(config) != 0 {
				return false
			}
		default:
			return false
		}
	}
	return os.Remove(path) == nil
}

func sameJSON(first, second any) bool {
	a, errA := json.Marshal(first)
	b, errB := json.Marshal(second)
	if errA != nil || errB != nil {
		return reflect.DeepEqual(first, second)
	}
	var x, y any
	_ = json.Unmarshal(a, &x)
	_ = json.Unmarshal(b, &y)
	return reflect.DeepEqual(x, y)
}

// ------------------------------------------------------------- TOML files

func readTOML(path string) (map[string]any, error) {
	config := map[string]any{}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return config, nil
	}
	if err != nil {
		return nil, err
	}
	if err := toml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("%s is not valid TOML: %w", path, err)
	}
	return config, nil
}

func readTOMLPath(path string, keys []string) (any, bool, error) {
	config, err := readTOML(path)
	if err != nil {
		return nil, false, err
	}
	var current any = config
	for _, key := range keys {
		object, ok := current.(map[string]any)
		if !ok {
			return nil, false, fmt.Errorf("%s: %s is not a table", path, key)
		}
		if current, ok = object[key]; !ok {
			return nil, false, nil
		}
	}
	return current, true, nil
}

func setTOMLPath(path string, keys []string, value any) error {
	config, err := readTOML(path)
	if err != nil {
		return err
	}
	table := config
	for _, key := range keys[:len(keys)-1] {
		next, ok := table[key].(map[string]any)
		if !ok {
			next = map[string]any{}
			table[key] = next
		}
		table = next
	}
	table[keys[len(keys)-1]] = value
	data, err := toml.Marshal(config)
	if err != nil {
		return err
	}
	return writeFile(path, data)
}

func removeTOMLKey(path string, keys []string) error {
	config, err := readTOML(path)
	if err != nil {
		return err
	}
	tables := []map[string]any{config}
	table := config
	for _, key := range keys[:len(keys)-1] {
		next, ok := table[key].(map[string]any)
		if !ok {
			return nil
		}
		tables = append(tables, next)
		table = next
	}
	delete(table, keys[len(keys)-1])
	for depth := len(tables) - 1; depth >= 1; depth-- {
		if len(tables[depth]) == 0 {
			delete(tables[depth-1], keys[depth-1])
		}
	}
	data, err := toml.Marshal(config)
	if err != nil {
		return err
	}
	return writeFile(path, data)
}

// ------------------------------------------------------------- AGENTS.md block

func insertBlock(path, body string) error {
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	text := string(data)
	if text != "" && !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	if text != "" {
		text += "\n"
	}
	text += blockBegin + "\n" + body + "\n" + blockEnd + "\n"
	return writeFile(path, []byte(text))
}

func removeBlock(path string) error {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	text := string(data)
	begin, end := strings.Index(text, blockBegin), strings.Index(text, blockEnd)
	if begin < 0 || end < begin {
		return nil
	}
	before := strings.TrimRight(text[:begin], "\n")
	after := strings.TrimLeft(text[end+len(blockEnd):], "\n")
	text = before
	if before != "" && after != "" {
		text += "\n\n"
	} else if before != "" {
		text += "\n"
	}
	text += after
	return os.WriteFile(path, []byte(text), 0o644)
}

func sortedKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
