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

	base "github.com/OlegHQ/agentpack/internal/harness"
	"github.com/OlegHQ/agentpack/internal/harness/registry"
	"github.com/OlegHQ/agentpack/internal/lockfile"
	"github.com/OlegHQ/agentpack/internal/manifest"
	"github.com/OlegHQ/agentpack/internal/paths"
	"github.com/OlegHQ/agentpack/internal/proxy"
	packSync "github.com/OlegHQ/agentpack/internal/sync"
)

var Version = "0.3.13"

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
	if len(arguments) == 0 || arguments[0] == "--help" || arguments[0] == "-h" {
		fmt.Fprint(runner.Stdout, usage)
		return 0, nil
	}
	invocation, err := Parse(arguments)
	if err != nil {
		return 2, err
	}
	if invocation.Command != "init" && hasBeforeDoubleDash(invocation.Args, "--version", "-V") {
		fmt.Fprintln(runner.Stdout, "agentpack "+Version)
		return 0, nil
	}
	if hasBeforeDoubleDash(invocation.Args, "--help", "-h") {
		fmt.Fprint(runner.Stdout, helpFor(invocation.Command, invocation.Args))
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
	allowsMissing := map[string]bool{"add": true, "claude": true, "opencode": true, "codex": true, "grok": true, "agy": true, "agent": true}
	var root string
	if allowsMissing[invocation.Command] {
		root, err = paths.ResolveProjectRootOrCWD(invocation.Global.ProjectRoot)
	} else {
		root, err = paths.ResolveProjectRoot(invocation.Global.ProjectRoot)
	}
	if err != nil {
		return 1, err
	}
	switch invocation.Command {
	case "lock":
		args, update := takeBool(invocation.Args, "--update")
		if err := noArgs(args); err != nil {
			return 2, err
		}
		var locked lockfile.PackLock
		locked, err = runner.Service.Lock(ctx, root, update)
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
	case "sync":
		args, dry := takeBool(invocation.Args, "--dry-run")
		args, verify := takeBool(args, "--verify-only")
		args, update := takeBool(args, "--update-lock")
		if err := noArgs(args); err != nil {
			return 2, err
		}
		var result packSync.SyncResult
		result, err = runner.Service.Sync(ctx, root, packSync.SyncOptions{DryRun: dry, VerifyOnly: verify, UpdateLock: update, Mode: invocation.Global.Mode})
		if err == nil && !invocation.Global.Quiet {
			if dry {
				fmt.Fprintf(runner.Stdout, "Dry-run: would sync %d skill(s), %d plugin(s); %d skill(s) shadowed by plugins (omitted from staging); no changes made.\n", result.Skills, result.Plugins, result.Shadowed)
			} else if verify {
				fmt.Fprintln(runner.Stdout, "Staging checks passed")
			} else {
				fmt.Fprintf(runner.Stdout, "Sync finished — %d skill(s), %d plugin(s), %d cache index entr(ies). One merged bundle: agentpack-bundle.\n", result.Skills, result.Plugins, result.IndexEntries)
			}
		}
	case "claude", "opencode", "codex", "grok", "agy", "agent":
		return runner.launch(ctx, root, invocation)
	case "mcp":
		err = runner.runMCP(ctx, root, invocation.Args, invocation.Global.Quiet)
	case "mode":
		err = runner.runMode(root, invocation.Args, invocation.Global.Quiet)
	default:
		return 2, fmt.Errorf("unknown command %q", invocation.Command)
	}
	if err != nil {
		return 1, err
	}
	return 0, nil
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

func (runner Runner) launch(ctx context.Context, root string, invocation Invocation) (int, error) {
	targetName := invocation.Command
	if targetName == "agent" {
		targetName = "cursor"
	}
	target, err := base.ParseTarget(targetName)
	if err != nil {
		return 1, err
	}
	effective, skipped, err := runner.Service.SyncForLaunch(ctx, root, invocation.Global.Mode, target)
	if err != nil {
		return 1, err
	}
	if invocation.Global.Debug {
		fmt.Fprintf(runner.Stderr, "agentpack: target=%s mode=%s fast-sync=%t\n", target, effective.Name(), skipped)
	}
	harness, err := registry.ByTarget(target)
	if err != nil {
		return 1, err
	}
	args := invocation.Args
	if len(args) > 0 && args[0] == "--" {
		args = args[1:]
	}
	command, err := harness.LaunchCommand(base.LaunchContext{ProjectRoot: root, Arguments: args, Mode: effective, Yolo: invocation.Global.Yolo})
	if err != nil {
		return 1, err
	}
	command.Stdin, command.Stdout, command.Stderr = runner.Stdin, runner.Stdout, runner.Stderr
	if invocation.Global.Proxy {
		running, err := proxy.Start(root)
		if err != nil {
			return 1, err
		}
		if invocation.Global.Debug {
			fmt.Fprintf(runner.Stderr, "agentpack: Claude proxy listening on %s\n", running.Server.BaseURL())
		}
		running.Apply(command)
		code, runErr := runProcess(command)
		running.Shutdown()
		return code, runErr
	}
	return runProcess(command)
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

const usage = `Pin skills/plugins via agentpack.toml and pack.lock

Usage: agentpack [GLOBAL OPTIONS] <COMMAND> [ARGS]

Commands: init, lock, add, remove, sync, claude, opencode, codex, grok, agy,
          agent, mcp, mode, hook-exec
Global:   --project-root PATH, -q/--quiet, --no-progress, --yolo, --mode NAME,
          --debug, --proxy
`
