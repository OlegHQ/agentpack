use std::collections::BTreeMap;
use std::io::{self, Read, Write};
use std::path::{Path, PathBuf};

use anyhow::{anyhow, Context};
use serde::{Deserialize, Serialize};
use serde_json::{Map, Value};

use crate::fs_util::read_json_value;

use super::super::ir::{
    ClaudeEvent, ClaudeHandler, HookDecision, HookOutputTarget, NormalizedHookResult,
};

#[derive(Clone, Debug, Deserialize, Serialize)]
pub struct HookExecutionSpec {
    pub target: HookOutputTarget,
    pub event: ClaudeEvent,
    pub handler: ClaudeHandler,
    pub working_dir: PathBuf,
    /// Original Claude-style matcher (regex over tool names). Populated for specs that
    /// participate in host-side dispatch (Cursor emulation). `None` → unconditional fire.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub matcher: Option<String>,
}

pub fn load_spec(path: &Path) -> anyhow::Result<HookExecutionSpec> {
    let value = read_json_value(path).map_err(anyhow::Error::from)?;
    serde_json::from_value(value).context("parse hook execution spec")
}

pub fn read_stdin_bytes() -> anyhow::Result<Vec<u8>> {
    let mut bytes = Vec::new();
    io::stdin().read_to_end(&mut bytes)?;
    Ok(bytes)
}

pub fn forward_process_output(stdout: &[u8], stderr: &[u8]) -> anyhow::Result<()> {
    io::stdout().write_all(stdout)?;
    io::stdout().flush()?;
    io::stderr().write_all(stderr)?;
    io::stderr().flush()?;
    Ok(())
}

pub fn stdin_json(stdin: &[u8]) -> Option<Value> {
    if stdin.is_empty() {
        return None;
    }
    serde_json::from_slice(stdin).ok()
}

fn take_string(map: &mut Map<String, Value>, keys: &[&str]) -> Option<String> {
    for key in keys {
        if let Some(value) = map.remove(*key) {
            if let Some(value) = value.as_str() {
                return Some(value.to_string());
            }
        }
    }
    None
}

fn take_value(map: &mut Map<String, Value>, keys: &[&str]) -> Option<Value> {
    for key in keys {
        if let Some(value) = map.remove(*key) {
            return Some(value);
        }
    }
    None
}

pub fn normalize_response(value: Value) -> NormalizedHookResult {
    let mut object = value.as_object().cloned().unwrap_or_default();
    let decision = take_string(
        &mut object,
        &["decision", "permission", "permissionDecision"],
    )
    .map(|value| match value.as_str() {
        "deny" | "block" => HookDecision::Deny,
        "ask" => HookDecision::Ask,
        _ => HookDecision::Allow,
    })
    .or_else(|| {
        object
            .get("continue")
            .and_then(Value::as_bool)
            .map(|value| {
                if value {
                    HookDecision::Allow
                } else {
                    HookDecision::Deny
                }
            })
    })
    .unwrap_or(HookDecision::Allow);

    let message = take_string(
        &mut object,
        &[
            "message",
            "permissionDecisionReason",
            "user_message",
            "agent_message",
            "stopReason",
        ],
    );
    let additional_context = take_string(&mut object, &["additional_context", "additionalContext"]);
    let updated_input = take_value(&mut object, &["updated_input", "updatedInput"]);
    let updated_tool_output = take_value(
        &mut object,
        &[
            "updated_tool_output",
            "updatedToolOutput",
            "updated_mcp_tool_output",
            "updatedMCPToolOutput",
        ],
    );
    let metadata = object.into_iter().collect::<BTreeMap<_, _>>();

    NormalizedHookResult {
        decision,
        message,
        additional_context,
        updated_input,
        updated_tool_output,
        metadata,
    }
}

pub fn write_json_stdout(value: &Value) -> anyhow::Result<()> {
    serde_json::to_writer(io::stdout(), value)?;
    io::stdout().write_all(b"\n")?;
    Ok(())
}

pub fn extract_json_object(text: &str) -> anyhow::Result<Value> {
    if let Ok(value) = serde_json::from_str(text) {
        return Ok(value);
    }
    let start = text
        .find('{')
        .ok_or_else(|| anyhow!("no JSON object found"))?;
    let end = text
        .rfind('}')
        .ok_or_else(|| anyhow!("no JSON object found"))?;
    serde_json::from_str(&text[start..=end]).context("parse JSON object from command output")
}
