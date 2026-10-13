package staging

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/OlegHQ/agentpack/internal/artifacts"
	"github.com/OlegHQ/agentpack/internal/cache"
)

// packageFile is one file of a cached package as staging sees it. Virtual is
// where the file sits in the package's canonical layout (skills/<name>/…,
// commands/…); Real is its actual path in the cache entry, which mode
// selectors are written against. The two differ for a file reached through a
// symbolic link or through a component path a plugin manifest declares.
type packageFile struct {
	Path    string
	Virtual string
	Real    string
}

// componentDirs are the directories that hold artifacts in every plugin format.
var componentDirs = []string{"skills", "commands", "agents", "rules"}

// maxFollowDepth bounds how deep a followed directory may nest.
const maxFollowDepth = 32

type packageWalk struct {
	root   string
	module string
	files  []packageFile
	seen   map[string]bool
	// open holds the directories being followed, so a link back to one of
	// them is not followed again.
	open map[string]bool
}

// packageFiles lists the files of a cache entry. A plugin gets its tree as it
// is, the symbolic links under its component directories followed, and the
// component paths its manifests declare; a bare skill gets its whole tree with
// links followed. A link or a declared path that leaves the package is skipped
// with a warning that names it.
func packageFiles(sourceRoot, module string, plugin bool) ([]packageFile, error) {
	root, err := filepath.EvalSymlinks(sourceRoot)
	if err != nil {
		return nil, err
	}
	walk := &packageWalk{root: root, module: module, seen: make(map[string]bool), open: make(map[string]bool)}
	err = filepath.WalkDir(root, func(current string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		relative, err := filepath.Rel(root, current)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		if entry.Type()&os.ModeSymlink != 0 {
			if plugin && !underComponentDir(relative) {
				return nil
			}
			walk.follow(current, relative, 0)
			return nil
		}
		walk.add(current, relative)
		return nil
	})
	if err != nil {
		return nil, err
	}
	if plugin {
		walk.declared()
	}
	sort.SliceStable(walk.files, func(i, j int) bool { return walk.files[i].Virtual < walk.files[j].Virtual })
	return walk.files, nil
}

func underComponentDir(relative string) bool {
	parts := strings.Split(artifacts.StripHarnessPrefix(relative), "/")
	for _, directory := range componentDirs {
		if len(parts) > 1 && parts[0] == directory {
			return true
		}
	}
	return false
}

func (walk *packageWalk) warn(format string, arguments ...any) {
	log.Printf("warning: %s: %s", walk.module, fmt.Sprintf(format, arguments...))
}

func (walk *packageWalk) add(absolute, virtual string) {
	real, err := filepath.Rel(walk.root, absolute)
	if err != nil {
		return
	}
	key := virtual + "\x00" + absolute
	if walk.seen[key] {
		return
	}
	walk.seen[key] = true

	walk.files = append(walk.files, packageFile{Path: absolute, Virtual: virtual, Real: filepath.ToSlash(real)})
}

// follow adds what a symbolic link or directory at path holds under the
// virtual prefix, following further links while they stay inside the package.
func (walk *packageWalk) follow(current, virtual string, depth int) {
	resolved, err := filepath.EvalSymlinks(current)
	if err != nil {
		walk.warn("skipping %s: its link target does not exist in the package", virtual)
		return
	}
	if !cache.Inside(walk.root, resolved) {
		walk.warn("skipping %s: it links outside the package", virtual)
		return
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return
	}
	if !info.IsDir() {
		if info.Mode().IsRegular() {
			walk.add(resolved, virtual)
		}
		return
	}
	if walk.open[resolved] {
		walk.warn("skipping %s: it links back to a directory that contains it", virtual)
		return
	}
	if depth >= maxFollowDepth {
		walk.warn("skipping %s: it is nested more than %d directories deep", virtual, maxFollowDepth)
		return
	}
	entries, err := os.ReadDir(resolved)
	if err != nil {
		return
	}
	walk.open[resolved] = true
	defer delete(walk.open, resolved)
	for _, entry := range entries {
		if entry.Name() == ".git" {
			continue
		}
		child, childVirtual := filepath.Join(resolved, entry.Name()), path.Join(virtual, entry.Name())
		switch {
		case entry.Type()&os.ModeSymlink != 0 || entry.IsDir():
			walk.follow(child, childVirtual, depth+1)
		case entry.Type().IsRegular():
			walk.add(child, childVirtual)
		}
	}
}

