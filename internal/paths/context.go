package paths

import (
	"fmt"
	"os"
)

// Context separates where the portable definition lives from the checkout the
// agent uses as cwd. When both roots are empty after Resolve, callers still
// use a single project root (legacy workflow).
type Context struct {
	// DefinitionRoot holds agentpack.toml and pack.lock.
	DefinitionRoot string
	// WorkspaceRoot is the checkout / agent cwd (.agents, overlays).
	WorkspaceRoot string
}

// SameRoot reports whether definition and workspace are the same path.
func (ctx Context) SameRoot() bool {
	return ctx.DefinitionRoot != "" && ctx.DefinitionRoot == ctx.WorkspaceRoot
}

// ResolveContext builds a Context from explicit flags and an optional binding.
// Legacy --project-root alone keeps today's single-root behavior.
func ResolveContext(projectRoot, definitionRoot, workspaceRoot string, boundDefinition string) (Context, error) {
	workspace := firstNonEmpty(workspaceRoot, projectRoot)
	definition := firstNonEmpty(definitionRoot, boundDefinition, projectRoot)

	if definition == "" && workspace == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return Context{}, fmt.Errorf("get current directory: %w", err)
		}
		root, err := FindProjectRoot(cwd)
		if err != nil {
			return Context{}, err
		}
		return Context{DefinitionRoot: root, WorkspaceRoot: root}, nil
	}

	if workspace == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return Context{}, fmt.Errorf("get current directory: %w", err)
		}
		workspace = cwd
	}
	ws, err := canonical(workspace)
	if err != nil {
		return Context{}, err
	}

	if definition == "" {
		if root, err := FindProjectRoot(ws); err == nil {
			return Context{DefinitionRoot: root, WorkspaceRoot: ws}, nil
		}
		return Context{}, fmt.Errorf("%w from %s", ErrProjectNotFound, ws)
	}
	def, err := canonical(definition)
	if err != nil {
		return Context{}, err
	}
	if !regularFile(ManifestPath(def)) && !regularFile(LockPath(def)) {
		return Context{}, fmt.Errorf("%w from %s", ErrProjectNotFound, def)
	}
	return Context{DefinitionRoot: def, WorkspaceRoot: ws}, nil
}

// ResolveContextOrCWD allows a missing definition (launchers / add), using cwd
// as both roots when nothing is discoverable.
func ResolveContextOrCWD(projectRoot, definitionRoot, workspaceRoot string, boundDefinition string) (Context, error) {
	ctx, err := ResolveContext(projectRoot, definitionRoot, workspaceRoot, boundDefinition)
	if err == nil {
		return ctx, nil
	}
	if definitionRoot != "" || boundDefinition != "" {
		return Context{}, err
	}
	root, err := ResolveProjectRootOrCWD(firstNonEmpty(workspaceRoot, projectRoot))
	if err != nil {
		return Context{}, err
	}
	return Context{DefinitionRoot: root, WorkspaceRoot: root}, nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
