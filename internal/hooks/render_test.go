package hooks

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/OlegHQ/agentpack/internal/harness"
)

func TestExecutionSpecPathCommandAndRoundTrip(t *testing.T) {
	t.Parallel()
	hook := Hook{Event: PreToolUse, Matcher: "Bash", Handler: Handler{Kind: CommandHandler, Command: "check"}, Origin: Origin{PackageKey: "pkg", EventIndex: 1, MatcherGroupIndex: 2, HookIndex: 3}}
	context := RenderContext{TargetRoot: "/tmp/root with space", StagedPackages: map[string]string{"pkg": "/tmp/package"}}
	var output RenderOutput
	path, err := BuildExecutionSpec(harness.Claude, hook, hook.Event, context, &output)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(filepath.ToSlash(path), "hooks/_packages/pkg/specs/001-002-003.json") || len(output.Files) != 1 {
		t.Fatalf("path/files = %s %#v", path, output.Files)
	}
	command := HookExecCommand(CommandHandler, harness.Claude, path)
	if !strings.Contains(command, "--target claude") || !strings.Contains(command, "'") {
		t.Fatalf("command = %s", command)
	}
	data, err := json.Marshal(output.Files[0].JSON)
	if err != nil {
		t.Fatal(err)
	}
	var spec ExecutionSpec
	if err := json.Unmarshal(data, &spec); err != nil || spec.Handler.Command != "check" || spec.Matcher != "Bash" {
		t.Fatalf("spec = %#v, %v", spec, err)
	}
}

func TestUnsupportedStrictHookFailsAndNonStrictIsOmitted(t *testing.T) {
	t.Parallel()
	strict := Hook{Event: PermissionRequest, Origin: Origin{SourceFile: "hooks.json"}}
	var output RenderOutput
	if _, err := CheckSupport(harness.OpenCode, strict, Support{Kind: Unsupported, Reason: "no mapping"}, &output, "", ""); err == nil {
		t.Fatal("expected strict mapping error")
	}
	nonstrict := Hook{Event: Notification}
	keep, err := CheckSupport(harness.Cursor, nonstrict, Support{Kind: Unsupported, Reason: "none"}, &output, "", "")
	if err != nil || keep || output.Summary.Omitted != 1 {
		t.Fatalf("keep/output = %v %#v, %v", keep, output, err)
	}
}
