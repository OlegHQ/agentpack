package cursor

import (
	"path/filepath"

	"github.com/olegpustovit/agentpack/internal/harness"
	"github.com/olegpustovit/agentpack/internal/hooks"
)

type HookRenderer struct{}

func (HookRenderer) Target() harness.Target { return harness.Cursor }

func (HookRenderer) Render(bundle hooks.Bundle, context hooks.RenderContext) (hooks.RenderOutput, error) {
	var output hooks.RenderOutput
	events := make(map[string][]any)
	seen := make(map[string]bool)
	for _, hook := range bundle.Hooks {
		step, mapped := cursorStep(hook.Event)
		if !mapped {
			hooks.PushDiagnostic(&output, "omitted", hook, "Cursor has no equivalent hook event")
			continue
		}
		if hook.Dialect == hooks.CursorDialect {
			// Written for Cursor: it keeps its own entry, matcher and options,
			// and speaks Cursor's hook protocol itself.
			entry, err := nativeEntry(hook, context, &output)
			if err != nil {
				return hooks.RenderOutput{}, err
			}
			events[step] = append(events[step], entry)
			continue
		}
		keep, err := hooks.CheckSupport(harness.Cursor, hook, Support(hook.Event, hook.Handler), &output, "routed via agentpack hook-exec dispatch", "routed via agentpack hook-exec dispatch")
		if err != nil {
			return hooks.RenderOutput{}, err
		}
		if !keep {
			continue
		}
		if _, err := hooks.BuildExecutionSpec(harness.Cursor, hook, hook.Event, context, &output); err != nil {
			return hooks.RenderOutput{}, err
		}
		key := step + "\x00" + string(hook.Event)
		if seen[key] {
			continue
		}
		seen[key] = true
		command := hooks.HookDispatchCommand(harness.Cursor, hook.Event, hooks.HookAssetRoot(harness.Cursor, context.TargetRoot))
		entry := map[string]any{"type": "command", "command": command}
		if hook.Strict() || hook.Event == hooks.PermissionRequest {
			entry["failClosed"] = true
		}
		events[step] = append(events[step], entry)
	}
	if len(events) == 0 {
		return output, nil
	}
	hookMap := make(map[string]any, len(events))
	for event, entries := range events {
		hookMap[event] = entries
	}
	output.Files = append(output.Files, hooks.RenderedFile{Path: filepath.Join(context.TargetRoot, "hooks", "hooks.json"), JSON: map[string]any{"version": 1, "hooks": hookMap}})
	return output, nil
}

// nativeEntry renders a hook published in Cursor's own format. A command runs
// through hook-exec only so that ${CURSOR_PLUGIN_ROOT} names the staged copy
// of its package; its input and output pass through unchanged.
func nativeEntry(hook hooks.Hook, context hooks.RenderContext, output *hooks.RenderOutput) (map[string]any, error) {
	entry := make(map[string]any, len(hook.RawExtra)+3)
	for key, value := range hook.RawExtra {
		entry[key] = value
	}
	if hook.Matcher != "" {
		entry["matcher"] = hook.Matcher
	}
	if hook.Handler.Kind != hooks.CommandHandler {
		hooks.PushDiagnostic(output, "native", hook, "rendered Cursor "+string(hook.Handler.Kind)+" hook natively")
		for key, value := range hooks.HandlerObject(hook, false) {
			entry[key] = value
		}
		return entry, nil
	}
	specPath, err := hooks.BuildExecutionSpec(harness.Cursor, hook, hook.Event, context, output)
	if err != nil {
		return nil, err
	}
	hooks.PushDiagnostic(output, "native", hook, "rendered Cursor hook natively")
	entry["command"] = hooks.HookExecCommand(hooks.CommandHandler, harness.Cursor, specPath)
	if hook.Handler.Timeout != nil {
		entry["timeout"] = *hook.Handler.Timeout
	}
	return entry, nil
}

func Support(event hooks.Event, handler hooks.Handler) hooks.Support {
	switch event {
	case hooks.Notification:
		return hooks.Support{Kind: hooks.Unsupported, Reason: "Cursor has no notification hook surface"}
	case hooks.PermissionRequest:
		if handler.Kind == hooks.HTTPHandler || handler.Kind == hooks.AgentHandler {
			return hooks.Support{Kind: hooks.Degraded, Reason: "Cursor permission hooks are decomposed into preToolUse bridge commands"}
		}
		return hooks.Support{Kind: hooks.Degraded, Reason: "Cursor models permission requests as preToolUse instead of a dedicated event"}
	case hooks.SessionStart, hooks.PreCompact:
		if handler.Kind == hooks.HTTPHandler || handler.Kind == hooks.AgentHandler {
			return hooks.Support{Kind: hooks.Degraded, Reason: "Cursor supports the lifecycle but requires bridge execution for this handler type"}
		}
		return hooks.Support{Kind: hooks.Degraded, Reason: "Cursor cannot preserve Claude trigger-specific matchers for this lifecycle event"}
	}
	if handler.Kind == hooks.CommandHandler || handler.Kind == hooks.PromptHandler {
		return hooks.Support{Kind: hooks.Native}
	}
	return hooks.Support{Kind: hooks.Emulated}
}

func cursorStep(event hooks.Event) (string, bool) {
	switch event {
	case hooks.PreToolUse, hooks.PermissionRequest:
		return "preToolUse", true
	case hooks.PostToolUse:
		return "postToolUse", true
	case hooks.UserPromptSubmit:
		return "beforeSubmitPrompt", true
	case hooks.Stop:
		return "stop", true
	case hooks.SubagentStop:
		return "subagentStop", true
	case hooks.SessionStart:
		return "sessionStart", true
	case hooks.SessionEnd:
		return "sessionEnd", true
	case hooks.PreCompact:
		return "preCompact", true
	case hooks.PostToolUseFailure, hooks.SubagentStart:
		return event.CursorName()
	default:
		// An event only Cursor has reaches here from a Cursor hook file.
		if event.Claude() {
			return "", false
		}
		return event.CursorName()
	}
}
