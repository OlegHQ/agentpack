package cli

import (
	"context"
	"fmt"
	"slices"

	"github.com/olegpustovit/agentpack/internal/eject"
	base "github.com/olegpustovit/agentpack/internal/harness"
	"github.com/olegpustovit/agentpack/internal/mode"
	packSync "github.com/olegpustovit/agentpack/internal/sync"
)

// runEject syncs, then writes the staged content into the project for the
// named agents (all it supports by default), or takes the last eject out.
func (runner Runner) runEject(ctx context.Context, root, workspace string, invocation Invocation) error {
	args, remove := takeBool(invocation.Args, "--remove")
	quiet := invocation.Global.Quiet
	if remove {
		if len(args) != 0 {
			return fmt.Errorf("eject --remove takes no agent names; it removes the whole last eject")
		}
		result, err := eject.Remove(workspace)
		if err == nil && !quiet {
			fmt.Fprintf(runner.Stdout, "Removed the eject: %d file(s) and every setting it added.\n", len(result.Removed))
		}
		return err
	}
	targets := eject.Supported()
	if len(args) != 0 {
		targets = nil
		for _, name := range args {
			if name == "agent" || name == "cursor-agent" {
				name = "cursor"
			}
			target, err := base.ParseTarget(name)
			if err != nil {
				return err
			}
			if !slices.Contains(eject.Supported(), target) {
				return fmt.Errorf("eject does not write for %s, which runs in a terminal: start it with `agentpack %s`", target, target)
			}
			if !slices.Contains(targets, target) {
				targets = append(targets, target)
			}
		}
	}
	modeName := invocation.Global.Mode
	if modeName == "" {
		modeName = mode.DefaultName
	}
	result, err := runner.Service.Sync(ctx, root, packSync.SyncOptions{Mode: modeName, WorkspaceRoot: workspace})
	if err != nil {
		return err
	}
	ejected, err := eject.Eject(eject.Options{ProjectRoot: root, WorkspaceRoot: workspace, Mode: result.Mode.Name(), Targets: targets})
	if err != nil {
		return err
	}
	if !quiet {
		names := make([]string, len(targets))
		for index, target := range targets {
			names[index] = string(target)
		}
		fmt.Fprintf(runner.Stdout, "Ejected mode %s for %v into %s; %d project file(s) and setting(s) written.\n",
			result.Mode.Name(), names, ejected.Dir, len(ejected.Written))
		fmt.Fprintln(runner.Stdout, "Run `agentpack eject` again after a change to agentpack.toml, and `agentpack eject --remove` to take it out.")
	}
	return nil
}
