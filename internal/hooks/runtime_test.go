package hooks

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/olegpustovit/agentpack/internal/harness"
)

func TestNormalizeResponseAliasesAndMetadata(t *testing.T) {
	t.Parallel()
	result := NormalizeResponse(map[string]any{
		"permissionDecision": "block", "stopReason": "no", "additionalContext": "ctx",
		"updatedInput": map[string]any{"x": true}, "updatedMCPToolOutput": "changed", "custom": 3.0,
	})
	if result.Decision != Deny || result.Message != "no" || result.AdditionalContext != "ctx" || result.UpdatedToolOutput != "changed" || result.Metadata["custom"] != 3.0 {
		t.Fatalf("result = %#v", result)
	}
	continued := NormalizeResponse(map[string]any{"continue": false})
	if continued.Decision != Deny || continued.Metadata["continue"] != false {
		t.Fatalf("continued = %#v", continued)
	}
}

func TestToolCandidatesAndFullMatch(t *testing.T) {
	t.Parallel()
	for raw, matcher := range map[string]string{"Shell": "Bash", "Write": "Edit|Write", "MCP:server__tool": "mcp__server__tool"} {
		if !MatcherMatches(matcher, CandidateToolNames(raw)) {
			t.Fatalf("%s did not match %s", raw, matcher)
		}
	}
	if MatcherMatches("Web", CandidateToolNames("WebSearch")) || !MatcherMatches("", []string{"Read"}) {
		t.Fatal("matcher anchoring/default mismatch")
	}
	if got := ExtractToolName(map[string]any{"tool": map[string]any{"name": "Read"}}); got != "Read" {
		t.Fatalf("tool = %q", got)
	}
}

func TestRunCommandForwardsStdinAndPluginRoot(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX command fixture")
	}
	root := t.TempDir()
	spec := ExecutionSpec{Handler: Handler{Kind: CommandHandler, Command: `printf '{"decision":"deny","message":"%s"}' "$AGENTPACK_PLUGIN_ROOT"; cat >/dev/null; exit 2`}, WorkingDir: root}
	output, err := RunCommand(context.Background(), spec, []byte(`{"tool_name":"Bash"}`))
	if err != nil {
		t.Fatal(err)
	}
	if output.ExitCode != 2 || !strings.Contains(string(output.Stdout), root) {
		t.Fatalf("output = %#v", output)
	}
}

func TestRunHTTPBodyAndStatusNormalization(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Header.Get("X-Test") != "yes" {
			t.Errorf("header missing")
		}
		response.WriteHeader(http.StatusForbidden)
		_, _ = response.Write([]byte(`{"additional_context":"context"}`))
	}))
	defer server.Close()
	spec := ExecutionSpec{Handler: Handler{Kind: HTTPHandler, URL: server.URL, Headers: map[string]string{"X-Test": "yes"}}}
	result, err := RunHTTP(context.Background(), server.Client(), spec, []byte(`{"input":true}`))
	if err != nil {
		t.Fatal(err)
	}
	if result.Decision != Allow || result.AdditionalContext != "context" || !strings.Contains(result.Message, "403") {
		t.Fatalf("result = %#v", result)
	}
}

func TestDispatchFiltersSpecsAndDenyWins(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX command fixture")
	}
	root := t.TempDir()
	writeSpec := func(name string, spec ExecutionSpec) {
		data, err := json.Marshal(spec)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeSpec("allow.json", ExecutionSpec{Target: harness.Cursor, Event: PreToolUse, Matcher: "Bash", Handler: Handler{Kind: CommandHandler, Command: `printf '{"decision":"allow","message":"ok"}'`}, WorkingDir: root})
	writeSpec("deny.json", ExecutionSpec{Target: harness.Cursor, Event: PreToolUse, Matcher: "Shell", Handler: Handler{Kind: CommandHandler, Command: `printf '{"decision":"deny","message":"no"}'; exit 2`}, WorkingDir: root})
	writeSpec("other.json", ExecutionSpec{Target: harness.Cursor, Event: PostToolUse, Handler: Handler{Kind: CommandHandler, Command: "exit 2"}, WorkingDir: root})
	outcome, err := Dispatch(context.Background(), DispatchArgs{Target: harness.Cursor, Event: PreToolUse, SpecsDirectory: root, Stdin: []byte(`{"tool_name":"Shell"}`)})
	if err != nil {
		t.Fatal(err)
	}
	if outcome.ExitCode != 2 {
		t.Fatalf("outcome = %#v", outcome)
	}
	value := outcome.JSON.(map[string]any)
	if value["permission"] != Deny || value["user_message"] != "ok\nno" {
		t.Fatalf("JSON = %#v", value)
	}
}

func TestHookOutputAndGuidanceShapes(t *testing.T) {
	t.Parallel()
	result := Result{Decision: Ask, Message: "why", AdditionalContext: "ctx", UpdatedInput: 1, UpdatedToolOutput: 2}
	codex := HookOutput(harness.Codex, PreToolUse, result).(map[string]any)
	if codex["permissionDecision"] != Ask || codex["continue"] != true || codex["updatedMCPToolOutput"] != 2 {
		t.Fatalf("codex = %#v", codex)
	}
	cursor := HookOutput(harness.Cursor, PreToolUse, result).(map[string]any)
	if cursor["permission"] != Ask {
		t.Fatalf("cursor = %#v", cursor)
	}
	guidance := GuidanceHookSpecific("body", SessionStart)
	if guidance["hookSpecificOutput"].(map[string]any)["additionalContext"] != "body" {
		t.Fatalf("guidance = %#v", guidance)
	}
}

