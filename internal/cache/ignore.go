package cache

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/go-git/go-git/v5/plumbing/format/gitignore"
)

func nativeIgnoredFiles(root string, files []string) (map[string]struct{}, error) {
	matcher, base, err := loadIgnoreMatcher(root)
	if err != nil {
		return nil, err
	}
	ignored := make(map[string]struct{})
	for _, relative := range files {
		fromBase, err := filepath.Rel(base, filepath.Join(root, relative))
		if err != nil {
			return nil, err
		}
		if matcher.Match(splitGitPath(fromBase), false) {
			ignored[filepath.Clean(relative)] = struct{}{}
		}
	}
	return ignored, nil
}

func loadIgnoreMatcher(root string) (gitignore.Matcher, string, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, "", err
	}
	base := findGitRoot(root)
	ignoreFiles, err := findIgnoreFiles(root, base)
	if err != nil {
		return nil, "", err
	}
	patterns := make([]gitignore.Pattern, 0)
	for _, ignoreFile := range ignoreFiles {
		domain, err := filepath.Rel(base, filepath.Dir(ignoreFile))
		if err != nil {
			return nil, "", err
		}
		parsed, err := parseIgnoreFile(ignoreFile, splitGitPath(domain))
		if err != nil {
			return nil, "", err
		}
		patterns = append(patterns, parsed...)
	}
	return gitignore.NewMatcher(patterns), base, nil
}

func findGitRoot(root string) string {
	for directory := root; ; directory = filepath.Dir(directory) {
		if _, err := os.Stat(filepath.Join(directory, ".git")); err == nil {
			return directory
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			return root
		}
	}
}

func findIgnoreFiles(root, base string) ([]string, error) {
	var ignoreFiles []string
	for directory := root; ; directory = filepath.Dir(directory) {
		candidate := filepath.Join(directory, ".gitignore")
		if regularFile(candidate) {
			ignoreFiles = append(ignoreFiles, candidate)
		}
		if directory == base {
			break
		}
	}
	for left, right := 0, len(ignoreFiles)-1; left < right; left, right = left+1, right-1 {
		ignoreFiles[left], ignoreFiles[right] = ignoreFiles[right], ignoreFiles[left]
	}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() && entry.Name() == ".git" {
			return filepath.SkipDir
		}
		if !entry.IsDir() && entry.Name() == ".gitignore" && path != filepath.Join(root, ".gitignore") {
			ignoreFiles = append(ignoreFiles, path)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("discover .gitignore files: %w", err)
	}
	sort.SliceStable(ignoreFiles, func(i, j int) bool {
		return pathDepth(ignoreFiles[i]) < pathDepth(ignoreFiles[j])
	})
	return ignoreFiles, nil
}

func parseIgnoreFile(ignoreFile string, domain []string) (_ []gitignore.Pattern, returnErr error) {
	file, err := os.Open(ignoreFile)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", ignoreFile, err)
	}
	defer func() {
		if err := file.Close(); err != nil && returnErr == nil {
			returnErr = fmt.Errorf("close %s: %w", ignoreFile, err)
		}
	}()
	var patterns []gitignore.Pattern
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "#") || strings.TrimSpace(line) == "" {
			continue
		}
		patterns = append(patterns, gitignore.ParsePattern(line, domain))
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read %s: %w", ignoreFile, err)
	}
	return patterns, nil
}

func splitGitPath(value string) []string {
	value = filepath.ToSlash(filepath.Clean(value))
	if value == "." {
		return nil
	}
	return strings.Split(value, "/")
}

func pathDepth(value string) int {
	return strings.Count(filepath.Clean(value), string(filepath.Separator))
}
