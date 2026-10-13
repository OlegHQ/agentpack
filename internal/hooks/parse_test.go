package hooks

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/OlegHQ/agentpack/internal/harness"
)

func TestParseNestedNormalizesHandlersExtrasAndIndices(t *testing.T) {
	t.Parallel()
	value := decodeHookJSON(t, `{
	  "hooks": {
	    "PostToolUse": [{"matcher":"Write","group-extra":true,"hooks":[
	      {"type":"http","url":"https://example.test","method":"PUT","headers":{"X-Test":"yes","X-Drop":3},"body":{"ok":true},"custom":1},
	      {"type":"agent","instruction":"review","agent":"critic","model":"fast"}
	    ]}],
	    "PreToolUse": [{"hooks":[{"type":"command","command":"check","timeout":12}]}]
	  }
	}`)
	base := Origin{Layer: PackPlugin, Module: "github.com/o/r", PackageKey: "key"}
	bundle, err := ParseNested("hooks.json", value, base)
	if err != nil {
		t.Fatal(err)
	}
	if len(bundle.Hooks) != 3 {
		t.Fatalf("hooks = %#v", bundle.Hooks)
	}
	httpHook := bundle.Hooks[0]
	if httpHook.Event != PostToolUse || httpHook.Handler.Kind != HTTPHandler || httpHook.Handler.Headers["X-Test"] != "yes" || httpHook.Handler.Headers["X-Drop"] != "" || httpHook.RawExtra["custom"] == nil || httpHook.MatcherGroupExtra["group-extra"] == nil {
		t.Fatalf("http hook = %#v", httpHook)
	}
	command := bundle.Hooks[2]
	if command.Event != PreToolUse || command.Handler.Timeout == nil || *command.Handler.Timeout != 12 || !command.Strict() || command.Origin.EventIndex != 1 {
		t.Fatalf("command hook = %#v", command)
	}
}

func TestParseCodexAcceptsDirectAndNestedEntries(t *testing.T) {
	t.Parallel()
	value := decodeHookJSON(t, `{"PreToolUse":[{"type":"prompt","prompt":"decide","model":"m"},{"matcher":"Bash","hooks":[{"type":"command","command":"check"}]}]}`)
	bundle, err := ParseCodex("hooks.json", value, Origin{Layer: SeededNative})
	if err != nil || len(bundle.Hooks) != 2 {
		t.Fatalf("bundle = %#v, %v", bundle, err)
	}
	if bundle.Hooks[0].Handler.Kind != PromptHandler || bundle.Hooks[1].Matcher != "Bash" {
		t.Fatalf("hooks = %#v", bundle.Hooks)
	}
}

func TestParseHooksRejectsMalformedHandlers(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{
		`{"hooks":{"Stop":[{"hooks":[{"type":"command"}]}]}}`,
		`{"hooks":{"Stop":{"hooks":[]}}}`,
		`{"Stop":[]}`,
	} {
		if _, err := ParseNested("bad.json", decodeHookJSON(t, raw), Origin{}); err == nil || !strings.Contains(err.Error(), "bad.json") {
			t.Fatalf("error = %v", err)
		}
	}
}

// hooks/hooks.json of the official code-modernization plugin uses
// PostToolUseFailure and StopFailure, which were rejected as unknown.
func TestParseNestedKeepsEveryClaudeEventAndSkipsOnlyWhatItCannotRead(t *testing.T) {
	t.Parallel()
	value := decodeHookJSON(t, `{"description":"telemetry","hooks":{
	  "PostToolUseFailure":[{"hooks":[{"type":"command","command":"failure","timeout":10,"asyncRewake":true}]}],
	  "StopFailure":[{"hooks":[{"type":"command","command":"stop-failure"}]}],
	  "SubagentStart":[{"hooks":[{"type":"command","command":"start"},{"type":"mcp_tool","server":"s","tool":"t"}]}],
	  "NotAnEvent":[{"hooks":[{"type":"command","command":"never"}]}],
	  "afterFileEdit":[{"hooks":[{"type":"command","command":"cursor-only"}]}]
	}}`)
	bundle, err := ParseNested("hooks.json", value, Origin{Module: "github.com/o/r"})
	if err != nil {
		t.Fatal(err)
	}
	var events []string
	for _, hook := range bundle.Hooks {
		events = append(events, string(hook.Event)+"="+hook.Handler.Command)
	}
	if got, want := strings.Join(events, " "), "PostToolUseFailure=failure StopFailure=stop-failure SubagentStart=start"; got != want {
		t.Fatalf("hooks = %s, want %s", got, want)
	}
	var skipped []string
	for _, diagnostic := range bundle.Skipped {
		skipped = append(skipped, diagnostic.Message)
	}
	joined := strings.Join(skipped, "\n")
	for _, part := range []string{`"NotAnEvent"`, `"afterFileEdit"`, `"mcp_tool"`} {
		if !strings.Contains(joined, part) {
			t.Fatalf("skipped does not name %s:\n%s", part, joined)
		}
	}
	if len(bundle.Skipped) != 3 || !strings.Contains(bundle.Skipped[0].Source, "github.com/o/r (hooks.json)") {
		t.Fatalf("skipped = %#v", bundle.Skipped)
	}
}

