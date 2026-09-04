package manifest

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/pelletier/go-toml/v2"

	"github.com/OlegHQ/agentpack/internal/mcp"
	"github.com/OlegHQ/agentpack/internal/mode"
	"github.com/OlegHQ/agentpack/internal/paths"
)

const DefaultGitRef = "HEAD"

type DependencyTable struct {
	Path    *string `toml:"path,omitempty"`
	Commit  *string `toml:"commit,omitempty"`
	Tag     *string `toml:"tag,omitempty"`
	Branch  *string `toml:"branch,omitempty"`
	Version *string `toml:"version,omitempty"`
}

type Dependency struct {
	Short *string
	Table *DependencyTable
}

func (dependency Dependency) PathValue() (string, bool) {
	if dependency.Table == nil || dependency.Table.Path == nil {
		return "", false
	}
	return *dependency.Table.Path, true
}

type MCPSection struct {
	Servers map[string]mcp.Server `toml:"servers"`
}

type fileSchema struct {
	Name         string                     `toml:"name"`
	Version      string                     `toml:"version"`
	Description  string                     `toml:"description"`
	Dependencies map[string]any             `toml:"dependencies"`
	MCP          MCPSection                 `toml:"mcp"`
	Modes        map[string]mode.Definition `toml:"modes"`
}

type Manifest struct {
	Name         string
	Version      string
	Description  string
	Dependencies map[string]Dependency
	MCP          MCPSection
	Modes        map[string]mode.Definition
}

func Load(projectRoot string) (*Manifest, error) {
	path := paths.ManifestPath(projectRoot)
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read manifest %s: %w", path, err)
	}
	defer file.Close()
	var schema fileSchema
	decoder := toml.NewDecoder(file)
	if err := decoder.Decode(&schema); err != nil {
		return nil, fmt.Errorf("parse agentpack.toml: %w", err)
	}
	applyDefaults(projectRoot, &schema)
	dependencies, err := decodeDependencies(schema.Dependencies)
	if err != nil {
		return nil, err
	}
	return &Manifest{
		Name: schema.Name, Version: schema.Version, Description: schema.Description,
		Dependencies: dependencies, MCP: schema.MCP,
		Modes: nonnilModes(schema.Modes),
	}, nil
}

func LoadNestedDependencies(cacheRoot string) (map[string]Dependency, error) {
	manifest, err := Load(cacheRoot)
	if err != nil || manifest == nil || len(manifest.Dependencies) == 0 {
		return nil, err
	}
	return manifest.Dependencies, nil
}

func (manifest *Manifest) ModeDefinition(name string) (mode.Definition, bool) {
	definition, ok := manifest.Modes[name]
	if ok {
		return definition, true
	}
	if name == mode.DefaultName {
		return mode.ImplicitDefault(), true
	}
	return mode.Definition{}, false
}

func (manifest *Manifest) ListModeNames() []string {
	names := make([]string, 0, len(manifest.Modes)+1)
	for name := range manifest.Modes {
		names = append(names, name)
	}
	if _, exists := manifest.Modes[mode.DefaultName]; !exists {
		names = append(names, mode.DefaultName)
	}
	sort.Strings(names)
	return names
}

func AppendDependencyPin(projectRoot, moduleKey, gitRef string) error {
	return mutateDocument(projectRoot, func(data []byte) ([]byte, error) {
		gitRef = strings.TrimSpace(gitRef)
		value := "{}"
		if gitRef != "" && gitRef != DefaultGitRef {
			value = quoteString(gitRef)
		}
		return insertAssignment(data, []string{"dependencies"}, moduleKey, value, true)
	})
}

func AppendPathDependency(projectRoot, name, relativePath string) error {
	return mutateDocument(projectRoot, func(data []byte) ([]byte, error) {
		return insertAssignment(data, []string{"dependencies"}, name, "{ path = "+quoteString(relativePath)+" }", false)
	})
}