func TestExtractJSONObjectFromNoisyOutput(t *testing.T) {
	t.Parallel()
	value, err := ExtractJSONObject("log before\n{\"decision\":\"allow\"}\nlog after")
	if err != nil || value.(map[string]any)["decision"] != "allow" {
		t.Fatalf("value = %#v, %v", value, err)
	}
}

// The stripe plugin writes its hooks in exec form: "command": "node",
// "args": ["${CLAUDE_PLUGIN_ROOT}/scripts/lifecycle/postToolUse.mjs"]. The
// arguments used to be dropped, so the hook ran `node` alone.
func TestExecFormCommandHookKeepsItsArgumentsAndPluginRoot(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("runs /bin/echo")
	}
	t.Parallel()
	var value any
	if err := json.Unmarshal([]byte(`{"hooks":{"PostToolUse":[{"matcher":"Skill","hooks":[{"type":"command","command":"/bin/echo","args":["${CLAUDE_PLUGIN_ROOT}/scripts/postToolUse.mjs","two words"],"timeout":5}]}]}}`), &value); err != nil {
		t.Fatal(err)
	}
	bundle, err := ParseNested("hooks.json", value, Origin{})
	if err != nil || len(bundle.Hooks) != 1 {
		t.Fatalf("bundle = %#v, %v", bundle, err)
	}
	hook := bundle.Hooks[0]
	if len(hook.Handler.Args) != 2 || hook.RawExtra["args"] != nil {
		t.Fatalf("handler = %#v extra = %#v", hook.Handler, hook.RawExtra)
	}
	encoded, err := json.Marshal(ExecutionSpec{Target: harness.Codex, Event: hook.Event, Handler: hook.Handler, WorkingDir: "/staged/package"})
	if err != nil || !strings.Contains(string(encoded), "postToolUse.mjs") {
		t.Fatalf("spec = %s, %v", encoded, err)
	}
	var spec ExecutionSpec
	if err := json.Unmarshal(encoded, &spec); err != nil {
		t.Fatal(err)
	}
	output, err := RunCommand(context.Background(), spec, nil)
	if err != nil || string(output.Stdout) != "/staged/package/scripts/postToolUse.mjs two words\n" {
		t.Fatalf("output = %q, %v", output.Stdout, err)
	}
}

// Antigravity sends {"toolCall": {"name": "run_command"}} and reads a
// decision from the JSON it gets back, not from the exit status.
func TestDispatchForAntigravityMatchesItsToolNamesAndAnswersInItsContract(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("runs sh")
	}
	t.Parallel()
	specs := t.TempDir()
	write := func(name string, spec ExecutionSpec) {
		data, err := json.Marshal(spec)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(specs, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("a.json", ExecutionSpec{Target: harness.Agy, Event: PreToolUse, Matcher: "Bash", Handler: Handler{Kind: CommandHandler, Command: `echo '{"decision":"block","message":"no shell"}'`}})
	write("b.json", ExecutionSpec{Target: harness.Agy, Event: UserPromptSubmit, Handler: Handler{Kind: CommandHandler, Command: `echo '{"additionalContext":"remember"}'`}})
	write("c.json", ExecutionSpec{Target: harness.Agy, Event: Stop, Handler: Handler{Kind: CommandHandler, Command: `echo '{"decision":"block","reason":"x","message":"not done"}'`}})
	run := func(event Event, stdin string) (map[string]any, int) {
		outcome, err := Dispatch(context.Background(), DispatchArgs{Target: harness.Agy, Event: event, SpecsDirectory: specs, Stdin: []byte(stdin)})
		if err != nil {
			t.Fatal(err)
		}
		object, _ := outcome.JSON.(map[string]any)
		return object, outcome.ExitCode
	}
	denied, code := run(PreToolUse, `{"toolCall":{"name":"run_command","args":{"CommandLine":"rm -rf /"}},"stepIdx":1}`)
	if denied["decision"] != Deny || denied["reason"] != "no shell" || code != 0 {
		t.Fatalf("PreToolUse = %#v, exit %d", denied, code)
	}
	// a hook that does not fire still answers: Antigravity denies a call whose hook gives no decision
	if other, _ := run(PreToolUse, `{"toolCall":{"name":"view_file"}}`); len(other) != 1 || other["decision"] != "allow" {
		t.Fatalf("a Bash matcher fired for view_file, or the answer has no decision: %#v", other)
	}
	injected, _ := run(UserPromptSubmit, `{"invocationNum":1}`)
	steps, _ := injected["injectSteps"].([]any)
	if len(steps) != 1 || steps[0].(map[string]any)["ephemeralMessage"] != "remember" {
		t.Fatalf("PreInvocation = %#v", injected)
	}
	if stop, code := run(Stop, `{"terminationReason":"NO_TOOL_CALL"}`); stop["decision"] != "continue" || stop["reason"] != "not done" || code != 0 {
		t.Fatalf("Stop = %#v", stop)
	}
}

// Antigravity requires a decision. "allow" hands the call on to the next hook
// or the default policy; "auto_approve" would skip the user's approval and is
// never sent for a hook that merely did not object.
func TestAntigravityPreToolUseAllowsWithoutAutoApproving(t *testing.T) {
	t.Parallel()
	if output := HookOutput(harness.Agy, PreToolUse, Result{Decision: Allow, Message: "fine"}).(map[string]any); output["decision"] != "allow" || len(output) != 1 {
		t.Fatalf("no objection = %#v", output)
	}
	if output := HookOutput(harness.Agy, PreToolUse, Result{}).(map[string]any); output["decision"] != "allow" {
		t.Fatalf("empty result = %#v", output)
	}
	if output := HookOutput(harness.Agy, PreToolUse, Result{Decision: Ask, Message: "sure?"}).(map[string]any); output["decision"] != Ask || output["reason"] != "sure?" {
		t.Fatalf("ask = %#v", output)
	}
}
