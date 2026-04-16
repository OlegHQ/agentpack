use std::path::Path;

use serde_json::Value;

use crate::error::Result;

use super::{build_hook, invalid, object_extra};
use crate::hooks::ir::{ClaudeEvent, HookBundle, HookOrigin};

pub fn parse_claude_hooks(
    path: &Path,
    value: &Value,
    base_origin: &HookOrigin,
) -> Result<HookBundle> {
    let hooks_object = value
        .get("hooks")
        .and_then(Value::as_object)
        .ok_or_else(|| invalid(path, "Claude hook file must contain top-level hooks object"))?;

    let mut hooks = Vec::new();
    for (event_index, (event_name, groups)) in hooks_object.iter().enumerate() {
        let event = ClaudeEvent::from_any_str(event_name)
            .ok_or_else(|| invalid(path, format!("unknown Claude event `{event_name}`")))?;
        let groups = groups
            .as_array()
            .ok_or_else(|| invalid(path, "event groups must be arrays"))?;
        for (matcher_group_index, group) in groups.iter().enumerate() {
            let group_obj = group
                .as_object()
                .ok_or_else(|| invalid(path, "matcher groups must be objects"))?;
            let matcher = group_obj
                .get("matcher")
                .and_then(Value::as_str)
                .map(ToOwned::to_owned);
            let group_extra = object_extra(group_obj, &["matcher", "hooks"]);
            let group_hooks = group_obj
                .get("hooks")
                .and_then(Value::as_array)
                .ok_or_else(|| invalid(path, "matcher groups must contain hooks arrays"))?;
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
        }
    }

    Ok(HookBundle { hooks })
}
