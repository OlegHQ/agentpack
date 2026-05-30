//! Cursor hook rendering + support.
//!
//! Cursor's native matcher is coarser than Claude's (no `Glob`, no regex alternations like
//! `Edit|Write`, no `mcp__*` syntax). Rather than lossily down-translate, we register one blanket
//! entry per Cursor lifecycle event whose command invokes `agentpack hook-exec dispatch ...`; the
//! router reads Cursor's stdin (`tool_name`), normalizes it to candidate Claude tool names, and
//! fires the stored specs whose original Claude matcher matches.

use serde_json::{json, Map, Value};

use crate::error::Result;
use crate::harness::HarnessTarget;
use crate::hooks::capabilities::SupportLevel;
use crate::hooks::ir::{ClaudeEvent, ClaudeHandler, HookBundle, NormalizedHook};
use crate::hooks::paths::{hook_dispatch_command, specs_dispatch_root};
use crate::hooks::render::{
    build_exec_spec_file, check_support, push_diag, HookRenderer, RenderContext, RenderedHookFile,
    RenderedHookFileContents, RenderedHookOutput,
};

pub(super) struct CursorHookRenderer;

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

/// Support level for emulating a Claude hook event+handler on Cursor.
pub(super) fn cursor_support(event: ClaudeEvent, handler: &ClaudeHandler) -> SupportLevel {
    match event {
        ClaudeEvent::Notification => SupportLevel::Unsupported {
            reason: "Cursor has no notification hook surface",
        },
        ClaudeEvent::PermissionRequest => match handler {
            ClaudeHandler::Http(_) | ClaudeHandler::Agent(_) => SupportLevel::Degraded {
                reason: "Cursor permission hooks are decomposed into preToolUse bridge commands",
            },
            _ => SupportLevel::Degraded {
                reason: "Cursor models permission requests as preToolUse instead of a dedicated event",
            },
        },
        ClaudeEvent::SessionStart | ClaudeEvent::PreCompact => match handler {
            ClaudeHandler::Http(_) | ClaudeHandler::Agent(_) => SupportLevel::Degraded {
                reason: "Cursor supports the lifecycle but requires bridge execution for this handler type",
            },
            _ => SupportLevel::Degraded {
                reason: "Cursor cannot preserve Claude trigger-specific matchers for this lifecycle event",
            },
        },
        _ => match handler {
            ClaudeHandler::Command(_) | ClaudeHandler::Prompt(_) => SupportLevel::Native,
            ClaudeHandler::Http(_) | ClaudeHandler::Agent(_) => SupportLevel::Emulated,
        },
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
