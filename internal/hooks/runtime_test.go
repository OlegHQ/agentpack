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

	"github.com/OlegHQ/agentpack/internal/harness"
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
