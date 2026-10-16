package claude

import (
	"strings"
	"testing"

	"github.com/olegpustovit/agentpack/internal/hooks"
)

func TestRendererWrapsCommandsAndPreservesNativeHTTP(t *testing.T) {
	t.Parallel()
	base := hooks.Origin{Module: "module", PackageKey: "pkg", SourceFile: "hooks.json"}
	timeout := uint64(5)
	bundle := hooks.Bundle{Hooks: []hooks.Hook{
		{Event: hooks.PreToolUse, Matcher: "Bash", Handler: hooks.Handler{Kind: hooks.CommandHandler, Command: "check", Timeout: &timeout}, Origin: base},
		{Event: hooks.PostToolUse, Handler: hooks.Handler{Kind: hooks.HTTPHandler, URL: "https://example.test"}, Origin: base, RawExtra: map[string]any{"custom": true}},
	}}
	output, err := (HookRenderer{}).Render(bundle, hooks.RenderContext{TargetRoot: t.TempDir(), StagedPackages: map[string]string{"pkg": t.TempDir()}})
	if err != nil {
		t.Fatal(err)
	}
	if len(output.Files) != 2 || output.Summary.Emulated != 1 || output.Summary.Native != 1 {
		t.Fatalf("output = %#v", output)
	}
	root := output.Files[len(output.Files)-1].JSON.(map[string]any)
	if root["hooks"] == nil || !strings.HasSuffix(output.Files[len(output.Files)-1].Path, "hooks.json") {
		t.Fatalf("root = %#v", root)
	}
}

func TestRendererKeepsNewClaudeEventsAndSkipsCursorOnlyOnes(t *testing.T) {
	t.Parallel()
	origin := func(index int) hooks.Origin {
		return hooks.Origin{Module: "module", PackageKey: "pkg", SourceFile: "hooks.json", EventIndex: index}
	}
	bundle := hooks.Bundle{Hooks: []hooks.Hook{
		{Event: hooks.PostToolUseFailure, Dialect: hooks.ClaudeDialect, Handler: hooks.Handler{Kind: hooks.CommandHandler, Command: "failure"}, RawExtra: map[string]any{"asyncRewake": true}, Origin: origin(0)},
		{Event: "StopFailure", Dialect: hooks.ClaudeDialect, Handler: hooks.Handler{Kind: hooks.CommandHandler, Command: "stop-failure"}, Origin: origin(1)},
		{Event: "afterAgentResponse", Dialect: hooks.CursorDialect, Handler: hooks.Handler{Kind: hooks.CommandHandler, Command: "cursor"}, RawExtra: map[string]any{"loop_limit": 3}, Origin: origin(2)},
	}}
	output, err := (HookRenderer{}).Render(bundle, hooks.RenderContext{TargetRoot: "/staged", StagedPackages: map[string]string{"pkg": "/staged/pkg"}})
	if err != nil {
		t.Fatal(err)
	}
	events := output.Files[len(output.Files)-1].JSON.(map[string]any)["hooks"].(map[string]any)
	if len(events) != 2 || events["PostToolUseFailure"] == nil || events["StopFailure"] == nil {
		t.Fatalf("events = %#v", events)
	}
	handler := events["PostToolUseFailure"].([]any)[0].(map[string]any)["hooks"].([]any)[0].(map[string]any)
	if handler["asyncRewake"] != true || !strings.Contains(handler["command"].(string), "hook-exec command --target claude") {
		t.Fatalf("handler = %#v", handler)
	}
	last := output.Diagnostics[len(output.Diagnostics)-1]
	if output.Summary.Omitted != 1 || last.Message != "skipping afterAgentResponse hook: Claude Code has no such hook event" {
		t.Fatalf("diagnostics = %#v", output.Diagnostics)
	}
}

// The bundle's hooks directory starts as a raw copy of the package's, so a
// package whose hooks are all Cursor-only must still end with a Claude Code
// hooks file, an empty one.
func TestRendererReplacesACursorOnlyHooksFileWithAnEmptyOne(t *testing.T) {
	t.Parallel()
	bundle := hooks.Bundle{Hooks: []hooks.Hook{
		{Event: "afterFileEdit", Dialect: hooks.CursorDialect, Handler: hooks.Handler{Kind: hooks.CommandHandler, Command: "mark"}, Origin: hooks.Origin{Module: "module", PackageKey: "pkg", SourceFile: "hooks.json"}},
	}}
	output, err := (HookRenderer{}).Render(bundle, hooks.RenderContext{TargetRoot: "/staged", StagedPackages: map[string]string{"pkg": "/staged/pkg"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(output.Files) != 1 || !strings.HasSuffix(output.Files[0].Path, "hooks.json") || len(output.Files[0].JSON.(map[string]any)["hooks"].(map[string]any)) != 0 || output.Summary.Omitted != 1 {
		t.Fatalf("output = %#v", output)
	}
}
