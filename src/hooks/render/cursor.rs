use serde_json::{json, Map, Value};

use crate::artifacts::HarnessTarget;
use crate::error::Result;

use super::{
    build_exec_spec_file, check_support, output_target_for, push_diag, strict_mapping_error,
    HookRenderer, RenderContext, RenderedHookFile, RenderedHookFileContents, RenderedHookOutput,
};
use crate::hooks::ir::{ClaudeEvent, ClaudeHandler, HookBundle, NormalizedHook};
use crate::hooks::paths::hook_exec_command;

pub struct CursorHookRenderer;

impl HookRenderer for CursorHookRenderer {
    fn target(&self) -> HarnessTarget {
        HarnessTarget::Cursor
    }

    fn render(&self, bundle: &HookBundle, ctx: &RenderContext<'_>) -> Result<RenderedHookOutput> {
        let mut output = RenderedHookOutput::default();
        let mut hooks_map = Map::new();
        for hook in &bundle.hooks {
            let Some(step) = mapped_cursor_step(hook.event) else {
                if hook.is_strict() {
                    return Err(strict_mapping_error(
                        hook,
                        HarnessTarget::Cursor,
                        "Cursor has no equivalent lifecycle step",
                    ));
                }
                push_diag(
                    &mut output,
                    "omitted",
                    hook,
                    "Cursor has no equivalent lifecycle step",
                );
                continue;
            };
            if !check_support(
                HarnessTarget::Cursor,
                hook,
                &mut output,
                "rendered into Cursor native hook config",
                "wrapped into agentpack hook-exec for Cursor",
            )? {
                continue;
            }
            let matcher = rewrite_cursor_matcher(hook, &mut output)?;
            if hook.matcher.is_some() && matcher.is_none() && hook.is_strict() {
                return Err(strict_mapping_error(
                    hook,
                    HarnessTarget::Cursor,
                    "all matcher segments are unsupported on Cursor",
                ));
            }
            let entry = render_entry(hook, matcher, ctx, &mut output)?;
            hooks_map
                .entry(step.to_string())
                .or_insert_with(|| Value::Array(Vec::new()));
            hooks_map
                .get_mut(step)
                .and_then(Value::as_array_mut)
                .expect("step array")
                .push(entry);
        }
        if !hooks_map.is_empty() {
            output.files.push(RenderedHookFile {
                path: ctx.target_root.join("hooks/hooks.json"),
                contents: RenderedHookFileContents::Json(json!({
                    "version": 1,
                    "hooks": hooks_map,
                })),
            });
        }
        Ok(output)
    }
}

fn mapped_cursor_step(event: ClaudeEvent) -> Option<&'static str> {
    match event {
        ClaudeEvent::PreToolUse => Some("preToolUse"),
        ClaudeEvent::PostToolUse => Some("postToolUse"),
        ClaudeEvent::UserPromptSubmit => Some("beforeSubmitPrompt"),
        ClaudeEvent::Stop => Some("stop"),
        ClaudeEvent::SubagentStop => Some("subagentStop"),
        ClaudeEvent::SessionStart => Some("sessionStart"),
        ClaudeEvent::SessionEnd => Some("sessionEnd"),
        ClaudeEvent::PreCompact => Some("preCompact"),
        ClaudeEvent::PermissionRequest => Some("preToolUse"),
        ClaudeEvent::Notification => None,
    }
}

fn rewrite_cursor_matcher(
    hook: &NormalizedHook,
    output: &mut RenderedHookOutput,
) -> Result<Option<String>> {
    let Some(matcher) = &hook.matcher else {
        return Ok(None);
    };
    let mut mapped = Vec::new();
    let mut stripped = Vec::new();
    for raw in matcher.split('|') {
        let token = raw.trim();
        if token.is_empty() {
            continue;
        }
        match token {
            "Bash" => mapped.push("Shell".to_string()),
            "Edit" | "Write" => mapped.push("Write".to_string()),
            "Read" | "Grep" | "List" | "Delete" | "Fetch" | "ComputerUse" | "ReadLints"
            | "BackgroundShell" | "WriteShellStdin" | "ListMcpResources" | "FetchMcpResource"
            | "WebSearch" => {
                if token == "WebSearch" {
                    stripped.push(token.to_string());
                } else {
                    mapped.push(token.to_string());
                }
            }
            "WebFetch" => mapped.push("Fetch".to_string()),
            "Glob" => stripped.push(token.to_string()),
            _ if token.starts_with("mcp__") => {
                let mut parts = token.split("__");
                let _ = parts.next();
                let _ = parts.next();
                if let Some(tool) = parts.next() {
                    mapped.push(format!("MCP:{tool}"));
                } else {
                    stripped.push(token.to_string());
                }
            }
            other => mapped.push(other.to_string()),
        }
    }
    if !stripped.is_empty() {
        push_diag(
            output,
            "degraded",
            hook,
            format!(
                "Cursor cannot fire hooks for matcher segments: {}",
                stripped.join(", ")
            ),
        );
    }
    if mapped.is_empty() {
        return Ok(None);
    }
    Ok(Some(mapped.join("|")))
}

fn render_entry(
    hook: &NormalizedHook,
    matcher: Option<String>,
    ctx: &RenderContext<'_>,
    output: &mut RenderedHookOutput,
) -> Result<Value> {
    match &hook.handler {
        ClaudeHandler::Prompt(handler) => {
            let mut entry = Map::new();
            if let Some(matcher) = matcher {
                entry.insert("matcher".to_string(), Value::String(matcher));
            }
            entry.insert("type".to_string(), Value::String("prompt".to_string()));
            entry.insert("prompt".to_string(), Value::String(handler.prompt.clone()));
            if let Some(model) = &handler.model {
                entry.insert("model".to_string(), Value::String(model.clone()));
            }
            if hook.is_strict() || matches!(hook.event, ClaudeEvent::PermissionRequest) {
                entry.insert("failClosed".to_string(), Value::Bool(true));
            }
            Ok(Value::Object(entry))
        }
        _ => {
            let kind = hook.handler.kind_name();
            let spec_path =
                build_exec_spec_file(HarnessTarget::Cursor, hook, hook.event, ctx, output)?;
            let mut entry = Map::new();
            if let Some(matcher) = matcher {
                entry.insert("matcher".to_string(), Value::String(matcher));
            }
            entry.insert("type".to_string(), Value::String("command".to_string()));
            entry.insert(
                "command".to_string(),
                Value::String(hook_exec_command(
                    kind,
                    output_target_for(HarnessTarget::Cursor),
                    &spec_path,
                )),
            );
            if hook.is_strict() || matches!(hook.event, ClaudeEvent::PermissionRequest) {
                entry.insert("failClosed".to_string(), Value::Bool(true));
            }
            Ok(Value::Object(entry))
        }
    }
}
