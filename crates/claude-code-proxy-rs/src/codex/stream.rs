use crate::anthropic::{emit_message_start, emit_ping};
use crate::anthropic::{AnthropicServerToolUse, AnthropicUsage};
use crate::sse::{encode_sse_event, parse_sse_bytes, SseError};
use bytes::Bytes;
use serde::{Deserialize, Serialize};
use serde_json::{json, Value};
use std::collections::BTreeMap;
use thiserror::Error;

#[derive(Debug, Error)]
pub enum CodexStreamError {
    #[error(transparent)]
    Sse(#[from] SseError),
    #[error("rate limit reached")]
    RateLimit,
    #[error("{0}")]
    Failed(String),
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
#[serde(tag = "kind")]
#[serde(rename_all_fields = "camelCase")]
pub enum CodexReducerEvent {
    #[serde(rename = "text-start")]
    TextStart { index: u64 },
    #[serde(rename = "text-delta")]
    TextDelta { index: u64, text: String },
    #[serde(rename = "text-stop")]
    TextStop { index: u64 },
    #[serde(rename = "tool-start")]
    ToolStart {
        index: u64,
        id: String,
        name: String,
    },
    #[serde(rename = "tool-delta")]
    ToolDelta { index: u64, partial_json: String },
    #[serde(rename = "tool-stop")]
    ToolStop { index: u64 },
    #[serde(rename = "tool-progress")]
    ToolProgress { index: u64 },
    #[serde(rename = "progress")]
    Progress,
    #[serde(rename = "web-search")]
    WebSearch {
        index: u64,
        result_index: u64,
        id: String,
        query: String,
    },
    #[serde(rename = "finish")]
    Finish {
        stop_reason: String,
        terminal_type: String,
        continuation_eligible: bool,
        usage: Option<CodexUsage>,
        web_search_requests: u64,
        response_id: Option<String>,
        output_items: Vec<Value>,
    },
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct CodexUsage {
    #[serde(skip_serializing_if = "Option::is_none")]
    pub input_tokens: Option<u64>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub output_tokens: Option<u64>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub input_tokens_details: Option<CodexInputTokenDetails>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub output_tokens_details: Option<Value>,
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct CodexInputTokenDetails {
    #[serde(skip_serializing_if = "Option::is_none")]
    pub cached_tokens: Option<u64>,
}

#[derive(Debug, Clone)]
enum BlockState {
    Text {
        index: u64,
        text: String,
    },
    Tool {
        index: u64,
        call_id: String,
        name: String,
        args: String,
        emitted_args: bool,
        buffer_until_done: bool,
    },
}

pub fn reduce_codex_sse(input: &[u8]) -> Result<Vec<CodexReducerEvent>, CodexStreamError> {
    let events = parse_sse_bytes(input)?;
    let mut out = Vec::new();
    let mut blocks: BTreeMap<u64, BlockState> = BTreeMap::new();
    let mut output_items: BTreeMap<u64, Value> = BTreeMap::new();
    let mut item_id_to_output_index: BTreeMap<String, u64> = BTreeMap::new();
    let mut anthropic_index = 0_u64;
    let mut saw_tool_use = false;
    let mut saw_terminal = false;
    let mut terminal_type: Option<String> = None;
    let mut response_id: Option<String> = None;
    let mut final_usage: Option<CodexUsage> = None;
    let mut continuation_eligible = false;
    let mut incomplete = false;
    let mut web_search_requests = 0_u64;

    for event in events {
        if event.data.is_empty() {
            continue;
        }
        let payload: Value = match serde_json::from_str(&event.data) {
            Ok(payload) => payload,
            Err(_) => continue,
        };
        let event_type = payload
            .get("type")
            .and_then(Value::as_str)
            .or(event.event.as_deref())
            .unwrap_or("");

        match event_type {
            "codex.rate_limits" => {
                if payload
                    .pointer("/rate_limits/limit_reached")
                    .and_then(Value::as_bool)
                    == Some(true)
                {
                    return Err(CodexStreamError::RateLimit);
                }
                out.push(CodexReducerEvent::Progress);
            }
            "keepalive"
            | "response.web_search_call.in_progress"
            | "response.web_search_call.searching"
            | "response.web_search_call.completed" => {
                out.push(CodexReducerEvent::Progress);
            }
            "response.failed" | "response.error" | "error" => {
                let message = payload
                    .pointer("/response/error/message")
                    .or_else(|| payload.pointer("/error/message"))
                    .and_then(Value::as_str)
                    .unwrap_or("Upstream error");
                return Err(CodexStreamError::Failed(message.to_string()));
            }
            "response.output_item.added" => {
                let Some(item) = payload.get("item") else {
                    continue;
                };
                let output_index = payload
                    .get("output_index")
                    .and_then(Value::as_u64)
                    .unwrap_or(0);
                match item.get("type").and_then(Value::as_str) {
                    Some("reasoning") => {}
                    Some("web_search_call") => out.push(CodexReducerEvent::Progress),
                    Some("message") => {
                        let index = anthropic_index;
                        anthropic_index += 1;
                        if let Some(id) = item.get("id").and_then(Value::as_str) {
                            item_id_to_output_index.insert(id.to_string(), output_index);
                        }
                        blocks.insert(
                            output_index,
                            BlockState::Text {
                                index,
                                text: String::new(),
                            },
                        );
                        out.push(CodexReducerEvent::TextStart { index });
                    }
                    Some("function_call") => {
                        saw_tool_use = true;
                        let index = anthropic_index;
                        anthropic_index += 1;
                        let call_id = item
                            .get("call_id")
                            .and_then(Value::as_str)
                            .unwrap_or_default()
                            .to_string();
                        let name = item
                            .get("name")
                            .and_then(Value::as_str)
                            .unwrap_or_default()
                            .to_string();
                        let buffer_until_done = name == "Read";
                        blocks.insert(
                            output_index,
                            BlockState::Tool {
                                index,
                                call_id: call_id.clone(),
                                name: name.clone(),
                                args: String::new(),
                                emitted_args: false,
                                buffer_until_done,
                            },
                        );
                        out.push(CodexReducerEvent::ToolStart {
                            index,
                            id: call_id,
                            name,
                        });
                    }
                    _ => {}
                }
            }
            "response.output_text.delta" => {
                let state = state_for_text_delta(&mut blocks, &item_id_to_output_index, &payload);
                if let Some(BlockState::Text { index, text }) = state {
                    let delta = payload.get("delta").and_then(Value::as_str).unwrap_or("");
                    if !delta.is_empty() {
                        text.push_str(delta);
                        out.push(CodexReducerEvent::TextDelta {
                            index: *index,
                            text: delta.to_string(),
                        });
                    }
                }
            }
            "response.function_call_arguments.delta" => {
                let output_index = payload
                    .get("output_index")
                    .and_then(Value::as_u64)
                    .unwrap_or(0);
                if let Some(BlockState::Tool {
                    index,
                    args,
                    emitted_args,
                    buffer_until_done,
                    ..
                }) = blocks.get_mut(&output_index)
                {
                    let delta = payload.get("delta").and_then(Value::as_str).unwrap_or("");
                    if !delta.is_empty() {
                        args.push_str(delta);
                        if *buffer_until_done {
                            out.push(CodexReducerEvent::ToolProgress { index: *index });
                        } else {
                            *emitted_args = true;
                            out.push(CodexReducerEvent::ToolDelta {
                                index: *index,
                                partial_json: delta.to_string(),
                            });
                        }
                    }
                }
            }
            "response.function_call_arguments.done" => {
                let output_index = payload
                    .get("output_index")
                    .and_then(Value::as_u64)
                    .unwrap_or(0);
                if let Some(BlockState::Tool { args, .. }) = blocks.get_mut(&output_index) {
                    if args.is_empty() {
                        if let Some(arguments) = payload.get("arguments").and_then(Value::as_str) {
                            args.push_str(arguments);
                        }
                    }
                }
            }
            "response.output_item.done" => {
                let item = payload.get("item");
                if item
                    .and_then(|item| item.get("type"))
                    .and_then(Value::as_str)
                    == Some("web_search_call")
                {
                    let index = anthropic_index;
                    anthropic_index += 1;
                    let result_index = anthropic_index;
                    anthropic_index += 1;
                    web_search_requests += 1;
                    let id = server_tool_use_id_from_codex_web_search_id(
                        item.and_then(|item| item.get("id")).and_then(Value::as_str),
                    );
                    let query = web_search_query(item.unwrap_or(&Value::Null));
                    out.push(CodexReducerEvent::WebSearch {
                        index,
                        result_index,
                        id,
                        query,
                    });
                    continue;
                }

                let output_index = payload
                    .get("output_index")
                    .and_then(Value::as_u64)
                    .unwrap_or(0);
                let Some(mut state) = blocks.remove(&output_index) else {
                    continue;
                };
                match &mut state {
                    BlockState::Text { index, text } => {
                        if !text.is_empty() {
                            output_items.insert(
                                output_index,
                                json!({
                                    "type": "message",
                                    "role": "assistant",
                                    "content": [{ "type": "output_text", "text": text }]
                                }),
                            );
                        }
                        out.push(CodexReducerEvent::TextStop { index: *index });
                    }
                    BlockState::Tool {
                        index,
                        call_id,
                        name,
                        args,
                        emitted_args,
                        buffer_until_done,
                    } => {
                        let final_args = item
                            .and_then(|item| item.get("arguments"))
                            .and_then(Value::as_str)
                            .filter(|value| !value.is_empty())
                            .unwrap_or(args);
                        let sanitized = sanitize_tool_args(name, final_args);
                        *args = sanitized.clone();
                        if !sanitized.is_empty() && (*buffer_until_done || !*emitted_args) {
                            out.push(CodexReducerEvent::ToolDelta {
                                index: *index,
                                partial_json: sanitized.clone(),
                            });
                        }
                        output_items.insert(
                            output_index,
                            json!({
                                "type": "function_call",
                                "call_id": call_id,
                                "name": name,
                                "arguments": args
                            }),
                        );
                        out.push(CodexReducerEvent::ToolStop { index: *index });
                    }
                }
            }
            "response.completed" | "response.incomplete" | "response.done" => {
                saw_terminal = true;
                terminal_type = Some(event_type.to_string());
                response_id = payload
                    .pointer("/response/id")
                    .and_then(Value::as_str)
                    .map(str::to_string);
                final_usage = payload
                    .pointer("/response/usage")
                    .and_then(|usage| serde_json::from_value(usage.clone()).ok());
                let has_incomplete_reason = payload
                    .pointer("/response/incomplete_details/reason")
                    .is_some()
                    || payload.pointer("/response/status").and_then(Value::as_str)
                        == Some("incomplete");
                if event_type == "response.incomplete" || has_incomplete_reason {
                    incomplete = true;
                }
                continuation_eligible = (event_type == "response.completed"
                    || event_type == "response.done")
                    && !incomplete;
            }
            _ => {}
        }
    }

    if !saw_terminal || !blocks.is_empty() {
        return Err(CodexStreamError::Failed(if saw_terminal {
            "Upstream stream ended with open output blocks".to_string()
        } else {
            "Upstream stream ended without a terminal response event".to_string()
        }));
    }

    let stop_reason = if incomplete {
        "max_tokens"
    } else if saw_tool_use {
        "tool_use"
    } else {
        "end_turn"
    };
    out.push(CodexReducerEvent::Finish {
        stop_reason: stop_reason.to_string(),
        terminal_type: terminal_type.unwrap_or_else(|| "response.incomplete".to_string()),
        continuation_eligible,
        usage: final_usage,
        web_search_requests,
        response_id,
        output_items: output_items.into_values().collect(),
    });
    Ok(out)
}

fn state_for_text_delta<'a>(
    blocks: &'a mut BTreeMap<u64, BlockState>,
    item_id_to_output_index: &BTreeMap<String, u64>,
    payload: &Value,
) -> Option<&'a mut BlockState> {
    if let Some(output_index) = payload.get("output_index").and_then(Value::as_u64) {
        return blocks.get_mut(&output_index);
    }
    if let Some(item_id) = payload.get("item_id").and_then(Value::as_str) {
        if let Some(output_index) = item_id_to_output_index.get(item_id) {
            return blocks.get_mut(output_index);
        }
    }
    None
}

pub fn map_usage_to_anthropic(
    usage: Option<&CodexUsage>,
    web_search_requests: u64,
) -> AnthropicUsage {
    let input = usage.and_then(|usage| usage.input_tokens).unwrap_or(0);
    let output = usage.and_then(|usage| usage.output_tokens).unwrap_or(0);
    let cached = usage
        .and_then(|usage| usage.input_tokens_details.as_ref())
        .and_then(|details| details.cached_tokens)
        .unwrap_or(0);
    AnthropicUsage {
        input_tokens: input.saturating_sub(cached),
        output_tokens: output,
        cache_creation_input_tokens: 0,
        cache_read_input_tokens: cached,
        server_tool_use: (web_search_requests > 0).then_some(AnthropicServerToolUse {
            web_search_requests: Some(web_search_requests),
        }),
    }
}

pub fn codex_stream_to_anthropic_sse(
    input: &[u8],
    message_id: &str,
    model: &str,
) -> Result<Vec<Bytes>, CodexStreamError> {
    let events = reduce_codex_sse(input)?;
    let mut out = Vec::new();
    let mut message_started = false;
    let mut deferred = Vec::new();
    let mut searches = Vec::new();

    for event in events {
        if matches!(event, CodexReducerEvent::WebSearch { .. }) {
            searches.push(event);
            continue;
        }
        if !searches.is_empty() && is_content_event(&event) {
            deferred.push(event);
            continue;
        }
        emit_reducer_event(
            event,
            &mut out,
            &mut message_started,
            message_id,
            model,
            &searches,
            &deferred,
        );
    }
    Ok(out)
}

fn is_content_event(event: &CodexReducerEvent) -> bool {
    matches!(
        event,
        CodexReducerEvent::TextStart { .. }
            | CodexReducerEvent::TextDelta { .. }
            | CodexReducerEvent::TextStop { .. }
            | CodexReducerEvent::ToolStart { .. }
            | CodexReducerEvent::ToolDelta { .. }
            | CodexReducerEvent::ToolStop { .. }
    )
}

#[allow(clippy::too_many_arguments)]
fn emit_reducer_event(
    event: CodexReducerEvent,
    out: &mut Vec<Bytes>,
    message_started: &mut bool,
    message_id: &str,
    model: &str,
    searches: &[CodexReducerEvent],
    deferred: &[CodexReducerEvent],
) {
    let ensure_start = |out: &mut Vec<Bytes>, started: &mut bool| {
        if !*started {
            *started = true;
            out.push(emit_message_start(message_id, model));
            out.push(emit_ping());
        }
    };
    match event {
        CodexReducerEvent::TextStart { index } => {
            ensure_start(out, message_started);
            out.push(encode_sse_event(
                "content_block_start",
                &json!({ "type": "content_block_start", "index": index, "content_block": { "type": "text", "text": "" } }),
            ));
        }
        CodexReducerEvent::TextDelta { index, text } => out.push(encode_sse_event(
            "content_block_delta",
            &json!({ "type": "content_block_delta", "index": index, "delta": { "type": "text_delta", "text": text } }),
        )),
        CodexReducerEvent::TextStop { index } => out.push(encode_sse_event(
            "content_block_stop",
            &json!({ "type": "content_block_stop", "index": index }),
        )),
        CodexReducerEvent::ToolStart { index, id, name } => {
            ensure_start(out, message_started);
            out.push(encode_sse_event(
                "content_block_start",
                &json!({ "type": "content_block_start", "index": index, "content_block": { "type": "tool_use", "id": id, "name": name, "input": {} } }),
            ));
        }
        CodexReducerEvent::ToolDelta {
            index,
            partial_json,
        } => out.push(encode_sse_event(
            "content_block_delta",
            &json!({ "type": "content_block_delta", "index": index, "delta": { "type": "input_json_delta", "partial_json": partial_json } }),
        )),
        CodexReducerEvent::ToolStop { index } => out.push(encode_sse_event(
            "content_block_stop",
            &json!({ "type": "content_block_stop", "index": index }),
        )),
        CodexReducerEvent::Finish {
            stop_reason,
            usage,
            web_search_requests,
            ..
        } => {
            let _ = searches;
            let _ = deferred;
            ensure_start(out, message_started);
            out.push(encode_sse_event(
                "message_delta",
                &json!({
                    "type": "message_delta",
                    "delta": { "stop_reason": stop_reason, "stop_sequence": null },
                    "usage": map_usage_to_anthropic(usage.as_ref(), web_search_requests)
                }),
            ));
            out.push(encode_sse_event(
                "message_stop",
                &json!({ "type": "message_stop" }),
            ));
        }
        CodexReducerEvent::Progress | CodexReducerEvent::ToolProgress { .. } => {}
        CodexReducerEvent::WebSearch { .. } => {}
    }
}

pub fn server_tool_use_id_from_codex_web_search_id(id: Option<&str>) -> String {
    let suffix = id.unwrap_or("unknown");
    let sanitized = suffix
        .chars()
        .map(|ch| {
            if ch.is_ascii_alphanumeric() || ch == '_' {
                ch
            } else {
                '_'
            }
        })
        .collect::<String>();
    format!("srvtoolu_{sanitized}")
}

fn web_search_query(item: &Value) -> String {
    item.pointer("/action/query")
        .and_then(Value::as_str)
        .or_else(|| {
            item.pointer("/action/queries")
                .and_then(Value::as_array)
                .and_then(|queries| queries.iter().find_map(Value::as_str))
        })
        .unwrap_or("")
        .to_string()
}

fn sanitize_tool_args(name: &str, args: &str) -> String {
    if name != "Read" || args.is_empty() {
        return args.to_string();
    }
    let Ok(mut value) = serde_json::from_str::<Value>(args) else {
        return args.to_string();
    };
    let Some(object) = value.as_object_mut() else {
        return args.to_string();
    };
    if object.get("pages").and_then(Value::as_str) != Some("") {
        return args.to_string();
    }
    object.remove("pages");
    serde_json::to_string(&value).unwrap_or_else(|_| args.to_string())
}
