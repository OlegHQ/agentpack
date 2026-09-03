package cursor

import (
	"testing"

	"github.com/OlegHQ/agentpack/internal/hooks"
)

func TestRendererWritesEverySpecButDeduplicatesBlanketDispatcher(t *testing.T) {
	t.Parallel()
	origin := hooks.Origin{Module: "module", PackageKey: "pkg", SourceFile: "hooks.json"}
	bundle := hooks.Bundle{Hooks: []hooks.Hook{
		{Event: hooks.PreToolUse, Matcher: "Bash", Handler: hooks.Handler{Kind: hooks.CommandHandler, Command: "a"}, Origin: origin},
		{Event: hooks.PreToolUse, Matcher: "Write", Handler: hooks.Handler{Kind: hooks.CommandHandler, Command: "b"}, Origin: hooks.Origin{Module: "module", PackageKey: "pkg", SourceFile: "hooks.json", HookIndex: 1}},
		{Event: hooks.Notification, Handler: hooks.Handler{Kind: hooks.CommandHandler, Command: "c"}, Origin: origin},
	}}
	output, err := (HookRenderer{}).Render(bundle, hooks.RenderContext{TargetRoot: t.TempDir(), StagedPackages: map[string]string{"pkg": t.TempDir()}})
	if err != nil {
		t.Fatal(err)
	}
	if len(output.Files) != 3 || output.Summary.Native != 2 || output.Summary.Omitted != 1 {
		t.Fatalf("output = %#v", output)
	}
	root := output.Files[len(output.Files)-1].JSON.(map[string]any)
	events := root["hooks"].(map[string]any)
	if len(events["preToolUse"].([]any)) != 1 {
		t.Fatalf("events = %#v", events)
	}
}
