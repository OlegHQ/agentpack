use serde_json::{json, Map, Value};

use crate::artifacts::HarnessTarget;
use crate::error::Result;

use super::{
    build_exec_spec_file, check_support, handler_to_json_object, HookRenderer, RenderContext,
    RenderedHookFile, RenderedHookFileContents, RenderedHookOutput,
};
use crate::hooks::ir::{ClaudeEvent, ClaudeHandler, HookBundle, HookLayer, NormalizedHook};
use crate::hooks::paths::hook_exec_command;

pub struct CodexHookRenderer;

impl HookRenderer for CodexHookRenderer {
    fn target(&self) -> HarnessTarget {
        HarnessTarget::Codex
    }

    fn render(&self, bundle: &HookBundle, ctx: &RenderContext<'_>) -> Result<RenderedHookOutput> {
        let mut output = RenderedHookOutput::default();
        if bundle.hooks.is_empty() {
            return Ok(output);
        }

        let mut hooks_map = Map::new();
        for hook in &bundle.hooks {
            let mapped_event = match hook.event {
                ClaudeEvent::PermissionRequest => ClaudeEvent::PreToolUse,
                other => other,
            };
            if !check_support(
                HarnessTarget::Codex,
                hook,
                &mut output,
                "rendered into Codex native hooks.json",
                "wrapped into agentpack hook-exec for Codex",
            )? {
                continue;
            }
            let handler_value = render_handler(hook, mapped_event, ctx, &mut output)?;
            hooks_map
                .entry(mapped_event.as_codex_str().to_string())
                .or_insert_with(|| Value::Array(Vec::new()));
            let groups = hooks_map
                .get_mut(mapped_event.as_codex_str())
                .and_then(Value::as_array_mut)
                .expect("event group array");
            let mut group = Map::new();
            if let Some(matcher) = &hook.matcher {
                group.insert("matcher".to_string(), Value::String(matcher.clone()));
            }
            group.insert("hooks".to_string(), Value::Array(vec![handler_value]));
            groups.push(Value::Object(group));
        }
        if !hooks_map.is_empty() {
            output.files.push(RenderedHookFile {
                path: ctx.target_root.join("hooks.json"),
                contents: RenderedHookFileContents::Json(json!({ "hooks": hooks_map })),
            });
        }
        Ok(output)
    }
}

fn render_handler(
    hook: &NormalizedHook,
    event: ClaudeEvent,
    ctx: &RenderContext<'_>,
    output: &mut RenderedHookOutput,
) -> Result<Value> {
    if hook.origin.layer == HookLayer::SeededNative {
        return Ok(handler_to_json_object(hook, false));
    }
    match &hook.handler {
        ClaudeHandler::Prompt(_) => Ok(handler_to_json_object(hook, false)),
        _ => {
            let kind = hook.handler.kind_name();
            let spec_path = build_exec_spec_file(HarnessTarget::Codex, hook, event, ctx, output)?;
            Ok(json!({
                "type": "command",
                "command": hook_exec_command(kind, HarnessTarget::Codex, &spec_path),
            }))
        }
    }
}
