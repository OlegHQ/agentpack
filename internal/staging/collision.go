package staging

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type CollisionRemoval struct{ SkillSlugs map[string]struct{} }

func ResolveCollisionsWithHome(bundle string, skillRoots, markdownRoots []string, home string) (CollisionRemoval, error) {
	removed := CollisionRemoval{SkillSlugs: make(map[string]struct{})}
	if home == "" {
		return removed, nil
	}
	userSkills, err := directoryNames(filepath.Join(home, ".claude", "skills"), filepath.Join(home, ".grok", "skills"))
	if err != nil {
		return removed, err
	}
	userCommands, err := markdownStems(filepath.Join(home, ".claude", "commands"), filepath.Join(home, ".grok", "commands"))
	if err != nil {
		return removed, err
	}
	userAgents, err := markdownStems(filepath.Join(home, ".claude", "agents"), filepath.Join(home, ".grok", "agents"))
	if err != nil {
		return removed, err
	}
	bundleSkills, err := directoryNames(filepath.Join(bundle, "skills"))
	if err != nil {
		return removed, err
	}
	bundleCommands, err := markdownStems(filepath.Join(bundle, "commands"))
	if err != nil {
		return removed, err
	}
	bundleAgents, err := markdownStems(filepath.Join(bundle, "agents"))
	if err != nil {
		return removed, err
	}
	for _, slug := range intersection(userSkills, bundleSkills) {
		removed.SkillSlugs[slug] = struct{}{}
		fmt.Fprintf(os.Stderr, "warning: Using user-installed skill `%s`; omitted pack duplicate from staged bundle (and other harness trees)\n", slug)
		for _, root := range skillRoots {
			if err := removeNamedDirectory(filepath.Join(root, "skills"), slug); err != nil {
				return removed, err
			}
		}
	}
	for _, collision := range []struct {
		user, pack       map[string]struct{}
		directory, label string
	}{{userCommands, bundleCommands, "commands", "command"}, {userAgents, bundleAgents, "agents", "agent"}} {
		for _, stem := range intersection(collision.user, collision.pack) {
			fmt.Fprintf(os.Stderr, "warning: Using user-installed %s `%s`; omitted pack duplicate from staged bundle (and other harness trees)\n", collision.label, stem)
			for _, root := range markdownRoots {
				if err := removeMarkdownStem(filepath.Join(root, collision.directory), stem); err != nil {
					return removed, err
				}
			}
		}
	}
	return removed, nil
}

func directoryNames(roots ...string) (map[string]struct{}, error) {
	result := make(map[string]struct{})
	for _, root := range roots {
		entries, err := os.ReadDir(root)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			if entry.IsDir() {
				result[strings.ToLower(entry.Name())] = struct{}{}
			}
		}
	}
	return result, nil
}

func markdownStems(roots ...string) (map[string]struct{}, error) {
	result := make(map[string]struct{})
	for _, root := range roots {
		if _, err := os.Stat(root); os.IsNotExist(err) {
			continue
		}
		err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !entry.IsDir() && strings.EqualFold(filepath.Ext(entry.Name()), ".md") {
				result[strings.ToLower(strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name())))] = struct{}{}
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return result, nil
}

func intersection(left, right map[string]struct{}) []string {
	var values []string
	for value := range left {
		if _, exists := right[value]; exists {
			values = append(values, value)
		}
	}
	sort.Strings(values)
	return values
}

func removeNamedDirectory(root, lowerName string) error {
	entries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() && strings.EqualFold(entry.Name(), lowerName) {
			if err := os.RemoveAll(filepath.Join(root, entry.Name())); err != nil {
				return err
			}
		}
	}
	return nil
}

func removeMarkdownStem(root, lowerStem string) error {
	if _, err := os.Stat(root); os.IsNotExist(err) {
		return nil
	}
	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() && strings.EqualFold(filepath.Ext(entry.Name()), ".md") && strings.EqualFold(strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name())), lowerStem) {
			return os.Remove(path)
		}
		return nil
	})
}
