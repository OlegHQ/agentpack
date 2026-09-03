package modetui

import (
	"testing"

	"github.com/OlegHQ/agentpack/internal/modecatalog"
)

func TestTreeContainsPackageAndDotAgentsLeaves(t *testing.T) {
	catalog := modecatalog.CapabilityCatalog{
		PackageModules: map[string]struct{}{"github.com/acme/repo": {}},
		PackagePaths:   map[string]map[string]struct{}{"github.com/acme/repo": {"hooks/hooks.json": {}}},
		DotAgentsPaths: map[string]struct{}{"rules/a.mdc": {}},
		MCPNames:       map[string]struct{}{"docs": {}},
	}
	tree := BuildTree(catalog)
	expanded := map[string]bool{}
	var expand func([]Node)
	expand = func(nodes []Node) {
		for _, node := range nodes {
			expanded[node.ID] = true
			expand(node.Children)
		}
	}
	expand(tree)
	seen := map[string]bool{}
	for _, row := range Flatten(tree, expanded) {
		seen[row.Node.ID] = true
	}
	for _, id := range []string{"package-path:github.com/acme/repo:hooks/hooks.json", ".agents:rules/a.mdc", "mcp:docs"} {
		if !seen[id] {
			t.Fatalf("missing %s", id)
		}
	}
}
