package cache

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/OlegHQ/agentpack/internal/paths"
)

var marketplaceManifestPaths = []string{
	filepath.Join(".claude-plugin", "marketplace.json"),
	filepath.Join(".cursor-plugin", "marketplace.json"),
	filepath.Join(".agents", "plugins", "marketplace.json"),
}

func ComputeKey(identity string) string {
	sum := sha256.Sum256([]byte(identity))
	return hex.EncodeToString(sum[:])
}

func EntryDir(cacheKey string) (string, error) {
	root, err := paths.CacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, cacheKey), nil
}

func ClaudePluginManifestPath(root string) string {
	return filepath.Join(root, ".claude-plugin", "plugin.json")
}

func CursorPluginManifestPath(root string) string {
	return filepath.Join(root, ".cursor-plugin", "plugin.json")
}

func CodexPluginManifestPath(root string) string {
	return filepath.Join(root, ".codex-plugin", "plugin.json")
}

func HasPluginManifest(root string) bool {
	return regularFile(ClaudePluginManifestPath(root)) ||
		regularFile(CursorPluginManifestPath(root)) ||
		regularFile(CodexPluginManifestPath(root))
}

func IsPackageRoot(root string) bool {
	return regularFile(filepath.Join(root, "SKILL.md")) ||
		HasPluginManifest(root) ||
		regularFile(filepath.Join(root, paths.ManifestName)) ||
		hasMarketplaceManifest(root)
}

func RepoDirIsPackageRoot(relativePaths map[string]struct{}, directory string) bool {
	directory = strings.Trim(strings.ReplaceAll(directory, "\\", "/"), "/")
	join := func(leaf string) string {
		leaf = strings.ReplaceAll(leaf, "\\", "/")
		if directory == "" {
			return leaf
		}
		return directory + "/" + leaf
	}
	for _, leaf := range []string{
		".claude-plugin/plugin.json", ".cursor-plugin/plugin.json", ".codex-plugin/plugin.json",
		"SKILL.md", paths.ManifestName,
		".claude-plugin/marketplace.json", ".cursor-plugin/marketplace.json", ".agents/plugins/marketplace.json",
	} {
		if _, exists := relativePaths[join(leaf)]; exists {
			return true
		}
	}
	return false
}

func EnsurePlugin(root string) error {
	if !HasPluginManifest(root) {
		return fmt.Errorf("missing plugin manifest in %s", root)
	}
	return nil
}

// SourceFiles returns sorted regular files without following symlinks. In a
// Git worktree it asks Git to apply repository, parent, info, global, and
// system excludes in one batch, including for tracked files via --no-index.
func SourceFiles(root string) ([]string, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve source root %s: %w", root, err)
	}
	// Canonicalize platform aliases before comparing this path with Git's
	// worktree root. macOS commonly exposes /var through the /private/var
	// symlink while `git rev-parse` reports the canonical spelling.
	if canonical, evalErr := filepath.EvalSymlinks(root); evalErr == nil {
		root = canonical
	}
	var files []string
	err = filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if path != root && entry.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		files = append(files, relative)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk source tree %s: %w", root, err)
	}
	ignored, available, err := gitIgnoredFiles(root, files)
	if err != nil {
		return nil, err
	}
	if available {
		files = filterIgnored(files, ignored)
	} else {
		ignored, err := nativeIgnoredFiles(root, files)
		if err != nil {
			return nil, err
		}
		files = filterIgnored(files, ignored)
	}
	sort.Strings(files)
	return files, nil
}

func filterIgnored(files []string, ignored map[string]struct{}) []string {
	kept := files[:0]
	for _, file := range files {
		if _, excluded := ignored[filepath.Clean(file)]; !excluded {
			kept = append(kept, file)
		}
	}
	return kept
}

