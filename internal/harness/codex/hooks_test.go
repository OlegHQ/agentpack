package codex

import (
	"testing"

	"github.com/OlegHQ/agentpack/internal/hooks"
)

func TestRendererMapsPermissionAndOmitsUnsupportedLifecycle(t *testing.T) {
	t.Parallel()
	origin := hooks.Origin{Module: "module", PackageKey: "pkg", SourceFile: "hooks.json"}
	bundle := hooks.Bundle{Hooks: []hooks.Hook{
		{Event: hooks.PermissionRequest, Handler: hooks.Handler{Kind: hooks.CommandHandler, Command: "check"}, Origin: origin},
		{Event: hooks.SessionEnd, Handler: hooks.Handler{Kind: hooks.CommandHandler, Command: "done"}, Origin: hooks.Origin{Module: "module", PackageKey: "pkg", SourceFile: "hooks.json", EventIndex: 1}},
		{Event: hooks.SubagentStart, Handler: hooks.Handler{Kind: hooks.CommandHandler, Command: "start"}, Origin: hooks.Origin{Module: "module", PackageKey: "pkg", SourceFile: "hooks.json", EventIndex: 2}},
		{Event: hooks.SubagentStop, Handler: hooks.Handler{Kind: hooks.CommandHandler, Command: "stop"}, Origin: hooks.Origin{Module: "module", PackageKey: "pkg", SourceFile: "hooks.json", EventIndex: 3}},
		{Event: "PostCompact", Handler: hooks.Handler{Kind: hooks.CommandHandler, Command: "compacted"}, Origin: hooks.Origin{Module: "module", PackageKey: "pkg", SourceFile: "hooks.json", EventIndex: 4}},
		{Event: hooks.PreCompact, Handler: hooks.Handler{Kind: hooks.CommandHandler, Command: "compacting"}, Origin: hooks.Origin{Module: "module", PackageKey: "pkg", SourceFile: "hooks.json", EventIndex: 5}},
		{Event: hooks.PostToolUseFailure, Handler: hooks.Handler{Kind: hooks.CommandHandler, Command: "failed"}, Origin: hooks.Origin{Module: "module", PackageKey: "pkg", SourceFile: "hooks.json", EventIndex: 6}},
	}}
	output, err := (HookRenderer{}).Render(bundle, hooks.RenderContext{TargetRoot: t.TempDir(), StagedPackages: map[string]string{"pkg": t.TempDir()}})
	if err != nil {
		t.Fatal(err)
	}
	// Codex has every one of these events but PostToolUseFailure.
	if output.Summary.Native != 6 || output.Summary.Degraded != 0 || output.Summary.Omitted != 1 {
		t.Fatalf("summary = %#v", output.Summary)
	}
	hooksFile := output.Files[len(output.Files)-1].JSON.(map[string]any)["hooks"].(map[string]any)
	for _, event := range []string{"PermissionRequest", "SessionEnd", "SubagentStart", "SubagentStop", "PostCompact", "PreCompact"} {
		if _, found := hooksFile[event]; !found {
			t.Fatalf("no %s in %#v", event, hooksFile)
		}
	}
	if _, found := hooksFile["PostToolUseFailure"]; found || len(hooksFile) != 6 {
		t.Fatalf("events = %#v", hooksFile)
	}
}
