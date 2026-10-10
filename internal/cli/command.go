package cli

import (
	"context"
	"image/color"
	"slices"

	lipgloss "charm.land/lipgloss/v2"
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
	err := fang.Execute(ctx, root, fang.WithVersion(Version), fang.WithoutManpage(), fang.WithColorSchemeFunc(monochromeColorScheme))
	if err != nil && exitCode == 0 {
		exitCode = 2
	}
	return exitCode, err
}

func monochromeColorScheme(_ lipgloss.LightDarkFunc) fang.ColorScheme {
	foreground := lipgloss.NoColor{}
	return fang.ColorScheme{
		Base: foreground, Title: foreground, Description: foreground,
		Codeblock: foreground, Program: foreground, DimmedArgument: foreground,
		Comment: foreground, Flag: foreground, FlagDefault: foreground,
		Command: foreground, QuotedString: foreground, Argument: foreground,
		Help: foreground, Dash: foreground,
		ErrorHeader:  [2]color.Color{foreground, foreground},
		ErrorDetails: foreground,
	}
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
	flags.StringVar(&global.ProjectRoot, "project-root", "", "legacy single root (definition+workspace when no split flags)")
	flags.StringVar(&global.DefinitionRoot, "definition-root", "", "external directory holding agentpack.toml and pack.lock")
	flags.StringVar(&global.Env, "env", "", "external environment name or path (fails if missing; no fallthrough)")
	flags.StringVar(&global.WorkspaceRoot, "workspace", "", "checkout / agent cwd for .agents and overlays")
	flags.BoolVar(&global.StrictExternal, "strict-external", false, "refuse workspace overlay writes (Cursor/Agy)")
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
	flag(lock, "allow-unpinned-mcp", "", "record an MCP server as unpinned when its registry is unreachable")
	add := leaf("add SPEC", "Add and pin a package dependency")
	flag(add, "no-sync", "", "record the change without staging")
	remove := leaf("remove SPEC", "Remove a direct package dependency")
	flag(remove, "no-sync", "", "record the change without staging")
	update := leaf("update [SPEC...]", "Refresh one dependency, or every floating dependency")
	flag(update, "no-sync", "", "refresh pack.lock without staging")
	list := leaf("list", "List resolved package dependencies")
	syncCommand := leaf("sync", "Ensure cache and staging match pack.lock")
	flag(syncCommand, "dry-run", "", "show what would change")
	flag(syncCommand, "verify-only", "", "verify existing cache and staging")
	flag(syncCommand, "update-lock", "", "refresh the lock before staging")
	flag(syncCommand, "repair", "", "re-fetch cache entries that fail content verification")
	preflight := leaf("preflight", "Pure offline check of a locked environment (no mutation)")
	flag(preflight, "json", "", "emit a JSON report")
	flag(preflight, "strict-external", "", "treat workspace overlay targets as violations")
	value(preflight, "agent", "TARGET", "harness target to check (claude, opencode, ...)")
	value(preflight, "policy", "local|ci", "inheritance policy")
	value(preflight, "contract", "PATH", "versioned contract.json (default: <definition>/contract.json)")
	value(preflight, "receipt", "ID", "compare a prior probe receipt (freshness / observed coverage)")
	probe := leaf("probe", "Run an explicit native observation probe (writes a receipt)")
	flag(probe, "json", "", "emit the receipt JSON")
	value(probe, "agent", "claude|codex", "native adapter to probe")
	value(probe, "mode", "NAME", "staged mode whose generation to probe")

	configCmd := &cobra.Command{Use: "config", Short: "Compare configuration observation receipts"}
	configCompare := leaf("compare RECEIPT_A RECEIPT_B", "Diff two probe receipts")
	flag(configCompare, "json", "", "emit JSON comparison")
	configCmd.AddCommand(configCompare)
	supportCmd := &cobra.Command{Use: "support", Short: "Export redacted diagnostic capsules"}
	supportExport := leaf("export RECEIPT_ID", "Write a redacted support capsule")
	value(supportExport, "output", "PATH", "destination JSON file")
	flag(supportExport, "json", "", "also emit the capsule on stdout")
	supportCmd.AddCommand(supportExport)

	root.AddCommand(init, lock, add, remove, update, list, syncCommand, preflight, probe, configCmd, supportCmd)
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
	flag(mcpAdd, "allow-unpinned", "", "record the server as unpinned when its registry is unreachable")
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

	env := &cobra.Command{Use: "env", Short: "Manage portable external environments"}
	envInit := leaf("init [NAME]", "Create an external definition directory")
	value(envInit, "name", "NAME", "environment name under AGENTPACK_HOME/environments")
	value(envInit, "dir", "PATH", "explicit definition directory")
	value(envInit, "from", "PATH", "copy agentpack.toml and pack.lock from a project")
	envUse := leaf("use REF", "Bind the workspace to an external definition")
	value(envUse, "project", "PATH", "workspace checkout to bind")
	envBind := leaf("bind REF", "Alias for env use")
	value(envBind, "project", "PATH", "workspace checkout to bind")
	envExport := leaf("export PATH", "Export definition+lock as a portable bundle")
	value(envExport, "from", "REF", "environment name or definition path")
	envImport := leaf("import PATH", "Import a portable bundle")
	value(envImport, "name", "NAME", "destination environment name")
	value(envImport, "dir", "PATH", "explicit destination directory")
	env.AddCommand(
		envInit,
		envUse,
		envBind,
		leaf("unuse", "Clear the workspace environment binding"),
		leaf("list", "List environments under AGENTPACK_HOME"),
		leaf("status", "Show binding and storage location"),
		envExport,
		envImport,
		leaf("restore", "Fetch locked cache inputs without changing pack.lock"),
	)
	root.AddCommand(env)

	extra := &cobra.Command{Use: "extra", Short: "Optional, non-core commands"}
	syncClaude := leaf("sync-claude", "Reconcile .claude/skills and .agents/skills")
	flag(syncClaude, "dry-run", "", "show what would change")
	extra.AddCommand(syncClaude)
	root.AddCommand(extra)

	hook := leaf("hook-exec", "Internal hook execution bridge")
	hook.Hidden = true
	root.AddCommand(hook)
	return root
}
