package resolve

import (
	"encoding/hex"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/Masterminds/semver/v3"

	githubsource "github.com/OlegHQ/agentpack/internal/github"
	"github.com/OlegHQ/agentpack/internal/manifest"
)

var bareVersionConstraint = regexp.MustCompile(`^[0-9]+(?:\.[0-9]+){0,2}(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?$`)

type versionRequirement struct {
	raw        string
	constraint *semver.Constraints
}

type ModuleConstraints struct {
	Exact  string
	Branch string
	Tag    string
	Semver []versionRequirement
	Latest bool
}

func (constraints *ModuleConstraints) Merge(other ModuleConstraints) error {
	if err := mergeUniquePin("commit", &constraints.Exact, other.Exact); err != nil {
		return err
	}
	if err := mergeUniquePin("tag", &constraints.Tag, other.Tag); err != nil {
		return err
	}
	if err := mergeUniquePin("branch", &constraints.Branch, other.Branch); err != nil {
		return err
	}
	constraints.Semver = append(constraints.Semver, other.Semver...)
	constraints.Latest = constraints.Latest || other.Latest
	return nil
}

type Tag struct {
	Name string
	SHA  string
}

type TagLister interface {
	ListTags(owner, repo string, forceRefresh bool) ([]Tag, error)
}

func (constraints ModuleConstraints) PickGitRef(lister TagLister, owner, repo string, forceRefresh bool) (string, error) {
	if constraints.Exact != "" {
		return constraints.Exact, nil
	}
	if len(constraints.Semver) != 0 {
		tags, err := lister.ListTags(owner, repo, forceRefresh)
		if err != nil {
			return "", fmt.Errorf("list tags for %s/%s: %w", owner, repo, err)
		}
		type candidate struct {
			version *semver.Version
			name    string
		}
		candidates := make([]candidate, 0, len(tags))
		for _, tag := range tags {
			versionText := strings.TrimPrefix(tag.Name, "v")
			version, err := semver.StrictNewVersion(versionText)
			if err == nil {
				candidates = append(candidates, candidate{version: version, name: tag.Name})
			}
		}
		sort.Slice(candidates, func(i, j int) bool { return candidates[i].version.GreaterThan(candidates[j].version) })
		for _, candidate := range candidates {
			matched := true
			for _, requirement := range constraints.Semver {
				if !requirement.constraint.Check(candidate.version) {
					matched = false
					break
				}
			}
			if matched {
				return candidate.name, nil
			}
		}
		raw := make([]string, len(constraints.Semver))
		for index, requirement := range constraints.Semver {
			raw[index] = requirement.raw
		}
		return "", fmt.Errorf("no tag matching semver constraints %v for %s/%s", raw, owner, repo)
	}
	if constraints.Tag != "" {
		return constraints.Tag, nil
	}
	if constraints.Branch != "" {
		return constraints.Branch, nil
	}
	return githubsource.DefaultGitRef, nil
}

func ConstraintsFromRef(value string, present bool) (ModuleConstraints, error) {
	value = strings.TrimSpace(value)
	if !present || value == "" {
		return ModuleConstraints{Latest: true}, nil
	}
	if isCommitSHA(value) {
		return ModuleConstraints{Exact: strings.ToLower(value)}, nil
	}
	if requirement, err := parseCargoRequirement(value); err == nil {
		return ModuleConstraints{Semver: []versionRequirement{requirement}}, nil
	}
	return ModuleConstraints{Tag: value}, nil
}

func ConstraintsFromDependency(dependency manifest.Dependency, keyRef string, hasKeyRef bool) (ModuleConstraints, error) {
	if dependency.Short != nil {
		value := strings.TrimSpace(*dependency.Short)
		if value != "" {
			return ConstraintsFromRef(value, true)
		}
		return ConstraintsFromTable(manifest.DependencyTable{}, keyRef, hasKeyRef)
	}
	if dependency.Table == nil {
		return ModuleConstraints{}, fmt.Errorf("dependency has neither string nor table value")
	}
	return ConstraintsFromTable(*dependency.Table, keyRef, hasKeyRef)
}

func ConstraintsFromTable(table manifest.DependencyTable, keyRef string, hasKeyRef bool) (ModuleConstraints, error) {
	if table.Path != nil {
		return ModuleConstraints{}, fmt.Errorf("path dependencies should be resolved before constraint parsing")
	}
	count := 0
	for _, present := range []bool{table.Commit != nil, table.Tag != nil, table.Branch != nil, table.Version != nil} {
		if present {
			count++
		}
	}
	if count > 1 {
		return ModuleConstraints{}, fmt.Errorf("dependency table may only specify one of commit, tag, branch, version")
	}
	switch {
	case table.Commit != nil:
		return ModuleConstraints{Exact: strings.ToLower(*table.Commit)}, nil
	case table.Tag != nil:
		return ModuleConstraints{Tag: *table.Tag}, nil
	case table.Branch != nil:
		return ModuleConstraints{Branch: *table.Branch}, nil
	case table.Version != nil:
		requirement, err := parseCargoRequirement(*table.Version)
		if err != nil {
			return ModuleConstraints{}, fmt.Errorf("semver: %w", err)
		}
		return ModuleConstraints{Semver: []versionRequirement{requirement}}, nil
	case hasKeyRef:
		return ConstraintsFromRef(keyRef, true)
	default:
		return ModuleConstraints{Latest: true}, nil
	}
}

func parseCargoRequirement(value string) (versionRequirement, error) {
	value = strings.TrimSpace(value)
	if value == "" || strings.HasPrefix(value, "v") {
		return versionRequirement{}, fmt.Errorf("invalid semantic version requirement %q", value)
	}
	translated := value
	if bareVersionConstraint.MatchString(value) {
		translated = "^" + value
	}
	constraint, err := semver.NewConstraint(translated)
	if err != nil {
		return versionRequirement{}, err
	}
	return versionRequirement{raw: value, constraint: constraint}, nil
}

func mergeUniquePin(kind string, target *string, incoming string) error {
	if incoming == "" {
		return nil
	}
	if *target != "" && *target != incoming {
		plural := kind + "s"
		if kind == "branch" {
			plural = "branches"
		} else if kind == "commit" {
			plural = "commit pins"
		}
		return fmt.Errorf("conflicting %s for the same module: %s vs %s", plural, *target, incoming)
	}
	if *target == "" {
		*target = incoming
	}
	return nil
}

func isCommitSHA(value string) bool {
	if len(value) != 40 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}