// hooks/hooks.json of the Cursor advisor plugin.
func TestParseCursorReadsFlatEntriesAndCursorEvents(t *testing.T) {
	t.Parallel()
	value := decodeHookJSON(t, `{"version":1,"hooks":{
	  "afterFileEdit":[{"command":"bash \"${CURSOR_PLUGIN_ROOT}/hooks/mark-pending.sh\""}],
	  "afterAgentResponse":[{"command":"capture"}],
	  "subagentStop":[{"command":"record","matcher":"^advisor-subagent$"}],
	  "stop":[{"command":"stop-hook","loop_limit":3,"timeout":30}],
	  "beforeSubmitPrompt":[{"type":"prompt","prompt":"Is this safe?"}],
	  "onSomethingNew":[{"command":"never"}]
	}}`)
	if !LooksLikeCursor(value) || LooksLikeCursor(decodeHookJSON(t, `{"hooks":{"Stop":[]}}`)) {
		t.Fatal("format detection is wrong")
	}
	bundle, err := ParseCursor("hooks.json", value, Origin{Module: "github.com/cursor/plugins/advisor"})
	if err != nil {
		t.Fatal(err)
	}
	got := make(map[Event]Hook)
	for _, hook := range bundle.Hooks {
		if hook.Dialect != CursorDialect {
			t.Fatalf("dialect = %q", hook.Dialect)
		}
		got[hook.Event] = hook
	}
	if len(got) != 5 || len(bundle.Skipped) != 1 || !strings.Contains(bundle.Skipped[0].Message, `"onSomethingNew"`) {
		t.Fatalf("hooks = %#v\nskipped = %#v", got, bundle.Skipped)
	}
	if got[SubagentStop].Matcher != "^advisor-subagent$" || got[Stop].RawExtra["loop_limit"] != float64(3) || *got[Stop].Handler.Timeout != 30 {
		t.Fatalf("options lost: %#v", got)
	}
	if got[UserPromptSubmit].Handler.Kind != PromptHandler || got["afterAgentResponse"].Handler.Command != "capture" {
		t.Fatalf("handlers = %#v", got)
	}
	if got["afterFileEdit"].Event.Claude() || !got[Stop].Event.Claude() {
		t.Fatal("event classification is wrong")
	}
	if name, ok := UserPromptSubmit.CursorName(); !ok || name != "beforeSubmitPrompt" {
		t.Fatalf("CursorName = %q, %v", name, ok)
	}
}

func TestBundleForTargetPrefersTheHooksPublishedForThatAgent(t *testing.T) {
	t.Parallel()
	origin := Origin{Layer: PackPlugin, Module: "github.com/o/three", PackageKey: "three"}
	other := Origin{Layer: PackPlugin, Module: "github.com/o/cursor-only", PackageKey: "cursor-only"}
	bundle := Bundle{Hooks: []Hook{
		{Event: Stop, Dialect: ClaudeDialect, Origin: origin},
		{Event: Stop, Dialect: CursorDialect, Origin: origin},
		{Event: Stop, Dialect: CodexDialect, Origin: origin},
		{Event: Stop, Dialect: CursorDialect, Origin: other},
		{Event: Stop, Origin: Origin{Layer: DotAgents, Module: "dot-agents"}},
	}}
	for target, want := range map[harness.Target]string{
		harness.Claude: "claude cursor ", harness.Cursor: "cursor cursor ", harness.Codex: "codex cursor ", harness.OpenCode: "claude cursor ",
	} {
		var got string
		for _, hook := range bundle.ForTarget(target).Hooks {
			got += string(hook.Dialect) + " "
		}
		if strings.TrimSpace(got) != strings.TrimSpace(want) {
			t.Fatalf("%s gets %q, want %q", target, got, want)
		}
	}
}

func TestHandlerMarshalMatchesExternallyTaggedSpecWireFormat(t *testing.T) {
	t.Parallel()
	timeout := uint64(9)
	data, err := json.Marshal(Handler{Kind: CommandHandler, Command: "echo ok", Timeout: &timeout})
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != `{"Command":{"command":"echo ok","timeout_secs":9}}` {
		t.Fatalf("JSON = %s", data)
	}
	var decoded Handler
	if err := json.Unmarshal(data, &decoded); err != nil || decoded.Kind != CommandHandler || decoded.Timeout == nil || *decoded.Timeout != 9 {
		t.Fatalf("decoded = %#v, %v", decoded, err)
	}
}

func TestEventAliasesAndLayerRanks(t *testing.T) {
	t.Parallel()
	for _, value := range []string{"PreToolUse", "pre-tool-use"} {
		if event, ok := ParseEvent(value); !ok || event != PreToolUse {
			t.Fatalf("ParseEvent(%q) = %s, %v", value, event, ok)
		}
	}
	if !(SeededNative.Rank() < PackPlugin.Rank() && PackPlugin.Rank() < BareSkill.Rank() && BareSkill.Rank() < DotAgents.Rank()) {
		t.Fatal("layer ranks are not ordered")
	}
}

func decodeHookJSON(t *testing.T, raw string) any {
	t.Helper()
	var value any
	if err := json.Unmarshal([]byte(raw), &value); err != nil {
		t.Fatal(err)
	}
	return value
}
