package cache

import (
	"bufio"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

type ignoreRule struct {
	domain    string
	segments  []string
	negated   bool
	directory bool
	basename  bool
}

func nativeIgnoredFiles(root string, files []string) (map[string]struct{}, error) {
	rules, err := loadIgnoreRules(root)
	if err != nil {
		return nil, err
	}
	ignored := make(map[string]struct{})
	for _, relative := range files {
		absolute := filepath.Join(root, relative)
		excluded := false
		for _, rule := range rules {
			matched, err := rule.matches(absolute)
			if err != nil {
				return nil, err
			}
			if matched {
				excluded = !rule.negated
			}
		}
		if excluded {
			ignored[filepath.Clean(relative)] = struct{}{}
		}
	}
	return ignored, nil
}

func loadIgnoreRules(root string) ([]ignoreRule, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	base := root
	for directory := root; ; directory = filepath.Dir(directory) {
		if info, err := os.Stat(filepath.Join(directory, ".git")); err == nil && info.IsDir() {
			base = directory
			break
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			break
		}
	}
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
	err = filepath.WalkDir(root, func(filePath string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() && entry.Name() == ".git" {
			return filepath.SkipDir
		}
		if !entry.IsDir() && entry.Name() == ".gitignore" && filePath != filepath.Join(root, ".gitignore") {
			ignoreFiles = append(ignoreFiles, filePath)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("discover .gitignore files: %w", err)
	}
	// WalkDir is lexical, not depth-first priority across sibling domains. Sorting
	// by depth guarantees parent rules precede child rules; sibling order is
	// irrelevant because their domains do not overlap.
	sort.SliceStable(ignoreFiles, func(i, j int) bool {
		return pathDepth(ignoreFiles[i]) < pathDepth(ignoreFiles[j])
	})
	var rules []ignoreRule
	for _, ignoreFile := range ignoreFiles {
		parsed, err := parseIgnoreFile(ignoreFile)
		if err != nil {
			return nil, err
		}
		rules = append(rules, parsed...)
	}
	return rules, nil
}

func parseIgnoreFile(ignoreFile string) ([]ignoreRule, error) {
	file, err := os.Open(ignoreFile)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", ignoreFile, err)
	}
	var rules []ignoreRule
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, `\#`) {
			line = line[1:]
		}
		negated := strings.HasPrefix(line, "!")
		if negated {
			line = strings.TrimPrefix(line, "!")
		} else if strings.HasPrefix(line, `\!`) {
			line = line[1:]
		}
		if !strings.HasSuffix(line, `\ `) {
			line = strings.TrimRight(line, " ")
		}
		directory := strings.HasSuffix(line, "/")
		line = strings.TrimSuffix(line, "/")
		anchored := strings.HasPrefix(line, "/")
		line = strings.TrimPrefix(line, "/")
		if line == "" {
			continue
		}
		rules = append(rules, ignoreRule{
			domain: filepath.Dir(ignoreFile), segments: strings.Split(filepath.ToSlash(line), "/"),
			negated: negated, directory: directory, basename: !anchored && !strings.Contains(line, "/"),
		})
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read %s: %w", ignoreFile, err)
	}
	if err := file.Close(); err != nil {
		return nil, fmt.Errorf("close %s: %w", ignoreFile, err)
	}
	return rules, nil
}

func (rule ignoreRule) matches(absolute string) (bool, error) {
	relative, err := filepath.Rel(rule.domain, absolute)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return false, err
	}
	segments := strings.Split(filepath.ToSlash(relative), "/")
	if rule.basename {
		for _, candidate := range segments {
			matched, err := path.Match(rule.segments[0], candidate)
			if err != nil {
				return false, fmt.Errorf("invalid gitignore pattern %q: %w", rule.segments[0], err)
			}
			if matched {
				return true, nil
			}
		}
		return false, nil
	}
	// A pattern matching a directory also excludes its descendants, whether or
	// not it used a trailing slash. Check each path prefix as Git does.
	limit := len(segments)
	if !rule.directory {
		limit++
	}
	for length := 1; length <= len(segments) && length <= limit; length++ {
		matched, err := matchIgnoreSegments(rule.segments, segments[:length])
		if err != nil {
			return false, err
		}
		if matched && (!rule.directory || length < len(segments)) {
			return true, nil
		}
	}
	return false, nil
}

func matchIgnoreSegments(patterns, values []string) (bool, error) {
	if len(patterns) == 0 {
		return len(values) == 0, nil
	}
	if patterns[0] == "**" {
		if len(patterns) == 1 {
			return true, nil
		}
		for index := 0; index <= len(values); index++ {
			matched, err := matchIgnoreSegments(patterns[1:], values[index:])
			if err != nil || matched {
				return matched, err
			}
		}
		return false, nil
	}
	if len(values) == 0 {
		return false, nil
	}
	matched, err := path.Match(patterns[0], values[0])
	if err != nil || !matched {
		return false, err
	}
	return matchIgnoreSegments(patterns[1:], values[1:])
}

func pathDepth(value string) int {
	return strings.Count(filepath.Clean(value), string(filepath.Separator))
}
