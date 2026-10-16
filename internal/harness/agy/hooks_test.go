package agy

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/olegpustovit/agentpack/internal/hooks"
)

// Antigravity reads <plugin>/hooks.json as {"<hook name>": {"<Event>": [...]}},
// with matcher groups for the tool events and flat handlers for the others.
func TestRendererWritesAntigravityHooksAtThePluginRoot(t *testing.T) {
	t.Parallel()
	origin := func(index int) hooks.Origin {
		return hooks.Origin{Module: "module", PackageKey: "pkg", SourceFile: "hooks.json", EventIndex: index}
	}
	timeout := uint64(10)
	command := func(text string) hooks.Handler {
		return hooks.Handler{Kind: hooks.CommandHandler, Command: text, Timeout: &timeout}
	}
	bundle := hooks.Bundle{Hooks: []hooks.Hook{
		{Event: hooks.PreToolUse, Matcher: "Bash", Handler: command("check"), Origin: origin(0)},
		{Event: hooks.PreToolUse, Matcher: "Write|Edit", Handler: command("check-write"), Origin: hooks.Origin{Module: "module", PackageKey: "pkg", SourceFile: "hooks.json", HookIndex: 1}},
		{Event: hooks.PostToolUse, Handler: command("after"), Origin: origin(1)},
		{Event: hooks.SessionStart, Matcher: "startup", Handler: command("start"), Origin: origin(2)},
		{Event: hooks.UserPromptSubmit, Handler: command("prompt"), Origin: origin(3)},
		{Event: hooks.Stop, Handler: command("stop"), Origin: origin(4)},
		{Event: hooks.PostToolUseFailure, Handler: command("never"), Origin: origin(5)},
		{Event: hooks.SessionEnd, Handler: command("never"), Origin: origin(6)},
	}}
	output, err := (HookRenderer{}).Render(bundle, hooks.RenderContext{TargetRoot: "/staged/bundle", StagedPackages: map[string]string{"pkg": "/staged/bundle/hooks/_packages/pkg/package"}})
	if err != nil {
		t.Fatal(err)
	}
	file := output.Files[len(output.Files)-1]
	if file.Path != filepath.Join("/staged/bundle", "hooks.json") {
		t.Fatalf("hooks file at %s", file.Path)
	}
	named := file.JSON.(map[string]any)["agentpack"].(map[string]any)
	if len(named) != 5 {
		t.Fatalf("events = %#v", named)
	}
	for _, event := range []string{"PreToolUse", "PostToolUse"} {
		groups := named[event].([]any)
		group := groups[0].(map[string]any)
		handler := group["hooks"].([]any)[0].(map[string]any)
		if len(groups) != 1 || group["matcher"] != "*" || handler["type"] != "command" || handler["timeout"] != uint64(10) || !strings.Contains(handler["command"].(string), "hook-exec dispatch --target agy --event "+event) {
			t.Fatalf("%s = %#v", event, groups)
		}
	}
	for event, source := range map[string]string{"SessionStart": "SessionStart", "PreInvocation": "UserPromptSubmit", "Stop": "Stop"} {
		handlers := named[event].([]any)
		handler := handlers[0].(map[string]any)
		if len(handlers) != 1 || handler["hooks"] != nil || !strings.Contains(handler["command"].(string), "--event "+source+" ") {
			t.Fatalf("%s = %#v", event, handlers)
		}
	}
	if output.Summary.Omitted != 2 || output.Summary.Degraded != 1 || output.Summary.Emulated != 5 {
		t.Fatalf("summary = %#v", output.Summary)
	}
	var skipped []string
	for _, diagnostic := range output.Diagnostics {
		if diagnostic.Level == "omitted" {
			skipped = append(skipped, diagnostic.Message)
		}
	}
	if strings.Join(skipped, "|") != "skipping PostToolUseFailure hook: Antigravity has no equivalent hook event|skipping SessionEnd hook: Antigravity has no equivalent hook event" {
		t.Fatalf("skipped = %v", skipped)
	}
}
