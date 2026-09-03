package registry

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	base "github.com/OlegHQ/agentpack/internal/harness"
	"github.com/OlegHQ/agentpack/internal/mode"
	"github.com/OlegHQ/agentpack/internal/paths"
)

func TestHarnessLaunchCommandsOwnArgumentsAndEnvironment(t *testing.T) {
	project := t.TempDir()
	stage := t.TempDir()
	home := t.TempDir()
	binary := filepath.Join(t.TempDir(), "stub")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("AGENTPACK_HOME", t.TempDir())
	t.Setenv("AGENTPACK_STAGING_ROOT", stage)
	for _, key := range []string{"CLAUDE_CODE_PATH", "OPENCODE_PATH", "CODEX_PATH", "GROK_PATH", "AGY_PATH", "CURSOR_AGENT_PATH"} {
		t.Setenv(key, binary)
	}
	effective := mode.ImplicitEffective()
	tests := []struct {
		target                base.Target
		arguments, wantPrefix []string
		environment           string
	}{{base.Claude, []string{"hi"}, []string{"--dangerously-skip-permissions", "hi"}, ""}, {base.Codex, []string{"exec", "hi"}, []string{"exec", "--dangerously-bypass-approvals-and-sandbox", "hi"}, "CODEX_HOME="}, {base.Grok, []string{"inspect"}, []string{"--always-approve", "--cwd", project, "inspect"}, "GROK_HOME="}, {base.Agy, []string{"--print", "ok"}, []string{"--dangerously-skip-permissions", "--add-dir"}, ""}, {base.Cursor, []string{"--print", "ok"}, []string{"--trust", "--force", "--workspace"}, "CURSOR_CONFIG_DIR="}}
	for _, test := range tests {
		candidate, _ := ByTarget(test.target)
		command, err := candidate.LaunchCommand(base.LaunchContext{ProjectRoot: project, Arguments: test.arguments, Mode: effective, Yolo: true})
		if err != nil {
			t.Fatalf("%s: %v", test.target, err)
		}
		got := command.Args[1:]
		if len(got) < len(test.wantPrefix) || !reflect.DeepEqual(got[:len(test.wantPrefix)], test.wantPrefix) {
			t.Fatalf("%s args=%q want prefix=%q", test.target, got, test.wantPrefix)
		}
		if test.environment != "" && !containsEnvironment(command.Env, test.environment) {
			t.Fatalf("%s environment lacks %s: %q", test.target, test.environment, command.Env)
		}
	}
	open, _ := ByTarget(base.OpenCode)
	root, _ := paths.StagingOpenCodeDirForMode(project, "default")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "opencode.json"), []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}
	command, err := open.LaunchCommand(base.LaunchContext{ProjectRoot: project, Mode: effective, Yolo: true})
	if err != nil {
		t.Fatal(err)
	}
	if !containsEnvironment(command.Env, "OPENCODE_CONFIG_DIR=") {
		t.Fatal("missing OpenCode config env")
	}
	data, _ := os.ReadFile(filepath.Join(root, "opencode.json"))
	if !strings.Contains(string(data), `"permission": "allow"`) {
		t.Fatalf("config=%s", data)
	}
}
func containsEnvironment(environment []string, prefix string) bool {
	for _, entry := range environment {
		if strings.HasPrefix(entry, prefix) {
			return true
		}
	}
	return false
}
