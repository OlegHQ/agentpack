package claude

import (
	"path/filepath"

	"github.com/OlegHQ/agentpack/internal/harness"
	"github.com/OlegHQ/agentpack/internal/hooks"
)

type HookRenderer struct{}

func (HookRenderer) Target() harness.Target { return harness.Claude }

func (HookRenderer) Render(bundle hooks.Bundle, context hooks.RenderContext) (hooks.RenderOutput, error) {
	var output hooks.RenderOutput
	if len(bundle.Hooks) == 0 {
		return output, nil
	}
	events := make(map[string]any)
	for _, hook := range bundle.Hooks {
		handler, err := renderHandler(hook, context, &output)
		if err != nil {
			return hooks.RenderOutput{}, err
		}
		group := make(map[string]any)
		if hook.Matcher != "" {
			group["matcher"] = hook.Matcher
		}
		for key, value := range hook.MatcherGroupExtra {
			group[key] = value
		}
		group["hooks"] = []any{handler}
		groups, _ := events[string(hook.Event)].([]any)
		events[string(hook.Event)] = append(groups, group)
	}
	output.Files = append(output.Files, hooks.RenderedFile{Path: filepath.Join(context.TargetRoot, "hooks", "hooks.json"), JSON: map[string]any{"hooks": events}})
	return output, nil
}

func renderHandler(hook hooks.Hook, context hooks.RenderContext, output *hooks.RenderOutput) (map[string]any, error) {
	if hook.Handler.Kind != hooks.CommandHandler {
		hooks.PushDiagnostic(output, "native", hook, "rendered Claude "+string(hook.Handler.Kind)+" hook natively")
		return hooks.HandlerObject(hook, true), nil
	}
	specPath, err := hooks.BuildExecutionSpec(harness.Claude, hook, hook.Event, context, output)
	if err != nil {
		return nil, err
	}
	hooks.PushDiagnostic(output, "emulated", hook, "wrapped command hook to preserve package-relative working directory")
	object := map[string]any{"type": "command", "command": hooks.HookExecCommand(hooks.CommandHandler, harness.Claude, specPath)}
	if hook.Handler.Timeout != nil {
		object["timeout"] = *hook.Handler.Timeout
	}
	return object, nil
}
