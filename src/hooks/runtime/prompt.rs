use std::process::Command;

use anyhow::{anyhow, Context};

use super::bridge::{extract_json_object, normalize_response, stdin_json};
use crate::hooks::ir::{ClaudeHandler, NormalizedHookResult};
use crate::hooks::runtime::bridge::HookExecutionSpec;

pub fn execute(
    spec: &HookExecutionSpec,
    stdin_bytes: &[u8],
) -> anyhow::Result<NormalizedHookResult> {
    let ClaudeHandler::Prompt(handler) = &spec.handler else {
        return Err(anyhow!("prompt executor received non-prompt hook"));
    };
    let stdin_json = stdin_json(stdin_bytes).unwrap_or(serde_json::Value::Null);
    let prompt = format!(
        "You are executing an agentpack prompt hook. Return ONLY JSON with keys decision, message, additional_context, updated_input, updated_tool_output.\n\nAuthor prompt:\n{}\n\nHook input JSON:\n{}",
        handler.prompt,
        serde_json::to_string_pretty(&stdin_json)?
    );
    run_codex_exec(&prompt, handler.model.as_deref(), &spec.working_dir)
}

pub(crate) fn run_codex_exec(
    prompt: &str,
    model: Option<&str>,
    cwd: &std::path::Path,
) -> anyhow::Result<NormalizedHookResult> {
    let codex = std::env::var("CODEX_PATH").unwrap_or_else(|_| "codex".to_string());
    let mut cmd = Command::new(codex);
    cmd.arg("exec").arg("--ephemeral").arg("-C").arg(cwd);
    if let Some(model) = model {
        cmd.arg("--model").arg(model);
    }
    cmd.arg(prompt);
    let output = cmd.output().context("spawn codex exec for hook prompt")?;
    if !output.status.success() {
        return Err(anyhow!(
            "codex exec failed: {}",
            String::from_utf8_lossy(&output.stderr)
        ));
    }
    let parsed = extract_json_object(&String::from_utf8_lossy(&output.stdout))?;
    Ok(normalize_response(parsed))
}
