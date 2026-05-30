//! OpenCode hook rendering: generates a Bun plugin (`plugins/agentpack-hooks/`) that bridges
//! Claude-style hooks into OpenCode's lifecycle events via `agentpack hook-exec`.

use serde_json::{json, Value};

use crate::artifacts::HarnessTarget;
use crate::error::Result;
use crate::fs_util::read_json_value_opt;
use crate::hooks::capabilities::SupportLevel;
use crate::hooks::ir::{ClaudeEvent, ClaudeHandler, HookBundle};
use crate::hooks::render::{
    build_exec_spec_file, check_support, push_diag, strict_mapping_error, HookRenderer,
    RenderContext, RenderedHookFile, RenderedHookFileContents, RenderedHookOutput,
};

pub(super) struct OpenCodeHookRenderer;

impl HookRenderer for OpenCodeHookRenderer {
    fn target(&self) -> HarnessTarget {
        HarnessTarget::OpenCode
    }

    fn render(&self, bundle: &HookBundle, ctx: &RenderContext<'_>) -> Result<RenderedHookOutput> {
        let mut output = RenderedHookOutput::default();
        if bundle.hooks.is_empty() {
            return Ok(output);
        }
        let plugin_root = ctx.target_root.join("plugins/agentpack-hooks");
        let mut config_entries = Vec::new();
        for hook in &bundle.hooks {
            let Some(event_name) = mapped_event_name(hook.event) else {
                if hook.is_strict() {
                    return Err(strict_mapping_error(
                        hook,
                        HarnessTarget::OpenCode,
                        "OpenCode has no equivalent lifecycle hook",
                    ));
                }
                push_diag(
                    &mut output,
                    "omitted",
                    hook,
                    "OpenCode has no equivalent lifecycle hook",
                );
                continue;
            };
            if !check_support(
                HarnessTarget::OpenCode,
                hook,
                &mut output,
                "rendered into generated OpenCode plugin",
                "wrapped into generated OpenCode plugin",
            )? {
                continue;
            }
            let spec_path =
                build_exec_spec_file(HarnessTarget::OpenCode, hook, hook.event, ctx, &mut output)?;
            config_entries.push(json!({
                "event": event_name,
                "matcher": hook.matcher,
                "kind": hook.handler.kind_name(),
                "specPath": spec_path,
                "strict": hook.is_strict(),
            }));
        }
        if config_entries.is_empty() {
            return Ok(output);
        }
        output.files.push(RenderedHookFile {
            path: plugin_root.join("config.json"),
            contents: RenderedHookFileContents::Json(json!({ "hooks": config_entries })),
        });
        output.files.push(RenderedHookFile {
            path: plugin_root.join("index.js"),
            contents: RenderedHookFileContents::Text(plugin_source()),
        });
        output.files.push(RenderedHookFile {
            path: ctx.target_root.join("opencode.json"),
            contents: RenderedHookFileContents::Json(merged_opencode_config(ctx.target_root)?),
        });
        Ok(output)
    }
}

/// Support level for emulating a Claude hook event+handler on OpenCode.
pub(super) fn opencode_support(event: ClaudeEvent, handler: &ClaudeHandler) -> SupportLevel {
    let event_level = match event {
        ClaudeEvent::PreToolUse
        | ClaudeEvent::PostToolUse
        | ClaudeEvent::PermissionRequest
        | ClaudeEvent::PreCompact => None,
        ClaudeEvent::UserPromptSubmit => Some(SupportLevel::Degraded {
            reason: "OpenCode exposes chat.message after receipt rather than Claude's submit hook",
        }),
        _ => Some(SupportLevel::Unsupported {
            reason: "OpenCode has no direct lifecycle hook for this Claude event",
        }),
    };
    if let Some(level) = event_level {
        return level;
    }
    match handler {
        ClaudeHandler::Command(_) => SupportLevel::Native,
        ClaudeHandler::Http(_) | ClaudeHandler::Prompt(_) | ClaudeHandler::Agent(_) => {
            SupportLevel::Emulated
        }
    }
}

fn mapped_event_name(event: ClaudeEvent) -> Option<&'static str> {
    match event {
        ClaudeEvent::PreToolUse => Some("tool.execute.before"),
        ClaudeEvent::PostToolUse => Some("tool.execute.after"),
        ClaudeEvent::UserPromptSubmit => Some("chat.message"),
        ClaudeEvent::PermissionRequest => Some("permission.ask"),
        ClaudeEvent::PreCompact => Some("experimental.session.compacting"),
        _ => None,
    }
}

