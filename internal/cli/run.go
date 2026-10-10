package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/OlegHQ/agentpack/internal/environment"
	base "github.com/OlegHQ/agentpack/internal/harness"
	"github.com/OlegHQ/agentpack/internal/harness/registry"
	"github.com/OlegHQ/agentpack/internal/lockfile"
	"github.com/OlegHQ/agentpack/internal/manifest"
	"github.com/OlegHQ/agentpack/internal/paths"
	"github.com/OlegHQ/agentpack/internal/proxy"
	"github.com/OlegHQ/agentpack/internal/staging"
	packSync "github.com/OlegHQ/agentpack/internal/sync"
)

var Version = "0.4.1"

type Runner struct {
	Stdout, Stderr io.Writer
	Stdin          io.Reader
	Service        packSync.Service
	Launch         func(*os.Process) error
}

func NewRunner() Runner {
	return Runner{Stdout: os.Stdout, Stderr: os.Stderr, Stdin: os.Stdin, Service: packSync.NewService()}
}

func (runner Runner) Run(ctx context.Context, arguments []string) (int, error) {
	if len(arguments) == 1 && (arguments[0] == "--version" || arguments[0] == "-V") {
		fmt.Fprintln(runner.Stdout, "agentpack "+Version)
		return 0, nil
	}
	invocation, err := Parse(arguments)
	if err != nil {
		return 2, err
	}
	if runner.Service.Notify == nil {
		runner.Service.Notify = func(message string) { fmt.Fprintln(runner.Stderr, "warning: "+message) }
	}
	if invocation.Command != "init" && hasBeforeDoubleDash(invocation.Args, "--version", "-V") {
		fmt.Fprintln(runner.Stdout, "agentpack "+Version)
		return 0, nil
	}
	if invocation.Global.Proxy && invocation.Command != "claude" {
		return 2, errors.New("--proxy is only supported with `agentpack claude`")
	}
	if invocation.Command == "hook-exec" {
		return runner.runHook(ctx, invocation.Args)
	}
	if invocation.Command == "init" {
		if err := runner.runInit(invocation); err != nil {
			return 1, err
		}
		return 0, nil
	}
	allowsMissing := map[string]bool{"add": true, "claude": true, "opencode": true, "codex": true, "grok": true, "agy": true, "agent": true, "env": true, "config": true, "support": true}
	packContext, err := resolveInvocationContext(invocation, allowsMissing[invocation.Command])
	if err != nil {
		return 1, err
	}
	root := packContext.DefinitionRoot
	workspace := packContext.WorkspaceRoot
	switch invocation.Command {
	case "lock":
		args, update := takeBool(invocation.Args, "--update")
		args, allowUnpinned := takeBool(args, "--allow-unpinned-mcp")
		if err := noArgs(args); err != nil {
			return 2, err
		}
		var locked lockfile.PackLock
		locked, err = runner.Service.LockWithOptions(ctx, root, packSync.LockOptions{Refresh: update, AllowUnpinnedMCP: allowUnpinned})
		if err == nil && !invocation.Global.Quiet {
			fmt.Fprintf(runner.Stdout, "Wrote %s (%d package(s)).\n", paths.LockPath(root), len(locked.Packages))
		}
	case "add", "remove":
		args, noSync := takeBool(invocation.Args, "--no-sync")
		if len(args) != 1 {
			return 2, fmt.Errorf("%s requires exactly one package spec", invocation.Command)
		}
		if invocation.Command == "add" {
			if !invocation.Global.Quiet {
				fmt.Fprintf(runner.Stdout, "Adding: %s\n", args[0])
			}
			var pkg lockfile.Package
			pkg, err = runner.Service.Add(ctx, root, args[0], noSync)
			if err == nil && !invocation.Global.Quiet {
				fmt.Fprintf(runner.Stdout, "Recorded %s in agentpack.toml and refreshed pack.lock.\n", pkg.Module)
			}
		} else {
			var key string
			key, err = runner.Service.Remove(ctx, root, args[0], noSync)
			if err == nil && !invocation.Global.Quiet {
				fmt.Fprintf(runner.Stdout, "Removed %s from %s and refreshed %s.\n", key, paths.ManifestPath(root), paths.LockPath(root))
			}
		}
		if err == nil && noSync && !invocation.Global.Quiet {
			fmt.Fprintln(runner.Stdout, "Skipping sync (--no-sync).")
		}
	case "update":
		args, noSync := takeBool(invocation.Args, "--no-sync")
		var updated lockfile.PackLock
		updated, err = runner.Service.Update(ctx, root, args, noSync)
		if err == nil && !invocation.Global.Quiet {
			if len(args) == 0 {
				fmt.Fprintf(runner.Stdout, "Updated floating dependencies and refreshed %s (%d package(s)).\n", paths.LockPath(root), len(updated.Packages))
			} else {
				fmt.Fprintf(runner.Stdout, "Updated %s and refreshed %s.\n", strings.Join(args, ", "), paths.LockPath(root))
			}
			if noSync {
				fmt.Fprintln(runner.Stdout, "Skipping sync (--no-sync).")
			}
		}
	case "list":
		if err := noArgs(invocation.Args); err != nil {
			return 2, err
		}
		var listed lockfile.PackLock
		listed, err = lockfile.Load(root)
		if err == nil && !invocation.Global.Quiet {
			project, loadErr := manifest.Load(root)
			if loadErr != nil {
				return 1, loadErr
			}
			fmt.Fprintln(runner.Stdout, "MODULE\tKIND\tSCOPE\tSELECTOR\tCOMMIT")
			for _, pkg := range listed.Packages {
				scope := "transitive"
				selector := ""
				if pkg.Direct {
					scope = "direct"
					selector = dependencySelector(project, pkg.Module)
				}
				commit := pkg.Commit
				if len(commit) > 12 {
					commit = commit[:12]
				}
				fmt.Fprintf(runner.Stdout, "%s\t%s\t%s\t%s\t%s\n", pkg.Module, pkg.Kind, scope, selector, commit)
			}
		}
	case "sync":
		args, dry := takeBool(invocation.Args, "--dry-run")
		args, verify := takeBool(args, "--verify-only")
		args, update := takeBool(args, "--update-lock")
		args, repair := takeBool(args, "--repair")
		if err := noArgs(args); err != nil {
			return 2, err
		}
		var result packSync.SyncResult
		result, err = runner.Service.Sync(ctx, root, packSync.SyncOptions{
			DryRun: dry, VerifyOnly: verify, UpdateLock: update, Repair: repair,
			Mode: invocation.Global.Mode, WorkspaceRoot: workspace, StrictExternal: invocation.Global.StrictExternal,
		})
		if err == nil && !invocation.Global.Quiet {
			for _, warning := range result.Warnings {
				fmt.Fprintln(runner.Stderr, "warning: "+warning)
			}
			if dry {
				fmt.Fprintf(runner.Stdout, "Dry-run: would sync %d skill(s), %d plugin(s); %d skill(s) shadowed by plugins (omitted from staging); no changes made.\n", result.Skills, result.Plugins, result.Shadowed)
			} else if verify {
				fmt.Fprintln(runner.Stdout, "Staging checks passed")
			} else {
				fmt.Fprintf(runner.Stdout, "Sync finished — %d skill(s), %d plugin(s), %d cache index entr(ies). One merged bundle: agentpack-bundle.\n", result.Skills, result.Plugins, result.IndexEntries)
			}
		}
	case "preflight":
		args := invocation.Args
		if invocation.Global.StrictExternal && !hasBeforeDoubleDash(args, "--strict-external") {
			args = append(append([]string{}, args...), "--strict-external")
		}
		code, preflightErr := runner.runPreflight(root, workspace, args, invocation.Global.Quiet)
		return code, runner.reportIntegrity(preflightErr)
	case "probe":
		code, probeErr := runner.runProbe(ctx, root, workspace, invocation.Args, invocation.Global.Quiet)
		return code, runner.reportIntegrity(probeErr)
	case "config":
		if len(invocation.Args) == 0 {
			return 2, fmt.Errorf("config requires an action (compare)")
		}
		switch invocation.Args[0] {
		case "compare":
			code, compareErr := runner.runConfigCompare(workspace, invocation.Args[1:], invocation.Global.Quiet)
			return code, runner.reportIntegrity(compareErr)
		default:
			return 2, fmt.Errorf("unknown config action %q", invocation.Args[0])
		}
	case "support":
		code, supportErr := runner.runSupport(workspace, invocation.Args, invocation.Global.Quiet)
		return code, runner.reportIntegrity(supportErr)
	case "env":
		err = runner.runEnv(ctx, workspace, invocation.Args, invocation.Global.Quiet)
	case "claude", "opencode", "codex", "grok", "agy", "agent":
		code, err := runner.launch(ctx, packContext, invocation)
		return code, runner.reportIntegrity(err)
	case "mcp":
		err = runner.runMCP(ctx, root, invocation.Args, invocation.Global.Quiet)
	case "mode":
		err = runner.runMode(root, invocation.Args, invocation.Global.Quiet)
	case "extra":
		err = runner.runExtra(workspace, invocation.Args, invocation.Global.Quiet)
	default:
		return 2, fmt.Errorf("unknown command %q", invocation.Command)
	}
	if err != nil {
		return 1, runner.reportIntegrity(err)
	}
	return 0, nil
}