func RemoveDependencyEntry(projectRoot, moduleKey string) error {
	manifest, err := requireManifest(projectRoot)
	if err != nil {
		return err
	}
	for name, definition := range manifest.Modes {
		definition.Enable = filterSelectorsForModule(definition.Enable, moduleKey)
		definition.Disable = filterSelectorsForModule(definition.Disable, moduleKey)
		manifest.Modes[name] = definition
	}
	return mutateDocument(projectRoot, func(data []byte) ([]byte, error) {
		data, _, err := deleteAssignmentOrSection(data, []string{"dependencies", moduleKey})
		if err != nil {
			return nil, err
		}
		return replaceSections(data, []string{"modes"}, renderModes(manifest.Modes))
	})
}

func AddMCPServer(projectRoot, name string, server mcp.Server) error {
	return mutateDocument(projectRoot, func(data []byte) ([]byte, error) {
		path := []string{"mcp", "servers", name}
		exists, err := assignmentExists(data, path)
		if err != nil {
			return nil, err
		}
		if exists {
			return insertAssignment(data, []string{"mcp", "servers"}, name, renderMCPServer(server), false)
		}
		data, _, err = deleteAssignmentOrSection(data, path)
		if err != nil {
			return nil, err
		}
		return insertAssignment(data, []string{"mcp", "servers"}, name, renderMCPServer(server), false)
	})
}

func RemoveMCPServer(projectRoot, name string) (bool, error) {
	removed := false
	err := mutateDocument(projectRoot, func(data []byte) ([]byte, error) {
		updated, found, err := deleteAssignmentOrSection(data, []string{"mcp", "servers", name})
		removed = found
		return updated, err
	})
	return removed, err
}

func CreateMode(projectRoot, name string) error {
	name, err := mode.ValidateName(name)
	if err != nil {
		return err
	}
	return withModes(projectRoot, func(modes map[string]mode.Definition) error {
		if _, exists := modes[name]; exists {
			return fmt.Errorf("mode already exists: %s", name)
		}
		modes[name] = mode.ImplicitDefault()
		return nil
	})
}

func DeleteMode(projectRoot, name string) error {
	if mode.IsReserved(name) {
		return fmt.Errorf("%s is reserved and cannot be deleted", mode.DefaultName)
	}
	return withModes(projectRoot, func(modes map[string]mode.Definition) error {
		if _, exists := modes[name]; !exists {
			return fmt.Errorf("unknown mode: %s", name)
		}
		delete(modes, name)
		return nil
	})
}

func SetModeBase(projectRoot, name string, base mode.Base) error {
	name, err := mode.ValidateName(name)
	if err != nil {
		return err
	}
	if err := ensureModeEditable(name); err != nil {
		return err
	}
	return withModes(projectRoot, func(modes map[string]mode.Definition) error {
		definition, exists := modes[name]
		if !exists {
			definition = mode.ImplicitDefault()
		}
		definition.Base = base
		modes[name] = definition
		return nil
	})
}

func AddModeSelectors(projectRoot, name string, enabled bool, selectors []string) error {
	name, err := mode.ValidateName(name)
	if err != nil {
		return err
	}
	if err := ensureModeEditable(name); err != nil {
		return err
	}
	canonical, err := canonicalizeSelectors(selectors)
	if err != nil {
		return err
	}
	return withModes(projectRoot, func(modes map[string]mode.Definition) error {
		definition, exists := modes[name]
		if !exists {
			definition = mode.ImplicitDefault()
		}
		for _, selector := range canonical {
			if enabled {
				definition.Disable = removeString(definition.Disable, selector)
				definition.Enable = append(definition.Enable, selector)
			} else {
				definition.Enable = removeString(definition.Enable, selector)
				definition.Disable = append(definition.Disable, selector)
			}
		}
		definition.SortAndDeduplicate()
		modes[name] = definition
		return nil
	})
}

func ReplaceModes(projectRoot string, modes map[string]mode.Definition) error {
	return mutateDocument(projectRoot, func(data []byte) ([]byte, error) {
		return replaceSections(data, []string{"modes"}, renderModes(modes))
	})
}

