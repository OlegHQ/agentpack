package modetui

import (
	"sort"
	"strings"

	"github.com/OlegHQ/agentpack/internal/mode"
	"github.com/OlegHQ/agentpack/internal/modecatalog"
)

type Node struct {
	ID, Label, Subtitle string
	Selector            *mode.Selector
	Children            []Node
}
type Row struct {
	Node  *Node
	Depth int
}

func BuildTree(catalog modecatalog.CapabilityCatalog) []Node {
	var roots []Node
	packages := Node{ID: "section:packages", Label: "Packages"}
	modules := keys(catalog.PackageModules)
	for _, module := range modules {
		selector := mode.Selector{Kind: mode.SelectorPackage, Module: module}
		label, subtitle := packageDisplay(module)
		node := Node{ID: selector.CanonicalString(), Label: label, Subtitle: subtitle, Selector: &selector}
		node.Children = pathTree("package-path:"+module+":", catalog.PackagePaths[module], func(path string) mode.Selector {
			return mode.Selector{Kind: mode.SelectorPackagePath, Module: module, RelativePath: path}
		})
		packages.Children = append(packages.Children, node)
	}
	if len(packages.Children) != 0 {
		roots = append(roots, packages)
	}
	mcpRoot := Node{ID: "section:mcp", Label: "MCP servers"}
	for _, name := range keys(catalog.MCPNames) {
		selector := mode.Selector{Kind: mode.SelectorMCP, MCPName: name}
		mcpRoot.Children = append(mcpRoot.Children, Node{ID: selector.CanonicalString(), Label: name, Selector: &selector})
	}
	if len(mcpRoot.Children) != 0 {
		roots = append(roots, mcpRoot)
	}
	dot := Node{ID: "section:.agents", Label: ".agents", Children: pathTree(".agents:", catalog.DotAgentsPaths, func(path string) mode.Selector {
		return mode.Selector{Kind: mode.SelectorDotAgents, RelativePath: path}
	})}
	if len(dot.Children) != 0 {
		roots = append(roots, dot)
	}
	return roots
}

func Flatten(nodes []Node, expanded map[string]bool) []Row {
	var rows []Row
	var walk func([]Node, int)
	walk = func(items []Node, depth int) {
		for index := range items {
			node := &items[index]
			rows = append(rows, Row{node, depth})
			if expanded[node.ID] {
				walk(node.Children, depth+1)
			}
		}
	}
	walk(nodes, 0)
	return rows
}
func CollectSelectors(node *Node) []string {
	var result []string
	var walk func(*Node)
	walk = func(current *Node) {
		if current.Selector != nil {
			result = append(result, current.Selector.CanonicalString())
		}
		for index := range current.Children {
			walk(&current.Children[index])
		}
	}
	walk(node)
	return result
}

func pathTree(prefix string, paths map[string]struct{}, makeSelector func(string) mode.Selector) []Node {
	var forest []Node
	for _, path := range keys(paths) {
		segments := strings.Split(path, "/")
		insertPath(&forest, segments, 0, prefix, makeSelector)
	}
	return forest
}
func insertPath(forest *[]Node, segments []string, depth int, prefix string, makeSelector func(string) mode.Selector) {
	if depth >= len(segments) {
		return
	}
	relative := strings.Join(segments[:depth+1], "/")
	id := prefix + relative
	index := -1
	for i := range *forest {
		if (*forest)[i].ID == id {
			index = i
			break
		}
	}
	if index < 0 {
		selector := makeSelector(relative)
		*forest = append(*forest, Node{ID: id, Label: segments[depth], Selector: &selector})
		index = len(*forest) - 1
	}
	insertPath(&(*forest)[index].Children, segments, depth+1, prefix, makeSelector)
}
func packageDisplay(module string) (string, string) {
	rest := strings.TrimPrefix(module, "github.com/")
	segments := strings.Split(rest, "/")
	if rest == module || len(segments) < 2 {
		return module, ""
	}
	if len(segments) == 2 {
		return segments[1], segments[0]
	}
	return segments[len(segments)-1], strings.Join(segments[:len(segments)-1], "/")
}
func keys(values map[string]struct{}) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}
