package claude

import (
	"strings"
	"testing"

	"github.com/OlegHQ/agentpack/internal/hooks"
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
