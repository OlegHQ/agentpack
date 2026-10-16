package agy

import (
	"path/filepath"

	"github.com/olegpustovit/agentpack/internal/harness"
	"github.com/olegpustovit/agentpack/internal/hooks"
)

// HookRenderer writes Antigravity's own hooks file: hooks.json at the plugin
// root, one named hook whose keys are Antigravity's events. Antigravity loads
// a plugin's hooks only from <plugin>/hooks.json, not from hooks/hooks.json,
// and reads the file in its own shape, not Claude Code's.
type HookRenderer struct{}

func (HookRenderer) Target() harness.Target { return harness.Agy }

// hookName is the named hook the pack's handlers are registered under.
const hookName = "agentpack"

func (HookRenderer) Render(bundle hooks.Bundle, context hooks.RenderContext) (hooks.RenderOutput, error) {
	var output hooks.RenderOutput
	events := make(map[string]any)
	timeouts := make(map[string]uint64)
	seen := make(map[string]bool)
	for _, hook := range bundle.Hooks {
		event, grouped, mapped := agyEvent(hook.Event)
		if !mapped {
			hooks.PushDiagnostic(&output, "omitted", hook, "Antigravity has no equivalent hook event")
			continue
		}
		keep, err := hooks.CheckSupport(harness.Agy, hook, Support(hook.Event), &output, "routed via agentpack hook-exec dispatch", "routed via agentpack hook-exec dispatch")
		if err != nil {
			return hooks.RenderOutput{}, err
		}
		if !keep {
			continue
		}
		if _, err := hooks.BuildExecutionSpec(harness.Agy, hook, hook.Event, context, &output); err != nil {
			return hooks.RenderOutput{}, err
		}
		if hook.Handler.Timeout != nil && *hook.Handler.Timeout > timeouts[event] {
			timeouts[event] = *hook.Handler.Timeout
		}
		// One dispatcher per source event: it runs every hook of that event
		// whose matcher fits, and answers in Antigravity's output contract.
		key := event + "\x00" + string(hook.Event)
		if seen[key] {
			continue
		}
		seen[key] = true
		handler := map[string]any{"type": "command", "command": hooks.HookDispatchCommand(harness.Agy, hook.Event, hooks.HookAssetRoot(harness.Agy, context.TargetRoot))}
		entries, _ := events[event].([]any)
		if grouped {
			// Tool events wrap handlers in a matcher group; the dispatcher
			// applies each hook's own matcher, so the group matches every tool.
			events[event] = append(entries, map[string]any{"matcher": "*", "hooks": []any{handler}})
		} else {
			events[event] = append(entries, handler)
		}
	}
	if len(events) == 0 {
		return output, nil
	}
	for event, limit := range timeouts {
		for _, entry := range events[event].([]any) {
			object := entry.(map[string]any)
			if nested, grouped := object["hooks"].([]any); grouped {
				object = nested[0].(map[string]any)
			}
			object["timeout"] = limit
		}
	}
	output.Files = append(output.Files, hooks.RenderedFile{Path: filepath.Join(context.TargetRoot, "hooks.json"), JSON: map[string]any{hookName: events}})
	return output, nil
}

// agyEvent maps an event to Antigravity's (SessionStart, PreToolUse,
// PostToolUse, PreInvocation, PostInvocation, Stop) and says whether its
// handlers are wrapped in matcher groups.
func agyEvent(event hooks.Event) (name string, grouped, mapped bool) {
	switch event {
	case hooks.SessionStart:
		return "SessionStart", false, true
	case hooks.PreToolUse, hooks.PermissionRequest:
		return "PreToolUse", true, true
	case hooks.PostToolUse:
		return "PostToolUse", true, true
	case hooks.UserPromptSubmit:
		return "PreInvocation", false, true
	case hooks.Stop:
		return "Stop", false, true
	default:
		return "", false, false
	}
}

func Support(event hooks.Event) hooks.Support {
	switch event {
	case hooks.UserPromptSubmit:
		return hooks.Support{Kind: hooks.Degraded, Reason: "Antigravity has no prompt-submit event; the hook runs as PreInvocation, before each model call"}
	case hooks.PermissionRequest:
		return hooks.Support{Kind: hooks.Degraded, Reason: "Antigravity models permission requests as PreToolUse instead of a dedicated event"}
	}
	return hooks.Support{Kind: hooks.Emulated}
}
