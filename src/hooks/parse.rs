//! Parse hook-config files into the normalized [`NormalizedHook`] IR.
//!
//! These are **input-format** parsers for the shared `collect` pipeline — not per-harness logic.
//! `collect` picks a parser by the *source format* of the file, not by the harness being targeted:
//! every pack plugin / bare skill / `.agents` tree authors hooks in the canonical nested format
//! ([`parse_nested_hooks`]) and is parsed once, then fanned out to all harness renderers; only the
//! user's seeded native Codex `hooks.json` uses the Codex CLI's own format ([`parse_codex_hooks`]).

use std::collections::BTreeMap;
use std::io::{self, ErrorKind};
use std::path::Path;

use serde_json::{Map, Value};

use crate::error::{AgentpackError, Result};

use super::ir::{
    AgentHandler, ClaudeEvent, ClaudeHandler, CommandHandler, HookBundle, HookOrigin, HttpHandler,
    NormalizedHook, PromptHandler,
};

/// Parse the canonical nested hook format used by all pack content and `.agents`:
/// `{ "hooks": { <event>: [ { "matcher"?, "hooks": [ <handler>, … ] }, … ] } }`.
pub fn parse_nested_hooks(
    path: &Path,
    value: &Value,
    base_origin: &HookOrigin,
) -> Result<HookBundle> {
    let hooks_object = value
        .get("hooks")
        .and_then(Value::as_object)
        .ok_or_else(|| invalid(path, "hook file must contain a top-level hooks object"))?;

    let mut hooks = Vec::new();
    for (event_index, (event_name, groups)) in hooks_object.iter().enumerate() {
        let event = ClaudeEvent::from_any_str(event_name)
            .ok_or_else(|| invalid(path, format!("unknown hook event `{event_name}`")))?;
        let groups = groups
            .as_array()
            .ok_or_else(|| invalid(path, "event groups must be arrays"))?;
        for (matcher_group_index, group) in groups.iter().enumerate() {
            let group_obj = group
                .as_object()
                .ok_or_else(|| invalid(path, "matcher groups must be objects"))?;
            let matcher = group_obj
                .get("matcher")
                .and_then(Value::as_str)
                .map(ToOwned::to_owned);
            let group_extra = object_extra(group_obj, &["matcher", "hooks"]);
            let group_hooks = group_obj
                .get("hooks")
                .and_then(Value::as_array)
                .ok_or_else(|| invalid(path, "matcher groups must contain hooks arrays"))?;
            for (hook_index, handler_value) in group_hooks.iter().enumerate() {
                let mut origin = base_origin.clone();
                origin.event_index = event_index;
                origin.matcher_group_index = matcher_group_index;
                origin.hook_index = hook_index;
                hooks.push(build_hook(
                    event,
                    matcher.clone(),
                    handler_value,
                    origin,
                    group_extra.clone(),
                    path,
                )?);
            }
        }
    }

    Ok(HookBundle { hooks })
}

/// Parse the Codex CLI's native `hooks.json` (the seeded user config). Like the nested format but
/// also accepts entries that are handlers directly (no inner `hooks` array).
pub fn parse_codex_hooks(
    path: &Path,
    value: &Value,
    base_origin: &HookOrigin,
) -> Result<HookBundle> {
    let hooks_root = if let Some(hooks) = value.get("hooks") {
        hooks
    } else {
        value
    };
    let hooks_object = hooks_root
        .as_object()
        .ok_or_else(|| invalid(path, "Codex hooks file must be an object"))?;

    let mut hooks = Vec::new();
    for (event_index, (event_name, entries)) in hooks_object.iter().enumerate() {
        let event = ClaudeEvent::from_any_str(event_name)
            .ok_or_else(|| invalid(path, format!("unknown Codex hook event `{event_name}`")))?;
        let entries = entries
            .as_array()
            .ok_or_else(|| invalid(path, "Codex hook event entries must be arrays"))?;
        for (matcher_group_index, entry) in entries.iter().enumerate() {
            let entry_obj = entry
                .as_object()
                .ok_or_else(|| invalid(path, "Codex hook entries must be objects"))?;
            if let Some(group_hooks) = entry_obj.get("hooks").and_then(Value::as_array) {
                let matcher = entry_obj
                    .get("matcher")
                    .and_then(Value::as_str)
                    .map(ToOwned::to_owned);
                let group_extra = object_extra(entry_obj, &["matcher", "hooks"]);
                for (hook_index, handler_value) in group_hooks.iter().enumerate() {
                    let mut origin = base_origin.clone();
                    origin.event_index = event_index;
                    origin.matcher_group_index = matcher_group_index;
                    origin.hook_index = hook_index;
                    hooks.push(build_hook(
                        event,
                        matcher.clone(),
                        handler_value,
                        origin,
                        group_extra.clone(),
                        path,
                    )?);
                }
                continue;
            }

            let matcher = entry_obj
                .get("matcher")
                .and_then(Value::as_str)
                .map(ToOwned::to_owned);
            let group_extra = object_extra(entry_obj, &["matcher"]);
            let mut origin = base_origin.clone();
            origin.event_index = event_index;
            origin.matcher_group_index = matcher_group_index;
            origin.hook_index = 0;
            hooks.push(build_hook(
                event,
                matcher,
                entry,
                origin,
                group_extra,
                path,
            )?);
        }
    }

    Ok(HookBundle { hooks })
}

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
