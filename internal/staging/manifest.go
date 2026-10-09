package staging

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"

	base "github.com/OlegHQ/agentpack/internal/harness"
	"github.com/OlegHQ/agentpack/internal/harness/registry"
	"github.com/OlegHQ/agentpack/internal/paths"
)

// stagedContentDirs and stagedContentFiles are the parts of every harness
// root that hold what agentpack staged from packages: artifacts and the MCP
// configuration. The rest of a root (credentials, history, harness settings)
// is written by the harness while it runs and is not recorded.
var (
	stagedContentDirs  = []string{"skills", "commands", "agents", "rules", "hooks"}
	stagedContentFiles = []string{".mcp.json", "mcp.json", "mcp_config.json"}
)

// stagedEntry is one staged file as the last sync left it. Project marks a
// hard link to a file under the project's ./.agents/: that file belongs to the
// user, and editing it in place is not a change to staging.
type stagedEntry struct {
	Kind    string `json:"kind"`
	Digest  string `json:"digest"`
	Size    int64  `json:"size"`
	ModTime int64  `json:"mtime"`
	Project bool   `json:"project,omitempty"`
}

type stagedManifest map[base.Target]map[string]stagedEntry

// DriftError lists staged files that differ from what the last sync wrote.
type DriftError struct{ Changes []string }

func (err *DriftError) Error() string {
	return fmt.Sprintf("staged tree does not match the last sync: %s; run `agentpack sync` to rebuild it from the verified cache", strings.Join(err.Changes, "; "))
}

func (err *DriftError) Details() string {
	return "staged tree does not match the last sync\n  " + strings.Join(err.Changes, "\n  ") + "\n  fix       agentpack sync   (rebuilds staging from the verified cache and reports what it replaced)\n"
}

func (err *DriftError) Summary() string {
	return fmt.Sprintf("staged tree does not match the last sync: %d file(s) differ", len(err.Changes))
}

// RecordStaged saves the manifest of the staged content as it is now.
func (pipeline Pipeline) RecordStaged() error {
	return pipeline.recordStaged(pipeline.context())
}