func WriteStub(projectRoot, name, version string) error {
	path := paths.ManifestPath(projectRoot)
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return fmt.Errorf("create manifest %s: %w", path, err)
	}
	body := fmt.Sprintf("# Agentpack project manifest — direct dependencies only. Run `agentpack lock` to refresh pack.lock.\n\nname = %q\nversion = %q\n\n[dependencies]\n# \"github.com/owner/repo/path\" = {}\n", name, version)
	if _, err := file.WriteString(body); err != nil {
		file.Close()
		return fmt.Errorf("write manifest %s: %w", path, err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close manifest %s: %w", path, err)
	}
	return nil
}

func mutateDocument(projectRoot string, mutate func([]byte) ([]byte, error)) error {
	path := paths.ManifestPath(projectRoot)
	original, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read manifest %s: %w", path, err)
	}
	output, err := mutate(original)
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, output, 0o644); err != nil {
		return fmt.Errorf("write manifest %s: %w", path, err)
	}
	return nil
}

func withModes(projectRoot string, change func(map[string]mode.Definition) error) error {
	manifest, err := requireManifest(projectRoot)
	if err != nil {
		return err
	}
	if err := change(manifest.Modes); err != nil {
		return err
	}
	return ReplaceModes(projectRoot, manifest.Modes)
}

func requireManifest(projectRoot string) (*Manifest, error) {
	manifest, err := Load(projectRoot)
	if err != nil {
		return nil, err
	}
	if manifest == nil {
		return nil, fmt.Errorf("manifest missing: %s", paths.ManifestPath(projectRoot))
	}
	return manifest, nil
}

func applyDefaults(projectRoot string, schema *fileSchema) {
	if schema.Name == "" {
		schema.Name = filepath.Base(projectRoot)
	}
	if schema.Name == "" || schema.Name == "." {
		schema.Name = "project"
	}
	if schema.Version == "" {
		schema.Version = "0.0.1"
	}
	for name, definition := range schema.Modes {
		definition.ApplyDefaults()
		schema.Modes[name] = definition
	}
}

func nonnilModes(value map[string]mode.Definition) map[string]mode.Definition {
	if value == nil {
		return make(map[string]mode.Definition)
	}
	return value
}

func decodeDependencies(raw map[string]any) (map[string]Dependency, error) {
	dependencies := make(map[string]Dependency, len(raw))
	for module, value := range raw {
		switch value := value.(type) {
		case string:
			dependencies[module] = Dependency{Short: &value}
		case map[string]any:
			table := DependencyTable{}
			for key, rawField := range value {
				field, ok := rawField.(string)
				if !ok {
					return nil, fmt.Errorf("parse agentpack.toml: dependency %q field %q must be a string", module, key)
				}
				switch key {
				case "path":
					table.Path = &field
				case "commit":
					table.Commit = &field
				case "tag":
					table.Tag = &field
				case "branch":
					table.Branch = &field
				case "version":
					table.Version = &field
				}
			}
			dependencies[module] = Dependency{Table: &table}
		default:
			return nil, fmt.Errorf("parse agentpack.toml: dependency %q must be a string or table", module)
		}
	}
	return dependencies, nil
}

func canonicalizeSelectors(values []string) ([]string, error) {
	result := make([]string, 0, len(values))
	for _, value := range values {
		selector, err := mode.ParseSelector(value)
		if err != nil {
			return nil, err
		}
		result = append(result, selector.CanonicalString())
	}
	return result, nil
}

func ensureModeEditable(name string) error {
	if mode.IsReserved(name) {
		return fmt.Errorf("%s is read-only", mode.DefaultName)
	}
	return nil
}

func filterSelectorsForModule(values []string, module string) []string {
	result := values[:0]
	for _, value := range values {
		selector, err := mode.ParseSelector(value)
		if err == nil && (selector.Kind == mode.SelectorPackage || selector.Kind == mode.SelectorPackagePath) && selector.Module == module {
			continue
		}
		result = append(result, value)
	}
	return result
}

func removeString(values []string, target string) []string {
	return slices.DeleteFunc(values, func(value string) bool { return value == target })
}
