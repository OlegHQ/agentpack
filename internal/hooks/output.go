package hooks

import "github.com/OlegHQ/agentpack/internal/harness"

func GuidanceHookSpecific(body string, event Event) map[string]any {
	return map[string]any{"hookSpecificOutput": map[string]any{"hookEventName": string(event), "additionalContext": body}}
}

func GuidanceAdditionalContextContinue(body string) map[string]any {
	return map[string]any{"additionalContext": body, "continue": true}
}
func GuidanceAdditionalContext(body string) map[string]any {
	return map[string]any{"additional_context": body}
}

func HookOutput(target harness.Target, event Event, result Result) any {
	switch target {
	case harness.Codex, harness.Agy:
		return codexOutput(result)
	case harness.Cursor:
		return cursorOutput(event, result)
	case harness.OpenCode:
		return result
	default:
		metadata := result.Metadata
		if metadata == nil {
			metadata = map[string]any{}
		}
		return map[string]any{"decision": result.Decision, "message": nullable(result.Message), "additional_context": nullable(result.AdditionalContext), "updated_input": result.UpdatedInput, "updated_tool_output": result.UpdatedToolOutput, "metadata": metadata}
	}
}

func codexOutput(result Result) map[string]any {
	value := map[string]any{"permissionDecision": result.Decision, "continue": result.Decision != Deny}
	if result.Message != "" {
		value["permissionDecisionReason"], value["stopReason"] = result.Message, result.Message
	}
	if result.AdditionalContext != "" {
		value["additionalContext"] = result.AdditionalContext
	}
	if result.UpdatedInput != nil {
		value["updatedInput"] = result.UpdatedInput
	}
	if result.UpdatedToolOutput != nil {
		value["updatedMCPToolOutput"] = result.UpdatedToolOutput
	}
	mergeMetadata(value, result.Metadata)
	return value
}

func cursorOutput(event Event, result Result) map[string]any {
	value := make(map[string]any)
	if event == PreToolUse || event == PermissionRequest {
		value["permission"] = result.Decision
	} else {
		value["continue"] = result.Decision != Deny
	}
	if result.Message != "" {
		value["user_message"] = result.Message
	}
	if result.AdditionalContext != "" {
		value["additional_context"] = result.AdditionalContext
	}
	if result.UpdatedInput != nil {
		value["updated_input"] = result.UpdatedInput
	}
	if result.UpdatedToolOutput != nil {
		value["updated_mcp_tool_output"] = result.UpdatedToolOutput
	}
	mergeMetadata(value, result.Metadata)
	return value
}

func mergeMetadata(target, metadata map[string]any) {
	for key, value := range metadata {
		target[key] = value
	}
}
func nullable(value string) any {
	if value == "" {
		return nil
	}
	return value
}
