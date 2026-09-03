package opencode

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/OlegHQ/agentpack/internal/hooks"
)

func TestRendererGeneratesBridgeAndMergesConfig(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "opencode.json"), []byte(`{"plugin":["existing.js"],"theme":"dark"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	origin := hooks.Origin{Module: "module", PackageKey: "pkg", SourceFile: "hooks.json"}
	bundle := hooks.Bundle{Hooks: []hooks.Hook{
		{Event: hooks.PreToolUse, Matcher: "Bash", Handler: hooks.Handler{Kind: hooks.CommandHandler, Command: "check"}, Origin: origin},
		{Event: hooks.UserPromptSubmit, Handler: hooks.Handler{Kind: hooks.PromptHandler, Prompt: "guide"}, Origin: origin},
		{Event: hooks.Stop, Handler: hooks.Handler{Kind: hooks.CommandHandler, Command: "stop"}, Origin: origin},
	}}
	output, err := (HookRenderer{}).Render(bundle, hooks.RenderContext{TargetRoot: root, StagedPackages: map[string]string{"pkg": t.TempDir()}})
	if err != nil {
		t.Fatal(err)
	}
	if len(output.Files) != 5 || output.Summary.Native != 1 || output.Summary.Degraded != 1 || output.Summary.Omitted != 1 {
		t.Fatalf("output = %#v", output)
	}
	config := output.Files[len(output.Files)-1].JSON.(map[string]any)
	plugins := config["plugin"].([]any)
	if len(plugins) != 2 || config["theme"] != "dark" || !strings.Contains(pluginSource, "tool.execute.before") {
		t.Fatalf("config = %#v", config)
	}
}

func TestUnsupportedStrictEventFails(t *testing.T) {
	t.Parallel()
	hook := hooks.Hook{Event: hooks.PreToolUse, Handler: hooks.Handler{Kind: hooks.CommandHandler}, Origin: hooks.Origin{SourceFile: "hook"}}
	// Exercise the common strict failure directly with the OpenCode target semantics.
	if _, err := hooks.CheckSupport((HookRenderer{}).Target(), hook, hooks.Support{Kind: hooks.Unsupported, Reason: "missing"}, &hooks.RenderOutput{}, "", ""); err == nil {
		t.Fatal("expected strict error")
	}
}
