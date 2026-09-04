package cli

import (
	"context"
	"slices"

	"github.com/charmbracelet/fang"
	"github.com/spf13/cobra"
)

// Execute runs agentpack through Cobra's command model and Fang's terminal
// presentation. Runner.Run remains the behavior boundary; the command model
// owns discovery, contextual help, completion, version, and error rendering.
func (runner Runner) Execute(ctx context.Context, arguments []string) (int, error) {
	if len(arguments) == 1 && arguments[0] == "-V" {
		arguments = slices.Clone(arguments)
		arguments[0] = "--version"
	}
	exitCode := 0
	root := runner.rootCommand(arguments, &exitCode)
	root.SetArgs(arguments)
	root.SetIn(runner.Stdin)
	root.SetOut(runner.Stdout)
	root.SetErr(runner.Stderr)
	err := fang.Execute(ctx, root, fang.WithVersion(Version), fang.WithoutManpage())
	if err != nil && exitCode == 0 {
		exitCode = 2
	}
	return exitCode, err
}

func (runner Runner) rootCommand(original []string, exitCode *int) *cobra.Command {
	root := &cobra.Command{
		Use:           "agentpack",
		Short:         "Pin skills and plugins for every coding agent",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(command *cobra.Command, _ []string) error {
			return command.Help()
		},
	}
	root.SetVersionTemplate("agentpack {{.Version}}\n")

	var global Global
	flags := root.PersistentFlags()
	flags.StringVar(&global.ProjectRoot, "project-root", "", "project root containing agentpack.toml")
	flags.BoolVarP(&global.Quiet, "quiet", "q", false, "suppress status output")
	flags.BoolVar(&global.NoProgress, "no-progress", false, "disable progress output")
	flags.BoolVar(&global.Yolo, "yolo", false, "enable unattended harness execution")
	flags.StringVar(&global.Mode, "mode", "", "project-local mode to stage")
	flags.BoolVar(&global.Debug, "debug", false, "print launch diagnostics")
	flags.BoolVar(&global.Proxy, "proxy", false, "route Claude through the agentpack proxy")

	leaf := func(use, short string) *cobra.Command {
		command := &cobra.Command{Use: use, Short: short, DisableFlagParsing: true}
		command.RunE = func(command *cobra.Command, args []string) error {
			if hasBeforeDoubleDash(args, "--help", "-h") {
				return command.Help()
			}
			code, err := runner.Run(command.Context(), original)
			*exitCode = code
			return err
		}
		return command
	}
	flag := func(command *cobra.Command, name, shorthand, usage string) {
		if shorthand == "" {
			command.Flags().Bool(name, false, usage)
		} else {
			command.Flags().BoolP(name, shorthand, false, usage)
		}
	}
	value := func(command *cobra.Command, name, placeholder, usage string) {
		command.Flags().String(name, "", usage)
		command.Flags().Lookup(name).Usage = usage + " (" + placeholder + ")"
	}

	init := leaf("init", "Create agentpack.toml and pack.lock")
	value(init, "name", "NAME", "project name")
	value(init, "version", "VERSION", "project version")
	lock := leaf("lock", "Resolve the manifest and refresh pack.lock")
	flag(lock, "update", "", "refresh floating dependency pins")
	add := leaf("add SPEC", "Add and pin a package dependency")
	flag(add, "no-sync", "", "record the change without staging")
	remove := leaf("remove SPEC", "Remove a direct package dependency")
	flag(remove, "no-sync", "", "record the change without staging")
	syncCommand := leaf("sync", "Ensure cache and staging match pack.lock")
	flag(syncCommand, "dry-run", "", "show what would change")
	flag(syncCommand, "verify-only", "", "verify existing cache and staging")
	flag(syncCommand, "update-lock", "", "refresh the lock before staging")

	root.AddCommand(init, lock, add, remove, syncCommand)
	agent := leaf("agent [ARGS...]", "Launch Cursor Agent with a staged HOME")
	agent.Aliases = []string{"cursor-agent"}
	root.AddCommand(
		leaf("claude [ARGS...]", "Launch Claude Code with staged plugins"),
		leaf("opencode [ARGS...]", "Launch OpenCode with staged configuration"),
		leaf("codex [ARGS...]", "Launch Codex with a staged CODEX_HOME"),
		leaf("grok [ARGS...]", "Launch Grok with a staged GROK_HOME"),
		leaf("agy [ARGS...]", "Launch Antigravity with the project workspace"),
		agent,
	)

	mcp := &cobra.Command{Use: "mcp", Short: "Manage MCP servers"}
	mcpAdd := leaf("add NAME", "Add or replace an MCP server")
	value(mcpAdd, "command", "COMMAND", "server executable")
	value(mcpAdd, "args", "ARGS...", "arguments passed to the server")
	value(mcpAdd, "env", "KEY=VALUE...", "environment passed to the server")
	flag(mcpAdd, "no-sync", "", "record the change without staging")
	mcpRemove := leaf("remove NAME", "Remove an MCP server")
	flag(mcpRemove, "no-sync", "", "record the change without staging")
	mcp.AddCommand(mcpAdd, mcpRemove, leaf("list", "List configured MCP servers"))
	root.AddCommand(mcp)

	mode := &cobra.Command{Use: "mode", Short: "Manage project-local staging modes"}
	mode.AddCommand(
		leaf("list", "List modes"),
		leaf("show NAME", "Show a mode definition"),
		leaf("create NAME", "Create a mode"),
		leaf("delete NAME", "Delete a mode"),
		leaf("enable NAME [SELECTOR...]", "Enable capabilities in a mode"),
		leaf("disable NAME [SELECTOR...]", "Disable capabilities in a mode"),
		leaf("base NAME <all|none>", "Set a mode's default selection"),
		leaf("tui [NAME]", "Open the interactive mode editor"),
	)
	root.AddCommand(mode)

	hook := leaf("hook-exec", "Internal hook execution bridge")
	hook.Hidden = true
	root.AddCommand(hook)
	return root
}