// reportIntegrity prints an integrity failure as plain lines, because the
// error renderer reflows text and would break digests and paths.
func (runner Runner) reportIntegrity(err error) error {
	var failure interface {
		Details() string
		Summary() string
	}
	if !errors.As(err, &failure) {
		return err
	}
	fmt.Fprint(runner.Stderr, failure.Details())
	return errors.New(failure.Summary())
}

func dependencySelector(project *manifest.Manifest, module string) string {
	if project == nil {
		return ""
	}
	dependency, found := project.Dependencies[module]
	if !found {
		return ""
	}
	if dependency.Short != nil {
		if *dependency.Short == "" {
			return "HEAD"
		}
		return *dependency.Short
	}
	if dependency.Table == nil {
		return ""
	}
	switch {
	case dependency.Table.Path != nil:
		return "path:" + *dependency.Table.Path
	case dependency.Table.Commit != nil:
		return "commit:" + *dependency.Table.Commit
	case dependency.Table.Branch != nil:
		return "branch:" + *dependency.Table.Branch
	case dependency.Table.Tag != nil:
		return "tag:" + *dependency.Table.Tag
	case dependency.Table.Version != nil:
		return "version:" + *dependency.Table.Version
	default:
		return "HEAD"
	}
}

func (runner Runner) runInit(invocation Invocation) error {
	root, err := paths.ResolveProjectRootOrCWD(invocation.Global.ProjectRoot)
	if invocation.Global.ProjectRoot != "" {
		root, err = filepath.Abs(invocation.Global.ProjectRoot)
	}
	if err != nil {
		return err
	}
	args := invocation.Args
	name, args, _, err := takeFlag(args, "--name")
	if err != nil {
		return err
	}
	version, args, _, err := takeFlag(args, "--version")
	if err != nil {
		return err
	}
	if err := noArgs(args); err != nil {
		return err
	}
	if name == "" {
		name = filepath.Base(root)
	}
	if version == "" {
		version = "0.0.1"
	}
	if !invocation.Global.Quiet {
		fmt.Fprintln(runner.Stdout, "Initializing agentpack…")
	}
	if err := manifest.WriteStub(root, name, version); err != nil {
		return err
	}
	if err := lockfile.Init(root, name, version); err != nil {
		return err
	}
	if !invocation.Global.Quiet {
		fmt.Fprintf(runner.Stdout, "Created %s and %s\n", paths.ManifestPath(root), paths.LockPath(root))
	}
	return nil
}

