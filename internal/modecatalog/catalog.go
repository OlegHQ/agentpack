package modecatalog

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"

	"github.com/OlegHQ/agentpack/internal/cache"
	"github.com/OlegHQ/agentpack/internal/lockfile"
	"github.com/OlegHQ/agentpack/internal/manifest"
	"github.com/OlegHQ/agentpack/internal/mode"
	"github.com/OlegHQ/agentpack/internal/staging"
)

type DependencyNode struct {
	Module   string
	Children []DependencyNode
}

type CapabilityCatalog struct {
	PackageModules  map[string]struct{}
	PackagePaths    map[string]map[string]struct{}
	DotAgentsPaths  map[string]struct{}
	MCPNames        map[string]struct{}
	DependencyTrees []DependencyNode
}

func BuildCapabilityCatalog(projectRoot string, lock *lockfile.PackLock, project *manifest.Manifest) (CapabilityCatalog, error) {
	catalog := CapabilityCatalog{PackageModules: map[string]struct{}{}, PackagePaths: map[string]map[string]struct{}{}, DotAgentsPaths: map[string]struct{}{}, MCPNames: map[string]struct{}{}}
	lockMap := make(map[string]lockfile.Package)
	if lock != nil {
		for _, pkg := range lock.Packages {
			if pkg.Module == "" {
				continue
			}
			catalog.PackageModules[pkg.Module] = struct{}{}
			paths, err := packagePaths(pkg)
			if err != nil {
				return catalog, err
			}
			catalog.PackagePaths[pkg.Module], lockMap[pkg.Module] = paths, pkg
		}
	}
	if project != nil {
		for module := range project.Dependencies {
			catalog.PackageModules[module] = struct{}{}
		}
	}
	var err error
	catalog.DotAgentsPaths, err = relativeFiles(filepath.Join(projectRoot, ".agents"))
	if err != nil {
		return catalog, err
	}
	if lock != nil {
		entries, err := staging.CollectMCP(projectRoot, *lock, project, nil)
		if err != nil {
			return catalog, err
		}
		for name := range entries {
			catalog.MCPNames[name] = struct{}{}
		}
	} else if project != nil {
		for name := range project.MCP.Servers {
			catalog.MCPNames[name] = struct{}{}
		}
	}
	if project != nil {
		modules := make([]string, 0, len(project.Dependencies))
		for module := range project.Dependencies {
			modules = append(modules, module)
		}
		sort.Strings(modules)
		for _, module := range modules {
			node, err := dependencyNode(module, lockMap, map[string]bool{})
			if err != nil {
				return catalog, err
			}
			catalog.DependencyTrees = append(catalog.DependencyTrees, node)
		}
	}
	return catalog, nil
}

func (catalog CapabilityCatalog) Validate(selector mode.Selector) error {
	switch selector.Kind {
	case mode.SelectorPackage:
		if _, ok := catalog.PackageModules[selector.Module]; !ok {
			return fmt.Errorf("mode: unknown package selector target: %s", selector.Module)
		}
	case mode.SelectorPackagePath:
		if _, ok := catalog.PackageModules[selector.Module]; !ok {
			return fmt.Errorf("mode: unknown package selector target: %s", selector.Module)
		}
		if _, ok := catalog.PackagePaths[selector.Module][selector.RelativePath]; !ok {
			return fmt.Errorf("mode: unknown package path selector target: %s:%s", selector.Module, selector.RelativePath)
		}
	case mode.SelectorMCP:
		if _, ok := catalog.MCPNames[selector.MCPName]; !ok {
			return fmt.Errorf("mode: unknown MCP selector target: %s", selector.MCPName)
		}
	case mode.SelectorDotAgents:
		if _, ok := catalog.DotAgentsPaths[selector.RelativePath]; !ok {
			return fmt.Errorf("mode: unknown .agents selector target: %s", selector.RelativePath)
		}
	}
	return nil
}

func packagePaths(pkg lockfile.Package) (map[string]struct{}, error) {
	if pkg.CacheKey == "" {
		return map[string]struct{}{}, nil
	}
	root, err := cache.EntryDir(pkg.CacheKey)
	if err != nil {
		return nil, err
	}
	return relativeFiles(root)
}

func relativeFiles(root string) (map[string]struct{}, error) {
	result := map[string]struct{}{}
	if info, err := os.Stat(root); os.IsNotExist(err) {
		return result, nil
	} else if err != nil {
		return nil, err
	} else if !info.IsDir() {
		return result, nil
	}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		normalized, err := mode.NormalizeRelativeRuntimePath(relative)
		if err != nil {
			return err
		}
		result[normalized] = struct{}{}
		return nil
	})
	return result, err
}

func dependencyNode(module string, lockMap map[string]lockfile.Package, visiting map[string]bool) (DependencyNode, error) {
	node := DependencyNode{Module: module}
	if visiting[module] {
		return node, nil
	}
	visiting[module] = true
	defer delete(visiting, module)
	pkg, ok := lockMap[module]
	if !ok || pkg.CacheKey == "" {
		return node, nil
	}
	root, err := cache.EntryDir(pkg.CacheKey)
	if err != nil {
		return node, err
	}
	dependencies, err := manifest.LoadNestedDependencies(root)
	if err != nil {
		return node, err
	}
	modules := make([]string, 0, len(dependencies))
	for child := range dependencies {
		modules = append(modules, child)
	}
	sort.Strings(modules)
	for _, child := range modules {
		childNode, err := dependencyNode(child, lockMap, visiting)
		if err != nil {
			return node, err
		}
		node.Children = append(node.Children, childNode)
	}
	return node, nil
}
