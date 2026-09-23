package staging

import (
	"crypto/sha256"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/OlegHQ/agentpack/internal/paths"
)

// ProjectClaudeSkillNames returns the case-insensitive names of skills Claude
// Code discovers directly from the project.
func ProjectClaudeSkillNames(projectRoot string) (map[string]struct{}, error) {
	names := make(map[string]struct{})
	dirs, err := projectClaudeSkillDirs(projectRoot)
	if err != nil {
		return nil, err
	}
	for name := range dirs {
		names[name] = struct{}{}
	}
	return names, nil
}

// OmitProjectClaudeSkillDuplicates removes staged plugin skills whose names
// are already provided by project .claude/skills. Claude Code discovers both
// locations, and the project definition wins on a name collision. Full-tree
// hashes distinguish exact mirrored copies from same-name overrides while
// preserving different skill names even when their contents match.
func OmitProjectClaudeSkillDuplicates(projectRoot, bundle string) error {
	localRoot := filepath.Join(paths.ProjectDotClaudeDir(projectRoot), "skills")
	localNames, err := projectClaudeSkillDirs(projectRoot)
	if err != nil {
		return err
	}
	bundleRoot := filepath.Join(bundle, "skills")
	entries, err := os.ReadDir(bundleRoot)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		localName, duplicate := localNames[strings.ToLower(entry.Name())]
		if !duplicate {
			continue
		}
		localSkill := filepath.Join(localRoot, localName)
		bundleSkill := filepath.Join(bundleRoot, entry.Name())
		localHash, localErr := skillTreeHash(localSkill)
		bundleHash, bundleErr := skillTreeHash(bundleSkill)
		if localErr != nil && !os.IsNotExist(localErr) {
			return localErr
		}
		if bundleErr != nil {
			return bundleErr
		}
		if err := os.RemoveAll(bundleSkill); err != nil {
			return err
		}
		kind := "same-name project skill"
		if localErr == nil && localHash == bundleHash {
			kind = "identical project skill"
		}
		fmt.Fprintf(os.Stderr, "using %s `%s`; omitted plugin duplicate from Claude bundle\n", kind, entry.Name())
	}
	return nil
}

func projectClaudeSkillDirs(projectRoot string) (map[string]string, error) {
	dirs := make(map[string]string)
	root := filepath.Join(paths.ProjectDotClaudeDir(projectRoot), "skills")
	entries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return dirs, nil
	}
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		info, err := os.Stat(filepath.Join(root, entry.Name(), "SKILL.md"))
		if err == nil && info.Mode().IsRegular() {
			dirs[strings.ToLower(entry.Name())] = entry.Name()
		} else if err != nil && !os.IsNotExist(err) {
			return nil, err
		}
	}
	return dirs, nil
}

func skillTreeHash(root string) ([sha256.Size]byte, error) {
	h := sha256.New()
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if _, err := fmt.Fprintf(h, "%s\x00", filepath.ToSlash(relative)); err != nil {
			return err
		}
		contents, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if _, err := fmt.Fprintf(h, "%d\x00", len(contents)); err != nil {
			return err
		}
		_, err = h.Write(contents)
		return err
	})
	if err != nil {
		return [sha256.Size]byte{}, err
	}
	var digest [sha256.Size]byte
	copy(digest[:], h.Sum(nil))
	return digest, nil
}

// ClaudeSkillsUpdate records a local skill whose two copies had diverged and
// were reconciled to the newer side.
type ClaudeSkillsUpdate struct {
	Name   string
	Winner string // "claude" or "agents"
}

// ClaudeSkillsReport summarizes a reconciliation between a project's
// .claude/skills and .agents/skills directories.
type ClaudeSkillsReport struct {
	CopiedToClaude []string
	CopiedToAgents []string
	Updated        []ClaudeSkillsUpdate
	InSync         []string
}