// declared adds the component paths the plugin manifests name. Claude Code,
// Cursor and Codex each let plugin.json point skills, commands, agents (and,
// for Cursor, rules) at paths other than the default directories; the
// declared paths are staged in addition to the defaults.
func (walk *packageWalk) declared() {
	for _, manifest := range cache.ReadPluginManifests(walk.root) {
		for _, component := range componentDirs {
			paths, inline := manifest.Declared(component)
			if len(inline) != 0 {
				walk.warn("skipping %s %s: definitions written inline in the manifest are not staged, only paths", manifest.Relative, component)
			}
			for _, declared := range paths {
				for _, resolved := range walk.resolveDeclared(manifest.Relative, component, declared) {
					walk.component(component, resolved, manifest)
				}
			}
		}
	}
}

func (walk *packageWalk) resolveDeclared(manifest, component, declared string) []string {
	candidates := []string{declared}
	if strings.ContainsAny(declared, "*?[") {
		clean := strings.TrimPrefix(strings.ReplaceAll(declared, "\\", "/"), "./")
		if strings.HasPrefix(clean, "/") || strings.Contains(clean, "..") {
			walk.warn("skipping %s %s %q: the pattern leaves the package", manifest, component, declared)
			return nil
		}
		matches, err := filepath.Glob(filepath.Join(walk.root, filepath.FromSlash(clean)))
		if err != nil || len(matches) == 0 {
			walk.warn("skipping %s %s %q: the pattern matches nothing in the package", manifest, component, declared)
			return nil
		}
		candidates = candidates[:0]
		for _, match := range matches {
			if relative, err := filepath.Rel(walk.root, match); err == nil {
				candidates = append(candidates, filepath.ToSlash(relative))
			}
		}
	}
	var resolved []string
	for _, candidate := range candidates {
		target, err := cache.ResolveInside(walk.root, candidate)
		if err != nil {
			walk.warn("skipping %s %s: %v", manifest, component, err)
			continue
		}
		resolved = append(resolved, target)
	}
	return resolved
}

func (walk *packageWalk) component(component, resolved string, manifest cache.PluginManifest) {
	info, err := os.Stat(resolved)
	if err != nil {
		return
	}
	if !info.IsDir() {
		// A single command, agent or rule file.
		if component != "skills" {
			walk.add(resolved, component+"/"+filepath.Base(resolved))
		}
		return
	}
	if component != "skills" {
		walk.follow(resolved, component, 0)
		return
	}
	// A skills path is a directory of skill directories, or one skill.
	if regular(filepath.Join(resolved, "SKILL.md")) {
		name := filepath.Base(resolved)
		if resolved == walk.root {
			// `"skills": "."`: the plugin itself is one skill, named after the plugin.
			if json.Unmarshal(manifest.Fields["name"], &name) != nil || name == "" {
				walk.warn("skipping %s skills \".\": the manifest has no name to stage the skill under", manifest.Relative)
				return
			}
		}
		walk.follow(resolved, "skills/"+name, 0)
		return
	}
	entries, err := os.ReadDir(resolved)
	if err != nil {
		return
	}
	for _, entry := range entries {
		child, err := filepath.EvalSymlinks(filepath.Join(resolved, entry.Name()))
		if err != nil {
			if entry.Type()&os.ModeSymlink != 0 {
				walk.warn("skipping skill %s: its link target does not exist in the package", entry.Name())
			}
			continue
		}
		if !cache.Inside(walk.root, child) {
			walk.warn("skipping skill %s: it links outside the package", entry.Name())
			continue
		}
		if regular(filepath.Join(child, "SKILL.md")) {
			walk.follow(child, "skills/"+entry.Name(), 0)
		}
	}
}

func regular(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}
