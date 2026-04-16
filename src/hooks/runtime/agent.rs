use anyhow::{anyhow, Context};

use crate::hooks::ir::{ClaudeHandler, NormalizedHookResult};
use crate::hooks::runtime::bridge::{stdin_json, HookExecutionSpec};

use super::prompt::run_codex_exec;

pub fn execute(
    spec: &HookExecutionSpec,
    stdin_bytes: &[u8],
) -> anyhow::Result<NormalizedHookResult> {
    let ClaudeHandler::Agent(handler) = &spec.handler else {
        return Err(anyhow!("agent executor received non-agent hook"));
    };
    let input = stdin_json(stdin_bytes).unwrap_or(serde_json::Value::Null);
    let prompt = format!(
        "You are executing an agentpack agent hook{}.\nReturn ONLY JSON with keys decision, message, additional_context, updated_input, updated_tool_output.\n\nHook instructions:\n{}\n\nHook input JSON:\n{}",
        handler
            .agent
            .as_deref()
            .map(|agent| format!(" for agent `{agent}`"))
            .unwrap_or_default(),
        handler.prompt,
        serde_json::to_string_pretty(&input).context("serialize hook input")?
    );
    run_codex_exec(&prompt, handler.model.as_deref(), &spec.working_dir)
}
