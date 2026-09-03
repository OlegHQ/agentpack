package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/OlegHQ/agentpack/internal/lockfile"
	"github.com/OlegHQ/agentpack/internal/manifest"
	"github.com/OlegHQ/agentpack/internal/mcp"
	"github.com/OlegHQ/agentpack/internal/staging"
	packSync "github.com/OlegHQ/agentpack/internal/sync"
)

func (runner Runner) runMCP(ctx context.Context, root string, arguments []string, quiet bool) error {
	if len(arguments) == 0 {
		return fmt.Errorf("mcp requires add, remove, or list")
	}
	action, args := arguments[0], arguments[1:]
	switch action {
	case "add":
		if len(args) == 0 {
			return fmt.Errorf("mcp add requires a name")
		}
		name := args[0]
		args = args[1:]
		command, remaining, found, err := takeFlag(args, "--command")
		if err != nil {
			return err
		}
		if !found {
			return fmt.Errorf("mcp add requires --command")
		}
		args = remaining
		args, noSync := takeBool(args, "--no-sync")
		serverArgs, environment, err := parseMCPLists(args)
		if err != nil {
			return err
		}
		if err := manifest.AddMCPServer(root, name, mcp.Server{Command: &command, Args: serverArgs, Env: environment}); err != nil {
			return err
		}
		if !quiet {
			fmt.Fprintf(runner.Stdout, "Added MCP server %q to agentpack.toml\n", name)
		}
		if !noSync {
			_, err = runner.Service.Sync(ctx, root, packSync.SyncOptions{})
		}
		return err
	case "remove":
		args, noSync := takeBool(args, "--no-sync")
		if len(args) != 1 {
			return fmt.Errorf("mcp remove requires one name")
		}
		removed, err := manifest.RemoveMCPServer(root, args[0])
		if err != nil {
			return err
		}
		if !removed {
			return fmt.Errorf("no MCP server named %q in agentpack.toml [mcp.servers]", args[0])
		}
		if !noSync {
			_, err = runner.Service.Sync(ctx, root, packSync.SyncOptions{})
		}
		return err
	case "list":
		if err := noArgs(args); err != nil {
			return err
		}
		project, err := manifest.Load(root)
		if err != nil {
			return err
		}
		lock, err := lockfile.Load(root)
		if err != nil {
			return err
		}
		entries, err := staging.CollectMCP(root, lock, project, nil)
		if err != nil {
			return err
		}
		if len(entries) == 0 {
			fmt.Fprintln(runner.Stdout, "No MCP servers configured.")
			return nil
		}
		for _, name := range entries.Names() {
			entry := entries[name]
			shown := ""
			if entry.Server.URL != nil {
				shown = *entry.Server.URL
			} else if entry.Server.Command != nil {
				shown = strings.TrimSpace(*entry.Server.Command + " " + strings.Join(entry.Server.Args, " "))
			}
			disabled := ""
			if entry.Server.Disabled != nil && *entry.Server.Disabled {
				disabled = " (disabled)"
			}
			fmt.Fprintf(runner.Stdout, "  %s: %s [from %s]%s\n", name, shown, entry.Source, disabled)
		}
		return nil
	default:
		return fmt.Errorf("unknown mcp action %q", action)
	}
}

func parseMCPLists(arguments []string) ([]string, map[string]string, error) {
	var commandArgs []string
	environment := make(map[string]string)
	section := ""
	for index := 0; index < len(arguments); index++ {
		switch arguments[index] {
		case "--args":
			section = "args"
		case "--env":
			section = "env"
		default:
			if section == "args" {
				commandArgs = append(commandArgs, arguments[index])
				continue
			}
			if section == "env" {
				key, value, found := strings.Cut(arguments[index], "=")
				if !found {
					return nil, nil, fmt.Errorf("expected KEY=VALUE, got %q", arguments[index])
				}
				environment[key] = value
				continue
			}
			return nil, nil, fmt.Errorf("unexpected argument %q", arguments[index])
		}
	}
	return commandArgs, environment, nil
}
