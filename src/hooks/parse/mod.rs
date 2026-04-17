mod claude;
mod codex;
mod cursor;

use std::collections::BTreeMap;
use std::io::{self, ErrorKind};
use std::path::Path;

use serde_json::{Map, Value};

use crate::error::{AgentpackError, Result};

use super::ir::{
    AgentHandler, ClaudeEvent, ClaudeHandler, CommandHandler, HookOrigin, HttpHandler,
    NormalizedHook, PromptHandler,
};

pub use claude::parse_claude_hooks;
pub use codex::parse_codex_hooks;
pub use cursor::parse_cursor_hooks;

fn invalid(path: &Path, message: impl Into<String>) -> AgentpackError {
    AgentpackError::io(path, io::Error::new(ErrorKind::InvalidData, message.into()))
}

fn object_extra(object: &Map<String, Value>, excluded: &[&str]) -> BTreeMap<String, Value> {
    object
        .iter()
        .filter(|(key, _)| !excluded.iter().any(|candidate| candidate == &key.as_str()))
        .map(|(key, value)| (key.clone(), value.clone()))
        .collect()
}

fn parse_handler(path: &Path, value: &Value) -> Result<(ClaudeHandler, BTreeMap<String, Value>)> {
    let object = value
        .as_object()
        .ok_or_else(|| invalid(path, "hook entries must be JSON objects"))?;
    let hook_type = object
        .get("type")
        .and_then(Value::as_str)
        .ok_or_else(|| invalid(path, "hook entries must include type"))?;
    let handler = match hook_type {
        "command" => {
            let command = object
                .get("command")
                .and_then(Value::as_str)
                .ok_or_else(|| invalid(path, "command hook missing command"))?;
            let timeout_secs = object.get("timeout").and_then(Value::as_u64);
            ClaudeHandler::Command(CommandHandler {
                command: command.to_string(),
                timeout_secs,
            })
        }
        "http" => {
            let url = object
                .get("url")
                .and_then(Value::as_str)
                .ok_or_else(|| invalid(path, "http hook missing url"))?;
            let method = object
                .get("method")
                .and_then(Value::as_str)
                .map(ToOwned::to_owned);
            let headers = object
                .get("headers")
                .and_then(Value::as_object)
                .map(|headers| {
                    headers
                        .iter()
                        .filter_map(|(key, value)| {
                            value.as_str().map(|value| (key.clone(), value.to_string()))
                        })
                        .collect::<BTreeMap<_, _>>()
                })
                .unwrap_or_default();
            ClaudeHandler::Http(HttpHandler {
                url: url.to_string(),
                method,
                headers,
                body: object.get("body").cloned(),
            })
        }
        "prompt" => {
            let prompt = object
                .get("prompt")
                .and_then(Value::as_str)
                .ok_or_else(|| invalid(path, "prompt hook missing prompt"))?;
            ClaudeHandler::Prompt(PromptHandler {
                prompt: prompt.to_string(),
                model: object
                    .get("model")
                    .and_then(Value::as_str)
                    .map(ToOwned::to_owned),
            })
        }
        "agent" => {
            let prompt = object
                .get("prompt")
                .or_else(|| object.get("instruction"))
                .and_then(Value::as_str)
                .ok_or_else(|| invalid(path, "agent hook missing prompt"))?;
            ClaudeHandler::Agent(AgentHandler {
                prompt: prompt.to_string(),
                agent: object
                    .get("agent")
                    .and_then(Value::as_str)
                    .map(ToOwned::to_owned),
                model: object
                    .get("model")
                    .and_then(Value::as_str)
                    .map(ToOwned::to_owned),
            })
        }
        other => {
            return Err(invalid(
                path,
                format!("unsupported Claude hook type `{other}`"),
            ))
        }
    };
    let extra = match hook_type {
        "command" => object_extra(object, &["type", "command", "timeout"]),
        "http" => object_extra(object, &["type", "url", "method", "headers", "body"]),
        "prompt" => object_extra(object, &["type", "prompt", "model"]),
        "agent" => object_extra(object, &["type", "prompt", "instruction", "agent", "model"]),
        _ => BTreeMap::new(),
    };
    Ok((handler, extra))
}

fn build_hook(
    event: ClaudeEvent,
    matcher: Option<String>,
    handler_value: &Value,
    origin: HookOrigin,
    matcher_group_extra: BTreeMap<String, Value>,
    path: &Path,
) -> Result<NormalizedHook> {
    let (handler, raw_extra) = parse_handler(path, handler_value)?;
    Ok(NormalizedHook {
        event,
        matcher,
        handler,
        origin,
        matcher_group_extra,
        raw_extra,
    })
}
