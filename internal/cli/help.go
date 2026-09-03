package cli

import "fmt"

func helpFor(command string, arguments []string) string {
	action := ""
	if len(arguments) != 0 && arguments[0] != "--help" && arguments[0] != "-h" {
		action = arguments[0]
	}
	key := command
	if action != "" {
		key += " " + action
	}
	if text := commandHelp[key]; text != "" {
		return text
	}
	if text := commandHelp[command]; text != "" {
		return text
	}
	return fmt.Sprintf("Usage: agentpack %s [OPTIONS]\n", command)
}

var commandHelp = map[string]string{
	"init":       "Create agentpack.toml and pack.lock (v2).\n\nUsage: agentpack init [--name NAME] [--version VERSION]\n",
	"lock":       "Resolve agentpack.toml and refresh pack.lock.\n\nUsage: agentpack lock [--update]\n",
	"add":        "Add and pin a package dependency.\n\nUsage: agentpack add <SPEC> [--no-sync]\n",
	"remove":     "Remove a direct package dependency.\n\nUsage: agentpack remove <SPEC> [--no-sync]\n",
	"sync":       "Ensure cache and staging match pack.lock.\n\nUsage: agentpack sync [--dry-run] [--verify-only] [--update-lock]\n",
	"claude":     "Launch Claude Code with staged agentpack plugins.\n\nUsage: agentpack claude [ARGS]...\n",
	"opencode":   "Launch OpenCode with staged configuration.\n\nUsage: agentpack opencode [ARGS]...\n",
	"codex":      "Launch Codex with a staged CODEX_HOME.\n\nUsage: agentpack codex [ARGS]...\n",
	"grok":       "Launch Grok with a staged GROK_HOME.\n\nUsage: agentpack grok [ARGS]...\n",
	"agy":        "Launch Antigravity with the project workspace.\n\nUsage: agentpack agy [ARGS]...\n",
	"agent":      "Launch Cursor Agent with a staged HOME.\n\nUsage: agentpack agent [ARGS]...\n",
	"mcp":        "Manage MCP servers.\n\nUsage: agentpack mcp <add|remove|list>\n",
	"mcp add":    "Usage: agentpack mcp add <NAME> --command <COMMAND> [--args ...] [--env KEY=VALUE ...] [--no-sync]\n",
	"mcp remove": "Usage: agentpack mcp remove <NAME> [--no-sync]\n",
	"mcp list":   "Usage: agentpack mcp list\n",
	"mode":       "Manage project-local modes.\n\nUsage: agentpack mode <list|show|create|delete|enable|disable|base|tui>\n",
	"mode list":  "Usage: agentpack mode list\n", "mode show": "Usage: agentpack mode show <NAME>\n", "mode create": "Usage: agentpack mode create <NAME>\n", "mode delete": "Usage: agentpack mode delete <NAME>\n", "mode enable": "Usage: agentpack mode enable <NAME> [SELECTOR]...\n", "mode disable": "Usage: agentpack mode disable <NAME> [SELECTOR]...\n", "mode base": "Usage: agentpack mode base <NAME> <all|none>\n", "mode tui": "Usage: agentpack mode tui [NAME]\n",
	"hook-exec": "Internal hook execution bridge.\n\nUsage: agentpack hook-exec <command|http|prompt|agent|dispatch|inject-guidance> [OPTIONS]\n",
}
