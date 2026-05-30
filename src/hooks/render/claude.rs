use serde_json::{json, Map, Value};

use crate::artifacts::HarnessTarget;
use crate::error::Result;

use super::{
    build_exec_spec_file, handler_to_json_object, push_diag, HookRenderer,
    RenderContext, RenderedHookFile, RenderedHookFileContents, RenderedHookOutput,
};
use crate::hooks::ir::{ClaudeHandler, HookBundle, NormalizedHook};
use crate::hooks::paths::hook_exec_command;

pub struct ClaudeHookRenderer;

impl HookRenderer for ClaudeHookRenderer {
    fn target(&self) -> HarnessTarget {
        HarnessTarget::Claude
    }

    fn render(&self, bundle: &HookBundle, ctx: &RenderContext<'_>) -> Result<RenderedHookOutput> {
        let mut output = RenderedHookOutput::default();
        if bundle.hooks.is_empty() {
            return Ok(output);
        }

        let mut hooks_map = Map::new();
        for hook in &bundle.hooks {
            let handler_value = render_handler(hook, ctx, &mut output)?;
            hooks_map
                .entry(hook.event.as_claude_str().to_string())
                .or_insert_with(|| Value::Array(Vec::new()));
            let groups = hooks_map
                .get_mut(hook.event.as_claude_str())
                .and_then(Value::as_array_mut)
                .expect("event group array");
            let mut group = Map::new();
            if let Some(matcher) = &hook.matcher {
                group.insert("matcher".to_string(), Value::String(matcher.clone()));
            }
            for (key, value) in &hook.matcher_group_extra {
                group.insert(key.clone(), value.clone());
            }
            group.insert("hooks".to_string(), Value::Array(vec![handler_value]));
            groups.push(Value::Object(group));
        }
        output.files.push(RenderedHookFile {
            path: ctx.target_root.join("hooks/hooks.json"),
            contents: RenderedHookFileContents::Json(json!({ "hooks": hooks_map })),
        });
        Ok(output)
    }
}

fn render_handler(
    hook: &NormalizedHook,
    ctx: &RenderContext<'_>,
    output: &mut RenderedHookOutput,
) -> Result<Value> {
    match &hook.handler {
        ClaudeHandler::Command(handler) => {
            let spec_path =
                build_exec_spec_file(HarnessTarget::Claude, hook, hook.event, ctx, output)?;
            push_diag(
                output,
                "emulated",
                hook,
                "wrapped command hook to preserve package-relative working directory",
            );
            let mut obj = Map::new();
            obj.insert("type".into(), Value::String("command".into()));
            obj.insert(
                "command".into(),
                Value::String(hook_exec_command(
                    "command",
                    HarnessTarget::Claude,
                    &spec_path,
                )),
            );
            if let Some(secs) = handler.timeout_secs {
                obj.insert("timeout".into(), json!(secs));
            }
            Ok(Value::Object(obj))
        }
        ClaudeHandler::Http(_) | ClaudeHandler::Prompt(_) | ClaudeHandler::Agent(_) => {
            let kind = hook.handler.kind_name();
            push_diag(
                output,
                "native",
                hook,
                format!("rendered Claude {kind} hook natively"),
            );
            Ok(handler_to_json_object(hook, true))
        }
    }
}