// ReconcileClaudeSkills makes a project's .claude/skills and .agents/skills
// directories hold the same local skills. Claude Code only discovers
// project-local skills under .claude/skills, while the dot-agents convention
// shares project-local content across every harness under .agents/skills;
// a skill authored under either one is copied so both sides end up identical.
// When a skill exists on both sides with differing content, the copy with the
// newer file modification time wins. dryRun reports what would change without
// touching the filesystem.
func ReconcileClaudeSkills(projectRoot string, dryRun bool) (ClaudeSkillsReport, error) {
	agentsRoot := filepath.Join(paths.ProjectDotAgentsDir(projectRoot), "skills")
	claudeRoot := filepath.Join(paths.ProjectDotClaudeDir(projectRoot), "skills")

	agentsNames, err := localSkillNames(agentsRoot)
	if err != nil {
		return ClaudeSkillsReport{}, err
	}
	claudeNames, err := localSkillNames(claudeRoot)
	if err != nil {
		return ClaudeSkillsReport{}, err
	}

	names := make(map[string]struct{}, len(agentsNames)+len(claudeNames))
	for name := range agentsNames {
		names[name] = struct{}{}
	}
	for name := range claudeNames {
		names[name] = struct{}{}
	}
	sorted := make([]string, 0, len(names))
	for name := range names {
		sorted = append(sorted, name)
	}
	sort.Strings(sorted)

	var report ClaudeSkillsReport
	for _, name := range sorted {
		_, inAgents := agentsNames[name]
		_, inClaude := claudeNames[name]
		agentsDir := filepath.Join(agentsRoot, name)
		claudeDir := filepath.Join(claudeRoot, name)

		switch {
		case inAgents && !inClaude:
			report.CopiedToClaude = append(report.CopiedToClaude, name)
			if !dryRun {
				if err := replaceDirectoryTree(agentsDir, claudeDir); err != nil {
					return report, err
				}
			}
		case inClaude && !inAgents:
			report.CopiedToAgents = append(report.CopiedToAgents, name)
			if !dryRun {
				if err := replaceDirectoryTree(claudeDir, agentsDir); err != nil {
					return report, err
				}
			}
		default:
			identical, err := directoryTreesEqual(agentsDir, claudeDir)
			if err != nil {
				return report, err
			}
			if identical {
				report.InSync = append(report.InSync, name)
				continue
			}
			agentsModified, err := latestModTime(agentsDir)
			if err != nil {
				return report, err
			}
			claudeModified, err := latestModTime(claudeDir)
			if err != nil {
				return report, err
			}
			winner, source, destination := "agents", agentsDir, claudeDir
			if claudeModified.After(agentsModified) {
				winner, source, destination = "claude", claudeDir, agentsDir
			}
			report.Updated = append(report.Updated, ClaudeSkillsUpdate{Name: name, Winner: winner})
			if !dryRun {
				if err := replaceDirectoryTree(source, destination); err != nil {
					return report, err
				}
			}
		}
	}
	return report, nil
}

// localSkillNames lists immediate subdirectories of root that contain a
// SKILL.md, the on-disk shape of a local skill.
func localSkillNames(root string) (map[string]struct{}, error) {
	names := make(map[string]struct{})
	entries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return names, nil
	}
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if info, err := os.Stat(filepath.Join(root, entry.Name(), "SKILL.md")); err == nil && info.Mode().IsRegular() {
			names[entry.Name()] = struct{}{}
		}
	}
	return names, nil
}

// replaceDirectoryTree makes destination an exact copy of source, removing
// any destination files that no longer exist in source.
func replaceDirectoryTree(source, destination string) error {
	if err := os.RemoveAll(destination); err != nil {
		return fmt.Errorf("remove %s: %w", destination, err)
	}
	return filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		target := filepath.Join(destination, relative)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		return copyFile(path, target)
	})
}

// directoryTreesEqual reports whether two directory trees contain the same
// relative files with identical contents.
func directoryTreesEqual(left, right string) (bool, error) {
	leftFiles, err := fileContentsByRelativePath(left)
	if err != nil {
		return false, err
	}
	rightFiles, err := fileContentsByRelativePath(right)
	if err != nil {
		return false, err
	}
	if len(leftFiles) != len(rightFiles) {
		return false, nil
	}
	for relative, contents := range leftFiles {
		other, found := rightFiles[relative]
		if !found || string(other) != string(contents) {
			return false, nil
		}
	}
	return true, nil
}

func fileContentsByRelativePath(root string) (map[string][]byte, error) {
	files := make(map[string][]byte)
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		files[filepath.ToSlash(relative)] = data
		return nil
	})
	if err != nil {
		return nil, err
	}
	return files, nil
}

// latestModTime returns the newest modification time of any regular file
// under root, used to decide which side of a diverged skill wins.
func latestModTime(root string) (time.Time, error) {
	var latest time.Time
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.ModTime().After(latest) {
			latest = info.ModTime()
		}
		return nil
	})
	return latest, err
}
