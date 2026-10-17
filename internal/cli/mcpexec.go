package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/olegpustovit/agentpack/internal/mcp"
	"github.com/olegpustovit/agentpack/internal/paths"
)

// runMCPExec starts an npm MCP server from its locked dependency tree:
// `agentpack mcp-exec --spec FILE -- ARGS...`. The tree is installed into the
// user cache on first use, every tarball checked against pack.lock, and the
// package's executable runs with the server's own arguments on the agent's
// standard input and output.
func (runner Runner) runMCPExec(ctx context.Context, arguments []string) (int, error) {
	specPath, args, found, err := takeFlag(arguments, "--spec")
	if err != nil || !found {
		return 2, flagError(err, "--spec")
	}
	if len(args) != 0 && args[0] == "--" {
		args = args[1:]
	}
	data, err := os.ReadFile(specPath)
	if err != nil {
		return 1, fmt.Errorf("mcp-exec: %w", err)
	}
	var spec mcp.TreeSpec
	if err := json.Unmarshal(data, &spec); err != nil {
		return 1, fmt.Errorf("mcp-exec: unreadable spec %s: %w", specPath, err)
	}
	cache, err := paths.CacheDir()
	if err != nil {
		return 1, err
	}
	executable, err := mcp.InstallTree(ctx, nil, filepath.Join(cache, "npm-trees"), spec)
	if err != nil {
		return 1, err
	}
	argv := append([]string{executable}, args...)
	if runsWithNode(executable) {
		node, err := exec.LookPath("node")
		if err != nil {
			return 1, errors.New("mcp-exec: node is not on PATH")
		}
		argv = append([]string{node}, argv...)
	}
	command := exec.CommandContext(ctx, argv[0], argv[1:]...)
	command.Stdin, command.Stdout, command.Stderr = runner.Stdin, runner.Stdout, runner.Stderr
	if err := command.Run(); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return exit.ExitCode(), nil
		}
		return 1, err
	}
	return 0, nil
}

// runsWithNode reports whether a package executable is a script for node, as
// npm's bin shims decide it: a JavaScript extension or a node shebang.
func runsWithNode(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".js", ".mjs", ".cjs":
		return true
	}
	file, err := os.Open(path)
	if err != nil {
		return true
	}
	defer file.Close()
	line, _ := bufio.NewReader(file).ReadString('\n')
	return !strings.HasPrefix(line, "#!") || strings.Contains(line, "node")
}
