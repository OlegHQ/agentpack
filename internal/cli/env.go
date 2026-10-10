package cli

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/OlegHQ/agentpack/internal/environment"
	"github.com/OlegHQ/agentpack/internal/paths"
	packSync "github.com/OlegHQ/agentpack/internal/sync"
)

func (runner Runner) runEnv(ctx context.Context, workspace string, arguments []string, quiet bool) error {
	if len(arguments) == 0 {
		return fmt.Errorf("env requires an action (init|use|bind|unuse|list|status|export|import|restore)")
	}
	action, args := arguments[0], arguments[1:]
	switch action {
	case "init":
		return runner.envInit(args, quiet)
	case "use", "bind":
		return runner.envUse(workspace, args, quiet)
	case "unuse":
		return runner.envUnuse(workspace, args, quiet)
	case "list":
		if err := noArgs(args); err != nil {
			return err
		}
		return runner.envList(quiet)
	case "status":
		if err := noArgs(args); err != nil {
			return err
		}
		return runner.envStatus(workspace, quiet)
	case "export":
		return runner.envExport(workspace, args, quiet)
	case "import":
		return runner.envImport(args, quiet)
	case "restore":
		if err := noArgs(args); err != nil {
			return err
		}
		return runner.envRestore(ctx, workspace, quiet)
	default:
		return fmt.Errorf("unknown env action %q", action)
	}
}

func (runner Runner) envInit(args []string, quiet bool) error {
	name, args, _, err := takeFlag(args, "--name")
	if err != nil {
		return err
	}
	dir, args, _, err := takeFlag(args, "--dir")
	if err != nil {
		return err
	}
	from, args, _, err := takeFlag(args, "--from")
	if err != nil {
		return err
	}
	if len(args) == 1 && name == "" {
		name = args[0]
		args = args[1:]
	}
	if err := noArgs(args); err != nil {
		return err
	}
	if name == "" && dir == "" {
		return fmt.Errorf("env init requires a name or --dir")
	}
	created, err := environment.Init(name, dir, from)
	if err != nil {
		return err
	}
	if !quiet {
		fmt.Fprintf(runner.Stdout, "Created external environment at %s\n", created)
	}
	return nil
}

func (runner Runner) envUse(workspace string, args []string, quiet bool) error {
	project, args, _, err := takeFlag(args, "--project")
	if err != nil {
		return err
	}
	if project != "" {
		workspace = project
	}
	if len(args) != 1 {
		return fmt.Errorf("env use requires an environment name or path")
	}
	definition, err := environment.ResolveRef(args[0])
	if err != nil {
		return err
	}
	workspace, err = filepath.Abs(workspace)
	if err != nil {
		return err
	}
	binding := environment.Binding{
		Environment:    filepath.Base(definition),
		DefinitionRoot: definition,
		WorkspaceRoot:  workspace,
	}
	if err := environment.SaveBinding(binding); err != nil {
		return err
	}
	if !quiet {
		fmt.Fprintf(runner.Stdout, "Bound workspace %s -> %s\n", workspace, definition)
	}
	return nil
}

func (runner Runner) envUnuse(workspace string, args []string, quiet bool) error {
	if err := noArgs(args); err != nil {
		return err
	}
	if err := environment.ClearBinding(workspace); err != nil {
		return err
	}
	if !quiet {
		fmt.Fprintf(runner.Stdout, "Cleared environment binding for %s\n", workspace)
	}
	return nil
}

func (runner Runner) envList(quiet bool) error {
	names, err := environment.List()
	if err != nil {
		return err
	}
	if quiet {
		return nil
	}
	root, _ := environment.Root()
	if len(names) == 0 {
		fmt.Fprintf(runner.Stdout, "No environments under %s\n", root)
		return nil
	}
	fmt.Fprintln(runner.Stdout, "NAME\tPATH")
	for _, name := range names {
		dir, _ := environment.Dir(name)
		fmt.Fprintf(runner.Stdout, "%s\t%s\n", name, dir)
	}
	return nil
}

func (runner Runner) envStatus(workspace string, quiet bool) error {
	binding, found, err := environment.LoadBinding(workspace)
	if err != nil {
		return err
	}
	if quiet {
		return nil
	}
	home, _ := paths.UserAgentpackHome()
	fmt.Fprintf(runner.Stdout, "workspace\t%s\n", workspace)
	fmt.Fprintf(runner.Stdout, "agentpack_home\t%s\n", home)
	if !found {
		fmt.Fprintln(runner.Stdout, "binding\t(none)")
		return nil
	}
	fmt.Fprintf(runner.Stdout, "environment\t%s\n", binding.Environment)
	fmt.Fprintf(runner.Stdout, "definition_root\t%s\n", binding.DefinitionRoot)
	return nil
}

func (runner Runner) envExport(workspace string, args []string, quiet bool) error {
	definition := ""
	if binding, found, err := environment.LoadBinding(workspace); err != nil {
		return err
	} else if found {
		definition = binding.DefinitionRoot
	}
	from, args, _, err := takeFlag(args, "--from")
	if err != nil {
		return err
	}
	if from != "" {
		definition, err = environment.ResolveRef(from)
		if err != nil {
			return err
		}
	}
	if definition == "" {
		root, err := paths.ResolveProjectRoot(workspace)
		if err != nil {
			return fmt.Errorf("env export needs a bound environment, --from, or a project with agentpack.toml: %w", err)
		}
		definition = root
	}
	if len(args) != 1 {
		return fmt.Errorf("env export requires a destination bundle path")
	}
	if err := environment.ExportBundle(definition, args[0]); err != nil {
		return err
	}
	if !quiet {
		fmt.Fprintf(runner.Stdout, "Exported %s -> %s\n", definition, args[0])
	}
	return nil
}

func (runner Runner) envImport(args []string, quiet bool) error {
	name, args, _, err := takeFlag(args, "--name")
	if err != nil {
		return err
	}
	dir, args, _, err := takeFlag(args, "--dir")
	if err != nil {
		return err
	}
	if len(args) != 1 {
		return fmt.Errorf("env import requires a bundle path")
	}
	if dir == "" {
		if name == "" {
			base := strings.TrimSuffix(filepath.Base(args[0]), ".bundle")
			base = strings.TrimSuffix(base, ".tar.gz")
			base = strings.TrimSuffix(base, ".tgz")
			name = base
		}
		dir, err = environment.Dir(name)
		if err != nil {
			return err
		}
	}
	imported, err := environment.ImportBundle(args[0], dir)
	if err != nil {
		return err
	}
	if !quiet {
		fmt.Fprintf(runner.Stdout, "Imported bundle into %s\n", imported)
	}
	return nil
}

func (runner Runner) envRestore(ctx context.Context, workspace string, quiet bool) error {
	definition := workspace
	if binding, found, err := environment.LoadBinding(workspace); err != nil {
		return err
	} else if found {
		definition = binding.DefinitionRoot
	} else {
		root, err := paths.ResolveProjectRoot(workspace)
		if err != nil {
			return err
		}
		definition = root
	}
	strict := definition != workspace
	if err := runner.Service.RestoreFrozenOptions(ctx, definition, packSync.SyncOptions{
		WorkspaceRoot: workspace, StrictExternal: strict,
	}); err != nil {
		return err
	}
	if !quiet {
		fmt.Fprintf(runner.Stdout, "Restored locked cache + staging for %s (pack.lock unchanged)\n", definition)
	}
	return nil
}
