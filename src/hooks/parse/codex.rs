use std::path::Path;

use serde_json::Value;

use crate::error::Result;

use super::{build_hook, invalid, object_extra};
use crate::hooks::ir::{ClaudeEvent, HookBundle, HookOrigin};

pub fn parse_codex_hooks(
    path: &Path,
    value: &Value,
    base_origin: &HookOrigin,
) -> Result<HookBundle> {
    let hooks_root = if let Some(hooks) = value.get("hooks") {
        hooks
    } else {
        value
    };
    let hooks_object = hooks_root
        .as_object()
        .ok_or_else(|| invalid(path, "Codex hooks file must be an object"))?;

    let mut hooks = Vec::new();
    for (event_index, (event_name, entries)) in hooks_object.iter().enumerate() {
        let event = ClaudeEvent::from_any_str(event_name)
            .ok_or_else(|| invalid(path, format!("unknown Codex hook event `{event_name}`")))?;
        let entries = entries
            .as_array()
            .ok_or_else(|| invalid(path, "Codex hook event entries must be arrays"))?;
        for (matcher_group_index, entry) in entries.iter().enumerate() {
            let entry_obj = entry
                .as_object()
                .ok_or_else(|| invalid(path, "Codex hook entries must be objects"))?;
            if let Some(group_hooks) = entry_obj.get("hooks").and_then(Value::as_array) {
                let matcher = entry_obj
                    .get("matcher")
                    .and_then(Value::as_str)
                    .map(ToOwned::to_owned);
                let group_extra = object_extra(entry_obj, &["matcher", "hooks"]);
                for (hook_index, handler_value) in group_hooks.iter().enumerate() {
                    let mut origin = base_origin.clone();
                    origin.event_index = event_index;
                    origin.matcher_group_index = matcher_group_index;
                    origin.hook_index = hook_index;
                    hooks.push(build_hook(
                        event,
                        matcher.clone(),
                        handler_value,
                        origin,
                        group_extra.clone(),
                        path,
                    )?);
                }
                continue;
            }

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
