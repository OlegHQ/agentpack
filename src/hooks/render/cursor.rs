//! Cursor hook renderer.
//!
//! Cursor's native matcher is coarser than Claude's (no `Glob`, no regex-style alternations
//! like `Edit|Write` without semantic equivalence, no `mcp__*` syntax). Rather than lossily
//! down-translating each matcher, we register one blanket entry per Cursor lifecycle event
//! whose command invokes `agentpack hook-exec dispatch ...`. The router reads Cursor's stdin
//! (which includes `tool_name`), normalizes it to a candidate Claude tool name set, then
//! iterates stored specs under the staged specs directory and fires the ones whose original
//! Claude matcher matches — giving Cursor the full Claude matcher vocabulary.

use serde_json::{json, Map, Value};

use crate::artifacts::HarnessTarget;
use crate::error::Result;

use super::{
    build_exec_spec_file, check_support, push_diag, HookRenderer, RenderContext, RenderedHookFile,
    RenderedHookFileContents, RenderedHookOutput,
};
use crate::hooks::ir::{ClaudeEvent, HookBundle, NormalizedHook};
use crate::hooks::paths::{hook_dispatch_command, specs_dispatch_root};

pub struct CursorHookRenderer;

impl HookRenderer for CursorHookRenderer {
    fn target(&self) -> HarnessTarget {
        HarnessTarget::Cursor
    }

    fn render(&self, bundle: &HookBundle, ctx: &RenderContext<'_>) -> Result<RenderedHookOutput> {
        let mut output = RenderedHookOutput::default();
        let mut entries_per_event: std::collections::BTreeMap<&'static str, Vec<Value>> =
            std::collections::BTreeMap::new();

        for hook in &bundle.hooks {
            let Some(step) = mapped_cursor_step(hook.event) else {
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
                "routed via agentpack hook-exec dispatch",
                "routed via agentpack hook-exec dispatch",
            )? {
                continue;
            }
            // Always write the spec so the dispatcher can find it; the command line emitted
            // in hooks.json is blanket (one per event) so we deliberately ignore the return path.
            let _ =
                build_exec_spec_file(HarnessTarget::Cursor, hook, hook.event, ctx, &mut output)?;

            entries_per_event.entry(step).or_default();
            add_blanket_entry(
                &mut entries_per_event,
                step,
                hook.event,
                ctx,
                needs_fail_closed(hook),
            );
        }

        if entries_per_event.is_empty() {
            return Ok(output);
        }

        let mut hooks_map = Map::new();
        for (step, entries) in entries_per_event {
            hooks_map.insert(step.into(), Value::Array(entries));
        }

        output.files.push(RenderedHookFile {
            path: ctx.target_root.join("hooks/hooks.json"),
            contents: RenderedHookFileContents::Json(json!({
                "version": 1,
                "hooks": hooks_map,
            })),
        });
        Ok(output)
    }
}

/// Insert a single dispatcher entry for `(cursor_step, claude_event)` if one isn't there yet.
/// Cursor fires every entry registered under a step; one per (step,event) is all we need.
fn add_blanket_entry(
    entries_per_event: &mut std::collections::BTreeMap<&'static str, Vec<Value>>,
    step: &'static str,
    event: ClaudeEvent,
    ctx: &RenderContext<'_>,
    fail_closed: bool,
) {
    let specs_dir = specs_dispatch_root(HarnessTarget::Cursor, ctx.target_root);
    let command = hook_dispatch_command(HarnessTarget::Cursor, event.as_claude_str(), &specs_dir);
    let entries = entries_per_event.entry(step).or_default();
    let already = entries.iter().any(|entry| {
        entry
            .get("command")
            .and_then(Value::as_str)
            .is_some_and(|c| c == command)
    });
    if already {
        return;
    }
    let mut obj = Map::new();
    obj.insert("type".into(), Value::String("command".into()));
    obj.insert("command".into(), Value::String(command));
    if fail_closed {
        obj.insert("failClosed".into(), Value::Bool(true));
    }
    entries.push(Value::Object(obj));
}

fn needs_fail_closed(hook: &NormalizedHook) -> bool {
    hook.is_strict() || matches!(hook.event, ClaudeEvent::PermissionRequest)
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