fn merged_opencode_config(root: &std::path::Path) -> Result<Value> {
    let mut config = read_json_value_opt(&root.join("opencode.json"))?.unwrap_or_else(|| {
        json!({
            "$schema": "https://opencode.ai/config.json"
        })
    });
    let object = config.as_object_mut().ok_or_else(|| {
        crate::error::AgentpackError::Staging("opencode.json must be a JSON object".to_string())
    })?;
    let plugins = object
        .entry("plugin".to_string())
        .or_insert_with(|| Value::Array(Vec::new()));
    let plugins = plugins.as_array_mut().ok_or_else(|| {
        crate::error::AgentpackError::Staging("opencode.json plugin must be an array".to_string())
    })?;
    let plugin_ref = Value::String("./plugins/agentpack-hooks/index.js".to_string());
    if !plugins.iter().any(|entry| entry == &plugin_ref) {
        plugins.push(plugin_ref);
    }
    Ok(config)
}

fn plugin_source() -> String {
    r#"import { readFileSync } from "node:fs";

const config = JSON.parse(readFileSync(new URL("./config.json", import.meta.url), "utf8"));

function normalizeToolName(name) {
  return String(name ?? "").toLowerCase();
}

function matchesMatcher(matcher, toolName) {
  if (!matcher || matcher === "*") return true;
  const actual = normalizeToolName(toolName);
  return String(matcher)
    .split("|")
    .map((value) => value.trim().toLowerCase())
    .filter(Boolean)
    .some((candidate) => candidate === actual || candidate === "bash" && actual === "shell");
}

async function runHook(kind, specPath, payload) {
  const proc = Bun.spawn(
    ["agentpack", "hook-exec", kind, "--target", "opencode", "--spec", specPath],
    {
      stdin: "pipe",
      stdout: "pipe",
      stderr: "pipe",
    },
  );
  const writer = proc.stdin.getWriter();
  await writer.write(new TextEncoder().encode(JSON.stringify(payload)));
  await writer.close();
  const stdout = await new Response(proc.stdout).text();
  const stderr = await new Response(proc.stderr).text();
  const status = await proc.exited;
  if (status !== 0) {
    throw new Error(stderr || stdout || `hook-exec exited ${status}`);
  }
  return stdout.trim() ? JSON.parse(stdout) : { decision: "allow" };
}

function entriesFor(event, toolName) {
  return (config.hooks || []).filter((entry) => entry.event === event && matchesMatcher(entry.matcher, toolName));
}

export default {
  id: "agentpack-hooks",
  server: async () => ({
    "tool.execute.before": async (input, output) => {
      for (const entry of entriesFor("tool.execute.before", input.tool)) {
        const result = await runHook(entry.kind, entry.specPath, { input, output });
        if (result.updated_input !== undefined) {
          output.args = result.updated_input;
        }
        if (result.decision === "deny" || result.decision === "ask") {
          throw new Error(result.message || "OpenCode hook blocked tool execution");
        }
      }
    },
    "tool.execute.after": async (input, output) => {
      for (const entry of entriesFor("tool.execute.after", input.tool)) {
        const result = await runHook(entry.kind, entry.specPath, { input, output });
        if (result.updated_tool_output !== undefined) {
          output.output = typeof result.updated_tool_output === "string"
            ? result.updated_tool_output
            : JSON.stringify(result.updated_tool_output);
        }
        if (result.additional_context) {
          output.output = `${output.output}\n\n${result.additional_context}`;
        }
      }
    },
    "permission.ask": async (input, output) => {
      for (const entry of entriesFor("permission.ask", input.tool || input.command || input.kind)) {
        const result = await runHook(entry.kind, entry.specPath, { input, output });
        if (result.decision === "deny") output.status = "deny";
        else if (result.decision === "ask" && output.status !== "deny") output.status = "ask";
        else if (result.decision === "allow" && output.status !== "deny") output.status = "allow";
      }
    },
    "chat.message": async (input, output) => {
      for (const entry of entriesFor("chat.message", "message")) {
        const result = await runHook(entry.kind, entry.specPath, { input, output });
        if (result.additional_context) {
          output.parts.push({ type: "text", text: result.additional_context });
        }
      }
    },
    "experimental.session.compacting": async (input, output) => {
      for (const entry of entriesFor("experimental.session.compacting", "compact")) {
        const result = await runHook(entry.kind, entry.specPath, { input, output });
        if (result.additional_context) {
          output.context.push(result.additional_context);
        }
      }
    },
  }),
};
"#
    .to_string()
}
