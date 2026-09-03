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
		{Event: hooks.SessionEnd, Handler: hooks.Handler{Kind: hooks.CommandHandler, Command: "done"}, Origin: origin},
	}}
	output, err := (HookRenderer{}).Render(bundle, hooks.RenderContext{TargetRoot: t.TempDir(), StagedPackages: map[string]string{"pkg": t.TempDir()}})
	if err != nil {
		t.Fatal(err)
	}
	if output.Summary.Degraded != 1 || output.Summary.Omitted != 1 || len(output.Files) != 2 {
		t.Fatalf("output = %#v", output)
	}
	hooksFile := output.Files[len(output.Files)-1].JSON.(map[string]any)["hooks"].(map[string]any)
	if _, found := hooksFile[string(hooks.PreToolUse)]; !found {
		t.Fatalf("events = %#v", hooksFile)
	}
}