func hashAndCopySourceTree(source, destination string) (string, error) {
	files, err := SourceFiles(source)
	if err != nil {
		return "", err
	}
	hasher := sha256.New()
	buffer := make([]byte, 32*1024)
	for _, relative := range files {
		hasher.Write([]byte(relative))
		hasher.Write([]byte{0})
		sourcePath := filepath.Join(source, relative)
		destinationPath := filepath.Join(destination, relative)
		if err := os.MkdirAll(filepath.Dir(destinationPath), 0o755); err != nil {
			return "", fmt.Errorf("create %s: %w", filepath.Dir(destinationPath), err)
		}
		input, err := os.Open(sourcePath)
		if err != nil {
			return "", fmt.Errorf("open %s: %w", sourcePath, err)
		}
		output, err := os.OpenFile(destinationPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
		if err != nil {
			input.Close()
			return "", fmt.Errorf("create %s: %w", destinationPath, err)
		}
		writer := io.MultiWriter(hasher, output)
		_, copyErr := io.CopyBuffer(writer, input, buffer)
		closeInputErr := input.Close()
		closeOutputErr := output.Close()
		if copyErr != nil {
			return "", fmt.Errorf("copy %s: %w", sourcePath, copyErr)
		}
		if closeInputErr != nil {
			return "", fmt.Errorf("close %s: %w", sourcePath, closeInputErr)
		}
		if closeOutputErr != nil {
			return "", fmt.Errorf("close %s: %w", destinationPath, closeOutputErr)
		}
	}
	return hex.EncodeToString(hasher.Sum(nil))[:40], nil
}

func gitIgnoredFiles(root string, files []string) (map[string]struct{}, bool, error) {
	command := exec.Command("git", "-C", root, "rev-parse", "--show-toplevel")
	repositoryBytes, err := command.Output()
	if err != nil {
		var executableErr *exec.Error
		if errors.As(err, &executableErr) || isExitError(err) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("locate Git worktree for %s: %w", root, err)
	}
	repository := strings.TrimSpace(string(repositoryBytes))
	rootRelative, err := filepath.Rel(repository, root)
	if err != nil {
		return nil, false, fmt.Errorf("resolve %s under Git worktree %s: %w", root, repository, err)
	}
	var input bytes.Buffer
	for _, file := range files {
		candidate := file
		if rootRelative != "." {
			candidate = filepath.Join(rootRelative, file)
		}
		input.WriteString(filepath.ToSlash(candidate))
		input.WriteByte(0)
	}
	check := exec.Command("git", "-C", repository, "check-ignore", "--no-index", "-z", "--stdin")
	check.Stdin = &input
	output, err := check.Output()
	if err != nil && !isExitCode(err, 1) {
		return nil, false, fmt.Errorf("apply Git ignore rules in %s: %w", repository, err)
	}
	ignored := make(map[string]struct{})
	scanner := bufio.NewScanner(bytes.NewReader(output))
	scanner.Split(splitNull)
	for scanner.Scan() {
		candidate := filepath.FromSlash(scanner.Text())
		if rootRelative != "." {
			candidate, err = filepath.Rel(rootRelative, candidate)
			if err != nil {
				return nil, false, err
			}
		}
		ignored[filepath.Clean(candidate)] = struct{}{}
	}
	if err := scanner.Err(); err != nil {
		return nil, false, fmt.Errorf("read ignored paths: %w", err)
	}
	return ignored, true, nil
}

func splitNull(data []byte, atEOF bool) (advance int, token []byte, err error) {
	if index := bytes.IndexByte(data, 0); index >= 0 {
		return index + 1, data[:index], nil
	}
	if atEOF && len(data) != 0 {
		return len(data), data, nil
	}
	return 0, nil, nil
}

func isExitError(err error) bool {
	var exitErr *exec.ExitError
	return errors.As(err, &exitErr)
}

func isExitCode(err error, code int) bool {
	var exitErr *exec.ExitError
	return errors.As(err, &exitErr) && exitErr.ExitCode() == code
}

func hasMarketplaceManifest(root string) bool {
	for _, relative := range marketplaceManifestPaths {
		if regularFile(filepath.Join(root, relative)) {
			return true
		}
	}
	return false
}

func regularFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}
