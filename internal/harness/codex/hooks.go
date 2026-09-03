package codex

import (
	"path/filepath"

	"github.com/OlegHQ/agentpack/internal/harness"
	"github.com/OlegHQ/agentpack/internal/hooks"
)

type HookRenderer struct{}

func (HookRenderer) Target() harness.Target { return harness.Codex }

func (HookRenderer) Render(bundle hooks.Bundle, context hooks.RenderContext) (hooks.RenderOutput, error) {
	var output hooks.RenderOutput
	events := make(map[string]any)
	for _, hook := range bundle.Hooks {
		mapped := hook.Event
		if mapped == hooks.PermissionRequest {
			mapped = hooks.PreToolUse
		}
		keep, err := hooks.CheckSupport(harness.Codex, hook, Support(hook.Event, hook.Handler), &output, "rendered into Codex native hooks.json", "wrapped into agentpack hook-exec for Codex")
		if err != nil {
			return hooks.RenderOutput{}, err
		}
		if !keep {
			continue
		}
		handler, err := renderHandler(hook, mapped, context, &output)
		if err != nil {
			return hooks.RenderOutput{}, err
		}
		group := map[string]any{"hooks": []any{handler}}
		if hook.Matcher != "" {
			group["matcher"] = hook.Matcher
		}
		groups, _ := events[string(mapped)].([]any)
		events[string(mapped)] = append(groups, group)
	}
	if len(events) != 0 {
		output.Files = append(output.Files, hooks.RenderedFile{Path: filepath.Join(context.TargetRoot, "hooks.json"), JSON: map[string]any{"hooks": events}})
	}
	return output, nil
}

func Support(event hooks.Event, handler hooks.Handler) hooks.Support {
	switch event {
	case hooks.PreToolUse, hooks.PostToolUse, hooks.UserPromptSubmit, hooks.SessionStart, hooks.Stop:
	case hooks.PermissionRequest:
		return hooks.Support{Kind: hooks.Degraded, Reason: "Codex permission checks are approximated with pre-tool-use hooks"}
	default:
		return hooks.Support{Kind: hooks.Unsupported, Reason: "Codex does not expose this Claude lifecycle event natively"}
	}
	if handler.Kind == hooks.CommandHandler || handler.Kind == hooks.PromptHandler {
		return hooks.Support{Kind: hooks.Native}
	}
	return hooks.Support{Kind: hooks.Emulated}
}

func renderHandler(hook hooks.Hook, event hooks.Event, context hooks.RenderContext, output *hooks.RenderOutput) (map[string]any, error) {
	if hook.Origin.Layer == hooks.SeededNative || hook.Handler.Kind == hooks.PromptHandler {
		return hooks.HandlerObject(hook, false), nil
	}
	specPath, err := hooks.BuildExecutionSpec(harness.Codex, hook, event, context, output)
	if err != nil {
		return nil, err
	}
	return map[string]any{"type": "command", "command": hooks.HookExecCommand(hook.Handler.Kind, harness.Codex, specPath)}, nil
}