func resolveInvocationContext(invocation Invocation, allowMissing bool) (paths.Context, error) {
	definitionRoot := invocation.Global.DefinitionRoot
	if invocation.Global.Env != "" {
		if definitionRoot != "" {
			return paths.Context{}, fmt.Errorf("use either --env or --definition-root, not both")
		}
		resolved, err := environment.ResolveRef(invocation.Global.Env)
		if err != nil {
			return paths.Context{}, err
		}
		definitionRoot = resolved
	}
	workspaceHint := firstNonEmpty(invocation.Global.WorkspaceRoot, invocation.Global.ProjectRoot)
	if workspaceHint == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return paths.Context{}, err
		}
		workspaceHint = cwd
	}
	bound := ""
	// Explicit selector wins; never fall through from a bad --env/--definition-root to a binding.
	if definitionRoot == "" {
		if abs, err := filepath.Abs(workspaceHint); err == nil {
			if binding, found, bindErr := environment.LoadBinding(abs); bindErr != nil {
				return paths.Context{}, bindErr
			} else if found {
				bound = binding.DefinitionRoot
			}
		}
	}
	if allowMissing {
		return paths.ResolveContextOrCWD(invocation.Global.ProjectRoot, definitionRoot, invocation.Global.WorkspaceRoot, bound)
	}
	return paths.ResolveContext(invocation.Global.ProjectRoot, definitionRoot, invocation.Global.WorkspaceRoot, bound)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func (runner Runner) launch(ctx context.Context, packContext paths.Context, invocation Invocation) (int, error) {
	targetName := invocation.Command
	if targetName == "agent" {
		targetName = "cursor"
	}
	target, err := base.ParseTarget(targetName)
	if err != nil {
		return 1, err
	}
	if invocation.Global.StrictExternal && target.UsesWorkspaceOverlay() {
		return 1, fmt.Errorf("%s requires a workspace overlay write; refuse under --strict-external (use claude/opencode, or omit --strict-external)", target)
	}
	effective, skipped, err := runner.Service.SyncForLaunchOptions(ctx, packContext.DefinitionRoot, packSync.SyncOptions{
		Mode: invocation.Global.Mode, Target: &target,
		WorkspaceRoot: packContext.WorkspaceRoot, StrictExternal: invocation.Global.StrictExternal,
	})
	if err != nil {
		return 1, err
	}
	if invocation.Global.Debug {
		fmt.Fprintf(runner.Stderr, "agentpack: target=%s mode=%s definition=%s workspace=%s fast-sync=%t\n", target, effective.Name(), packContext.DefinitionRoot, packContext.WorkspaceRoot, skipped)
	}
	harness, err := registry.ByTarget(target)
	if err != nil {
		return 1, err
	}
	args := invocation.Args
	if len(args) > 0 && args[0] == "--" {
		args = args[1:]
	}
	launch := base.LaunchContext{
		ProjectRoot:    packContext.DefinitionRoot,
		WorkspaceRoot:  packContext.WorkspaceRoot,
		Arguments:      args,
		Mode:           effective,
		Yolo:           invocation.Global.Yolo,
		StrictExternal: invocation.Global.StrictExternal,
	}
	command, err := harness.LaunchCommand(launch)
	if err != nil {
		return 1, err
	}
	launch.Command = command
	command.Stdin, command.Stdout, command.Stderr = runner.Stdin, runner.Stdout, runner.Stderr
	stagingLock, err := staging.AcquireLaunchShared(packContext.DefinitionRoot, effective.Name())
	if err != nil {
		return 1, err
	}
	defer stagingLock.Unlock()
	if invocation.Global.Proxy {
		running, err := proxy.Start(packContext.DefinitionRoot)
		if err != nil {
			return 1, err
		}
		if invocation.Global.Debug {
			fmt.Fprintf(runner.Stderr, "agentpack: Claude proxy listening on %s\n", running.Server.BaseURL())
		}
		running.Apply(command)
		code, runErr := runProcess(command)
		running.Shutdown()
		return code, errors.Join(runErr, harness.AfterLaunch(launch))
	}
	code, runErr := runProcess(command)
	return code, errors.Join(runErr, harness.AfterLaunch(launch))
}

func noArgs(arguments []string) error {
	if len(arguments) != 0 {
		return fmt.Errorf("unexpected arguments: %s", strings.Join(arguments, " "))
	}
	return nil
}

func hasBeforeDoubleDash(arguments []string, values ...string) bool {
	for _, argument := range arguments {
		if argument == "--" {
			return false
		}
		for _, value := range values {
			if argument == value {
				return true
			}
		}
	}
	return false
}

func writeJSON(output io.Writer, value any) error {
	encoder := json.NewEncoder(output)
	encoder.SetEscapeHTML(false)
	return encoder.Encode(value)
}
