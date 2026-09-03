package mode

import (
	"path/filepath"
	"strings"
)

type SelectorKind uint8

const (
	SelectorPackage SelectorKind = iota + 1
	SelectorPackagePath
	SelectorMCP
	SelectorDotAgents
)

type Selector struct {
	Kind         SelectorKind
	Module       string
	RelativePath string
	MCPName      string
}

type MatchSpecificity uint16

const packageSpecificity MatchSpecificity = 1

func ParseSelector(raw string) (Selector, error) {
	trimmed := strings.TrimSpace(raw)
	if value, ok := strings.CutPrefix(trimmed, "package:"); ok {
		module, err := normalizeModule(value)
		if err != nil {
			return Selector{}, err
		}
		return Selector{Kind: SelectorPackage, Module: module}, nil
	}
	if value, ok := strings.CutPrefix(trimmed, "package-path:"); ok {
		moduleValue, pathValue, found := strings.Cut(value, ":")
		if !found {
			return Selector{}, errorsf("invalid selector %q: expected package-path:<module>:<path>", trimmed)
		}
		module, err := normalizeModule(moduleValue)
		if err != nil {
			return Selector{}, err
		}
		relativePath, err := NormalizeRelativeSelectorPath(pathValue)
		if err != nil {
			return Selector{}, err
		}
		return Selector{Kind: SelectorPackagePath, Module: module, RelativePath: relativePath}, nil
	}
	if value, ok := strings.CutPrefix(trimmed, "mcp:"); ok {
		name := strings.TrimSpace(value)
		if name == "" {
			return Selector{}, errorsf("invalid selector %q: MCP name cannot be empty", trimmed)
		}
		return Selector{Kind: SelectorMCP, MCPName: name}, nil
	}
	if value, ok := strings.CutPrefix(trimmed, ".agents:"); ok {
		relativePath, err := NormalizeRelativeSelectorPath(value)
		if err != nil {
			return Selector{}, err
		}
		return Selector{Kind: SelectorDotAgents, RelativePath: relativePath}, nil
	}
	return Selector{}, errorsf("invalid selector %q: expected package:, package-path:, mcp:, or .agents:", trimmed)
}

func (selector Selector) CanonicalString() string {
	switch selector.Kind {
	case SelectorPackage:
		return "package:" + selector.Module
	case SelectorPackagePath:
		return "package-path:" + selector.Module + ":" + selector.RelativePath
	case SelectorMCP:
		return "mcp:" + selector.MCPName
	case SelectorDotAgents:
		return ".agents:" + selector.RelativePath
	default:
		return ""
	}
}

func (selector Selector) MatchesPackagePath(module, relativePath string) (MatchSpecificity, bool, error) {
	if selector.Kind == SelectorPackage {
		return packageSpecificity, selector.Module == module, nil
	}
	if selector.Kind != SelectorPackagePath || selector.Module != module {
		return 0, false, nil
	}
	normalized, err := NormalizeRelativeRuntimePath(relativePath)
	if err != nil {
		return 0, false, err
	}
	if !pathSelectorMatches(selector.RelativePath, normalized) {
		return 0, false, nil
	}
	return specificityForPath(selector.RelativePath), true, nil
}

func (selector Selector) MatchesDotAgentsPath(relativePath string) (MatchSpecificity, bool, error) {
	normalized, err := NormalizeRelativeRuntimePath(relativePath)
	if err != nil {
		return 0, false, err
	}
	if selector.Kind != SelectorDotAgents || !pathSelectorMatches(selector.RelativePath, normalized) {
		return 0, false, nil
	}
	return specificityForPath(selector.RelativePath), true, nil
}

func (selector Selector) MatchesMCP(name string) bool {
	return selector.Kind == SelectorMCP && selector.MCPName == name
}

func NormalizeRelativeSelectorPath(input string) (string, error) {
	return normalizeRelativePath(input, "selector path")
}

func NormalizeRelativeRuntimePath(input string) (string, error) {
	return normalizeRelativePath(input, "path")
}

func normalizeRelativePath(input, label string) (string, error) {
	replaced := strings.ReplaceAll(strings.TrimSpace(input), "\\", "/")
	stripped := replaced
	for strings.HasPrefix(stripped, "./") {
		stripped = strings.TrimPrefix(stripped, "./")
	}
	stripped = strings.Trim(stripped, "/")
	if stripped == "" {
		return "", errorsf("%s cannot be empty", label)
	}
	segments := strings.Split(stripped, "/")
	result := make([]string, 0, len(segments))
	for _, segment := range segments {
		switch segment {
		case "", ".":
			continue
		case "..":
			return "", errorsf("%s cannot contain parent traversal: %q", label, input)
		default:
			if filepath.IsAbs(segment) {
				return "", errorsf("%s must be relative: %q", label, input)
			}
			result = append(result, segment)
		}
	}
	if len(result) == 0 {
		return "", errorsf("%s cannot be empty", label)
	}
	return strings.Join(result, "/"), nil
}

func normalizeModule(module string) (string, error) {
	module = strings.TrimSpace(module)
	if module == "" {
		return "", errorsf("module selector cannot be empty")
	}
	return module, nil
}

func specificityForPath(relativePath string) MatchSpecificity {
	depth := 0
	for _, segment := range strings.Split(relativePath, "/") {
		if segment != "" {
			depth++
		}
	}
	return MatchSpecificity(10 + depth)
}

func pathSelectorMatches(selector, target string) bool {
	return target == selector || strings.HasPrefix(target, selector+"/")
}
