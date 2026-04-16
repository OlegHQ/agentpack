use std::path::Path;

use serde_json::Value;

use crate::error::Result;

use super::{build_hook, invalid, object_extra};
use crate::hooks::ir::{ClaudeEvent, HookBundle, HookOrigin};

fn strip_jsonc(raw: &str) -> std::result::Result<Value, serde_json::Error> {
    let mut buf = raw.as_bytes().to_vec();
    let _ = json_strip_comments::strip_slice(&mut buf);
    serde_json::from_slice(&buf)
}

fn cursor_step_to_event(step: &str) -> Option<ClaudeEvent> {
    match step {
        "preToolUse" => Some(ClaudeEvent::PreToolUse),
        "postToolUse" => Some(ClaudeEvent::PostToolUse),
        "beforeSubmitPrompt" => Some(ClaudeEvent::UserPromptSubmit),
        "stop" => Some(ClaudeEvent::Stop),
        "subagentStop" => Some(ClaudeEvent::SubagentStop),
        "sessionStart" => Some(ClaudeEvent::SessionStart),
        "sessionEnd" => Some(ClaudeEvent::SessionEnd),
        "preCompact" => Some(ClaudeEvent::PreCompact),
        _ => None,
    }
}

pub fn parse_cursor_hooks(path: &Path, raw: &str, base_origin: &HookOrigin) -> Result<HookBundle> {
    let value: Value = strip_jsonc(raw)
        .map_err(|err| invalid(path, format!("failed to parse Cursor JSONC hooks: {err}")))?;
    let hooks_root = value
        .get("hooks")
        .and_then(Value::as_object)
        .ok_or_else(|| {
            invalid(
                path,
                "Cursor hooks file must contain top-level hooks object",
            )
        })?;

    let mut hooks = Vec::new();
    for (event_index, (step_name, entries)) in hooks_root.iter().enumerate() {
        let Some(event) = cursor_step_to_event(step_name) else {
            continue;
        };
        let entries = entries
            .as_array()
            .ok_or_else(|| invalid(path, "Cursor step entries must be arrays"))?;
        for (matcher_group_index, entry) in entries.iter().enumerate() {
            let entry_obj = entry
                .as_object()
                .ok_or_else(|| invalid(path, "Cursor step entries must be objects"))?;
            let matcher = entry_obj
                .get("matcher")
                .and_then(Value::as_str)
                .map(ToOwned::to_owned);
            let group_extra = object_extra(entry_obj, &["matcher"]);
            let mut origin = base_origin.clone();
            origin.event_index = event_index;
            origin.matcher_group_index = matcher_group_index;
            origin.hook_index = 0;
            hooks.push(build_hook(
                event,
                matcher,
                entry,
                origin,
                group_extra,
                path,
            )?);
        }
    }

    Ok(HookBundle { hooks })
}
