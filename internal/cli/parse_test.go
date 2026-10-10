package cli

import (
	"reflect"
	"testing"
)

func TestParseGlobalOptionsAcrossCommandAndProtectTrailingArguments(t *testing.T) {
	got, err := Parse([]string{"--project-root=/tmp/project", "claude", "--mode", "review", "--", "--debug", "hello"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Command != "claude" || got.Global.ProjectRoot != "/tmp/project" || got.Global.Mode != "review" {
		t.Fatalf("unexpected invocation: %#v", got)
	}
	if !reflect.DeepEqual(got.Args, []string{"--", "--debug", "hello"}) {
		t.Fatalf("trailing args = %#v", got.Args)
	}
}

func TestParseDefinitionWorkspaceAndStrictExternal(t *testing.T) {
	got, err := Parse([]string{"--definition-root", "/def", "--workspace=/ws", "--strict-external", "preflight", "--json"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Command != "preflight" || got.Global.DefinitionRoot != "/def" || got.Global.WorkspaceRoot != "/ws" || !got.Global.StrictExternal {
		t.Fatalf("unexpected invocation: %#v", got)
	}
	if !reflect.DeepEqual(got.Args, []string{"--json"}) {
		t.Fatalf("args = %#v", got.Args)
	}
}

func TestParseEnvSelector(t *testing.T) {
	got, err := Parse([]string{"--env", "team-review", "--workspace", "/ws", "preflight"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Global.Env != "team-review" || got.Global.WorkspaceRoot != "/ws" || got.Command != "preflight" {
		t.Fatalf("unexpected invocation: %#v", got)
	}
}

func TestParseLeavesMCPEnvAssignments(t *testing.T) {
	got, err := Parse([]string{"mcp", "add", "docs", "--command", "npx", "--env", "TOKEN=value"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Global.Env != "" {
		t.Fatalf("mcp --env KEY=VALUE must not become global env selector: %#v", got.Global)
	}
	if !reflect.DeepEqual(got.Args, []string{"add", "docs", "--command", "npx", "--env", "TOKEN=value"}) {
		t.Fatalf("args = %#v", got.Args)
	}
}

func TestParseCursorAgentAliasAndMissingValue(t *testing.T) {
	got, err := Parse([]string{"cursor-agent", "--print", "hi"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Command != "agent" || !reflect.DeepEqual(got.Args, []string{"--print", "hi"}) {
		t.Fatalf("unexpected invocation: %#v", got)
	}
	if _, err := Parse([]string{"sync", "--mode"}); err == nil {
		t.Fatal("expected missing global value error")
	}
}

func TestParseMCPListsPreservesCommandArgumentOrder(t *testing.T) {
	arguments, environment, err := parseMCPLists([]string{"--args", "-y", "server", "--env", "TOKEN=abc", "EMPTY="})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(arguments, []string{"-y", "server"}) || environment["TOKEN"] != "abc" || environment["EMPTY"] != "" {
		t.Fatalf("args=%#v env=%#v", arguments, environment)
	}
}
