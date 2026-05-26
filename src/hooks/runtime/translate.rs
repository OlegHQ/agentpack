use serde_json::{json, Value};

use crate::hooks::ir::{ClaudeEvent, HookDecision, HookOutputTarget, NormalizedHookResult};

pub fn to_target_output(
    target: HookOutputTarget,
    event: ClaudeEvent,
    result: &NormalizedHookResult,
) -> Value {
    match target {
        HookOutputTarget::Opencode => serde_json::to_value(result).unwrap_or(Value::Null),
        HookOutputTarget::Codex => codex_output(result),
        HookOutputTarget::Cursor => cursor_output(event, result),
        HookOutputTarget::Claude => claude_fallback_output(result),
        HookOutputTarget::Grok => claude_fallback_output(result),
        HookOutputTarget::Agy => codex_output(result),
    }
}

fn codex_output(result: &NormalizedHookResult) -> Value {
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

fn cursor_output(event: ClaudeEvent, result: &NormalizedHookResult) -> Value {
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

fn claude_fallback_output(result: &NormalizedHookResult) -> Value {
    json!({
        "decision": result.decision.as_str(),
        "message": result.message,
        "additional_context": result.additional_context,
        "updated_input": result.updated_input,
        "updated_tool_output": result.updated_tool_output,
        "metadata": result.metadata,
    })
}
