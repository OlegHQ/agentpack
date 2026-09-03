package hooks

import (
	"encoding/json"
	"strings"
	"testing"
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

func TestParseHooksRejectsUnknownEventsAndMalformedHandlers(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{
		`{"hooks":{"Unknown":[]}}`,
		`{"hooks":{"Stop":[{"hooks":[{"type":"command"}]}]}}`,
		`{"hooks":{"Stop":[{"hooks":[{"type":"mystery"}]}]}}`,
	} {
		if _, err := ParseNested("bad.json", decodeHookJSON(t, raw), Origin{}); err == nil || !strings.Contains(err.Error(), "bad.json") {
			t.Fatalf("error = %v", err)
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