func (pipeline Pipeline) recordStaged(ctx base.StageContext) error {
	manifest, err := pipeline.snapshotStaged(ctx, true)
	if err != nil {
		return err
	}
	path, err := paths.StagedManifestPath(pipeline.ProjectRoot, pipeline.Mode.Name())
	if err != nil {
		return err
	}
	data, err := json.Marshal(manifest)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// StagedDrift compares the staged content with the manifest of the last sync
// and describes every file that was modified, added or removed since, by its
// path. recorded is false when no sync has left a manifest yet.
//
// With full set it hashes every staged file and also reports a harness root
// that is gone; that is the check behind `sync --verify-only`. Without it, it
// compares kind, size and modification time only and skips roots that no
// longer exist: enough for a sync to say what it is about to replace, since
// the rebuild that follows does not depend on the answer.
func (pipeline Pipeline) StagedDrift(full bool) (changes []string, recorded bool, err error) {
	path, err := paths.StagedManifestPath(pipeline.ProjectRoot, pipeline.Mode.Name())
	if err != nil {
		return nil, false, err
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	var expected stagedManifest
	if err := json.Unmarshal(data, &expected); err != nil {
		return nil, false, nil
	}
	ctx := pipeline.context()
	actual, err := pipeline.snapshotStaged(ctx, full)
	if err != nil {
		return nil, true, err
	}
	for _, candidate := range registry.All() {
		root, err := candidate.StagedRoot(ctx)
		if _, statErr := os.Stat(root); err != nil || statErr != nil {
			if full && len(expected[candidate.ID()]) != 0 {
				changes = append(changes, fmt.Sprintf("missing   the staged %s root", candidate.ID()))
			}
			continue
		}
		before, after := expected[candidate.ID()], actual[candidate.ID()]
		for relative, entry := range before {
			current, found := after[relative]
			file := filepath.Join(root, filepath.FromSlash(relative))
			switch {
			case !found:
				changes = append(changes, "missing   "+file)
			case current.Project && entry.Project:
			case current.Kind != entry.Kind || current.Size != entry.Size ||
				full && current.Digest != entry.Digest || !full && current.ModTime != entry.ModTime:
				changes = append(changes, "modified  "+file)
			}
		}
		for relative := range after {
			if _, found := before[relative]; !found {
				changes = append(changes, "added     "+filepath.Join(root, filepath.FromSlash(relative)))
			}
		}
	}
	sort.Strings(changes)
	return changes, true, nil
}

// snapshotStaged lists the staged content of every harness root that exists.
// Digests are computed only when hash is set, several files at a time: the
// harness roots together hold about six copies of the package content.
func (pipeline Pipeline) snapshotStaged(ctx base.StageContext, hash bool) (stagedManifest, error) {
	project, err := projectFiles(paths.ProjectDotAgentsDir(pipeline.ProjectRoot))
	if err != nil {
		return nil, err
	}
	type staged struct {
		target   base.Target
		relative string
		path     string
		info     fs.FileInfo
	}
	var files []staged
	manifest := make(stagedManifest)
	for _, candidate := range registry.All() {
		root, err := candidate.StagedRoot(ctx)
		if err != nil {
			continue
		}
		manifest[candidate.ID()] = make(map[string]stagedEntry)
		add := func(path string, info fs.FileInfo) error {
			relative, err := filepath.Rel(root, path)
			files = append(files, staged{candidate.ID(), filepath.ToSlash(relative), path, info})
			return err
		}
		for _, name := range stagedContentFiles {
			path := filepath.Join(root, name)
			if info, err := os.Lstat(path); err == nil && !info.IsDir() {
				if err := add(path, info); err != nil {
					return nil, err
				}
			}
		}
		for _, name := range stagedContentDirs {
			directory := filepath.Join(root, name)
			if info, err := os.Lstat(directory); err != nil || !info.IsDir() {
				continue
			}
			err := filepath.WalkDir(directory, func(path string, entry fs.DirEntry, walkErr error) error {
				if walkErr != nil || entry.IsDir() {
					return walkErr
				}
				info, err := entry.Info()
				if err != nil {
					return err
				}
				return add(path, info)
			})
			if err != nil {
				return nil, fmt.Errorf("walk staged tree %s: %w", directory, err)
			}
		}
	}
	entries := make([]stagedEntry, len(files))
	failures := make([]error, len(files))
	var group sync.WaitGroup
	next := make(chan int)
	for range min(runtime.GOMAXPROCS(0), 8) {
		group.Add(1)
		go func() {
			defer group.Done()
			for index := range next {
				entries[index], failures[index] = stagedFile(files[index].path, files[index].info, hash)
			}
		}()
	}
	for index := range files {
		next <- index
	}
	close(next)
	group.Wait()
	for index, file := range files {
		if failures[index] != nil {
			return nil, failures[index]
		}
		entry := entries[index]
		if file.info.Mode().IsRegular() {
			for _, candidate := range project[file.info.Size()] {
				if os.SameFile(file.info, candidate) {
					entry.Project = true
					break
				}
			}
		}
		manifest[file.target][file.relative] = entry
	}
	return manifest, nil
}

func stagedFile(path string, info fs.FileInfo, hash bool) (stagedEntry, error) {
	entry := stagedEntry{Kind: "f", Size: info.Size(), ModTime: info.ModTime().UnixNano()}
	switch {
	case info.Mode()&fs.ModeSymlink != 0:
		target, err := os.Readlink(path)
		entry.Kind, entry.Digest = "l", target
		return entry, err
	case !info.Mode().IsRegular():
		entry.Kind = info.Mode().Type().String()
		return entry, nil
	case info.Mode().Perm()&0o111 != 0:
		entry.Kind = "x"
	}
	if !hash {
		return entry, nil
	}
	file, err := os.Open(path)
	if err != nil {
		return entry, err
	}
	defer file.Close()
	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return entry, fmt.Errorf("read %s: %w", path, err)
	}
	entry.Digest = hex.EncodeToString(hasher.Sum(nil))
	return entry, nil
}

// projectFiles indexes the regular files under ./.agents/ by size, to find
// the staged files that are hard links to them.
func projectFiles(root string) (map[int64][]fs.FileInfo, error) {
	files := make(map[int64][]fs.FileInfo)
	if info, err := os.Stat(root); err != nil || !info.IsDir() {
		return files, nil
	}
	err := filepath.WalkDir(root, func(_ string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() {
			return walkErr
		}
		info, err := entry.Info()
		if err == nil && info.Mode().IsRegular() {
			files[info.Size()] = append(files[info.Size()], info)
		}
		return err
	})
	return files, err
}
