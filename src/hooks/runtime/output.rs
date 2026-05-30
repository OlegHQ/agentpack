use serde_json::{json, Value};

use crate::hooks::ir::{ClaudeEvent, HookDecision, NormalizedHookResult};

// ---- guidance-injection output formats (emitted by the `inject-guidance` hook) ----

/// Claude/Grok style: wrap the guidance body in `hookSpecificOutput.additionalContext`.
pub(crate) fn guidance_hook_specific(body: &str, event: &str) -> Value {
    json!({
        "hookSpecificOutput": {
            "hookEventName": event,
            "additionalContext": body,
        }
    })
}

/// Codex/Agy style: top-level `additionalContext` plus `continue: true`.
pub(crate) fn guidance_additional_context_continue(body: &str) -> Value {
    json!({ "additionalContext": body, "continue": true })
}

/// Cursor/OpenCode style (and the default): a bare `additional_context` field.
pub(crate) fn guidance_additional_context(body: &str) -> Value {
    json!({ "additional_context": body })
}

pub(crate) fn codex_output(result: &NormalizedHookResult) -> Value {
    let mut value = json!({
        "permissionDecision": result.decision.as_str(),
        "continue": result.decision != HookDecision::Deny,
    });
    if let Some(object) = value.as_object_mut() {
        if let Some(message) = &result.message {
            object.insert("permissionDecisionReason".to_string(), json!(message));
            object.insert("stopReason".to_string(), json!(message));
        }
        if let Some(context) = &result.additional_context {
            object.insert("additionalContext".to_string(), json!(context));
        }
        if let Some(updated_input) = &result.updated_input {
            object.insert("updatedInput".to_string(), updated_input.clone());
        }
        if let Some(updated_tool_output) = &result.updated_tool_output {
            object.insert(
                "updatedMCPToolOutput".to_string(),
                updated_tool_output.clone(),
            );
        }
        for (key, value) in &result.metadata {
            object.insert(key.clone(), value.clone());
        }
    }
    value
}

pub(crate) fn cursor_output(event: ClaudeEvent, result: &NormalizedHookResult) -> Value {
    let is_blocking = matches!(
        event,
        ClaudeEvent::PreToolUse | ClaudeEvent::PermissionRequest
    );
    let mut value = if is_blocking {
        json!({
            "permission": result.decision.as_str(),
        })
    } else {
        json!({
            "continue": result.decision != HookDecision::Deny,
        })
    };
    if let Some(object) = value.as_object_mut() {
        if let Some(message) = &result.message {
            object.insert("user_message".to_string(), json!(message));
        }
        if let Some(context) = &result.additional_context {
            object.insert("additional_context".to_string(), json!(context));
        }
        if let Some(updated_input) = &result.updated_input {
            object.insert("updated_input".to_string(), updated_input.clone());
        }
        if let Some(updated_tool_output) = &result.updated_tool_output {
            object.insert(
                "updated_mcp_tool_output".to_string(),
                updated_tool_output.clone(),
            );
        }
        for (key, value) in &result.metadata {
            object.insert(key.clone(), value.clone());
        }
    }
    value
}

pub(crate) fn claude_fallback_output(result: &NormalizedHookResult) -> Value {
    json!({
        "decision": result.decision.as_str(),
        "message": result.message,
        "additional_context": result.additional_context,
        "updated_input": result.updated_input,
        "updated_tool_output": result.updated_tool_output,
        "metadata": result.metadata,
    })
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn guidance_format_shapes() {
        let claude = guidance_hook_specific("body text", "SessionStart");
        assert_eq!(
            claude["hookSpecificOutput"]["additionalContext"],
            "body text"
        );
        assert_eq!(
            claude["hookSpecificOutput"]["hookEventName"],
            "SessionStart"
        );

        let codex = guidance_additional_context_continue("body text");
        assert_eq!(codex["additionalContext"], "body text");
        assert_eq!(codex["continue"], true);

        let cursor = guidance_additional_context("body text");
        assert_eq!(cursor["additional_context"], "body text");
    }
}
