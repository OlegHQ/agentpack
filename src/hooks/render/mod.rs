mod claude;
mod codex;
mod cursor;

use std::collections::BTreeMap;
use std::path::{Path, PathBuf};

use serde_json::Value;

use crate::artifacts::HarnessTarget;
use crate::error::{AgentpackError, Result};

use super::capabilities::SupportLevel;
use super::ir::{ClaudeHandler, HookBundle, NormalizedHook};
use super::paths::{spec_path_for_hook, staged_package_root};
use super::runtime::bridge::HookExecutionSpec;

pub use claude::ClaudeHookRenderer;
pub use codex::CodexHookRenderer;
pub use cursor::CursorHookRenderer;

#[derive(Clone, Debug)]
pub enum RenderedHookFileContents {
    Json(Value),
    Text(String),
}

#[derive(Clone, Debug)]
pub struct RenderedHookFile {
    pub path: PathBuf,
    pub contents: RenderedHookFileContents,
}

#[derive(Clone, Debug)]
pub struct HookDiagnostic {
    pub level: &'static str,
    pub source: String,
    pub message: String,
}

#[derive(Clone, Debug, Default)]
pub struct HookRenderSummary {
    pub native: usize,
    pub emulated: usize,
    pub degraded: usize,
    pub omitted: usize,
}

#[derive(Clone, Debug, Default)]
pub struct RenderedHookOutput {
    pub files: Vec<RenderedHookFile>,
    pub diagnostics: Vec<HookDiagnostic>,
    pub summary: HookRenderSummary,
}

pub struct RenderContext<'a> {
    pub project_root: &'a Path,
    pub target_root: &'a Path,
    pub staged_packages: &'a BTreeMap<String, PathBuf>,
}

pub trait HookRenderer {
    fn target(&self) -> HarnessTarget;
    fn render(&self, bundle: &HookBundle, ctx: &RenderContext<'_>) -> Result<RenderedHookOutput>;
}

pub fn push_diag(
    output: &mut RenderedHookOutput,
    level: &'static str,
    hook: &super::ir::NormalizedHook,
    message: impl Into<String>,
) {
    output.diagnostics.push(HookDiagnostic {
        level,
        source: format!(
            "{} ({})",
            hook.origin.module,
            hook.origin.source_file.display()
        ),
        message: message.into(),
    });
    match level {
        "native" => output.summary.native += 1,
        "emulated" => output.summary.emulated += 1,
        "degraded" => output.summary.degraded += 1,
        "omitted" => output.summary.omitted += 1,
        _ => {}
    }
}

pub fn write_rendered_files(output: &RenderedHookOutput) -> Result<()> {
    for file in &output.files {
        match &file.contents {
            RenderedHookFileContents::Json(value) => {
                crate::fs_util::write_json_value(&file.path, value)?
            }
            RenderedHookFileContents::Text(value) => {
                crate::fs_util::write_text_file(&file.path, value)?
            }
        }
    }
    Ok(())
}

pub fn strict_mapping_error(
    hook: &super::ir::NormalizedHook,
    target: HarnessTarget,
    reason: &str,
) -> AgentpackError {
    AgentpackError::Staging(format!(
        "hook {} from {} cannot be rendered safely for {:?}: {}",
        hook.event.as_claude_str(),
        hook.origin.source_file.display(),
        target,
        reason
    ))
}

/// Serialize a handler to a JSON object with `"type"` injected, respecting `skip_serializing_if`.
/// Optionally merges `raw_extra` from the hook.
pub fn handler_to_json_object(hook: &NormalizedHook, include_raw_extra: bool) -> Value {
    let (kind, inner) = match &hook.handler {
        ClaudeHandler::Command(h) => ("command", serde_json::to_value(h)),
        ClaudeHandler::Http(h) => ("http", serde_json::to_value(h)),
        ClaudeHandler::Prompt(h) => ("prompt", serde_json::to_value(h)),
        ClaudeHandler::Agent(h) => ("agent", serde_json::to_value(h)),
    };
    let mut obj = inner
        .ok()
        .and_then(|v| v.as_object().cloned())
        .unwrap_or_default();
    obj.insert("type".to_string(), Value::String(kind.to_string()));
    if include_raw_extra {
        for (key, value) in &hook.raw_extra {
            obj.insert(key.clone(), value.clone());
        }
    }
    Value::Object(obj)
}

/// Build a `HookExecutionSpec`, write it as a `RenderedHookFile`, and return the spec path.
/// Used by Claude, Codex, and Cursor renderers to wrap hooks via `agentpack hook-exec`.
pub fn build_exec_spec_file(
    target: HarnessTarget,
    hook: &NormalizedHook,
    event: super::ir::ClaudeEvent,
    ctx: &RenderContext<'_>,
    output: &mut RenderedHookOutput,
) -> Result<PathBuf> {
    let working_dir = staged_package_root(ctx.staged_packages, &hook.origin)?.to_path_buf();
    let spec_path = spec_path_for_hook(target, ctx.target_root, hook);
    let spec = HookExecutionSpec {
        target,
        event,
        handler: hook.handler.clone(),
        working_dir,
        matcher: hook.matcher.clone(),
    };
    output.files.push(RenderedHookFile {
        path: spec_path.clone(),
        contents: RenderedHookFileContents::Json(serde_json::to_value(&spec).unwrap()),
    });
    Ok(spec_path)
}

/// Check support level, emit diagnostics, and return `Ok(true)` to continue rendering
/// or `Ok(false)` to skip this hook. Returns `Err` for strict hooks that are unsupported.
pub fn check_support(
    target: HarnessTarget,
    hook: &NormalizedHook,
    output: &mut RenderedHookOutput,
    native_msg: &str,
    emulated_msg: &str,
) -> Result<bool> {
    match target.harness().hook_support(hook.event, &hook.handler) {
        SupportLevel::Unsupported { reason } => {
            if hook.is_strict() {
                return Err(strict_mapping_error(hook, target, reason));
            }
            push_diag(output, "omitted", hook, reason);
            Ok(false)
        }
        SupportLevel::Degraded { reason } => {
            push_diag(output, "degraded", hook, reason);
            Ok(true)
        }
        SupportLevel::Native => {
            push_diag(output, "native", hook, native_msg);
            Ok(true)
        }
        SupportLevel::Emulated => {
            push_diag(output, "emulated", hook, emulated_msg);
            Ok(true)
        }
    }
}
