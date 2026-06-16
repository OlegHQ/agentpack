use crate::anthropic::AnthropicMessageResponse;
use crate::codex::stream::{
    map_usage_to_anthropic, reduce_codex_sse, CodexReducerEvent, CodexStreamError, CodexUsage,
};
use serde_json::{json, Value};
use std::collections::BTreeMap;

#[derive(Debug, Clone, PartialEq)]
pub struct AccumulatedResponse {
    pub response: AnthropicMessageResponse,
    pub raw_usage: Option<CodexUsage>,
    pub terminal_type: Option<String>,
    pub continuation_eligible: bool,
    pub response_id: Option<String>,
    pub output_items: Vec<Value>,
}

#[derive(Debug, Clone)]
enum Block {
    Text {
        text: String,
    },
    Tool {
        id: String,
        name: String,
        args: String,
    },
}

pub fn accumulate_codex_response(
    input: &[u8],
    message_id: &str,
    model: &str,
) -> Result<AccumulatedResponse, CodexStreamError> {
    let events = reduce_codex_sse(input)?;
    let mut blocks: BTreeMap<u64, Block> = BTreeMap::new();
    let mut stop_reason = None;
    let mut raw_usage = None;
    let mut terminal_type = None;
    let mut continuation_eligible = false;
    let mut response_id = None;
    let mut output_items = Vec::new();
    let mut web_search_requests = 0;

    for event in events {
        match event {
            CodexReducerEvent::TextStart { index } => {
                blocks.insert(
                    index,
                    Block::Text {
                        text: String::new(),
                    },
                );
            }
            CodexReducerEvent::TextDelta { index, text } => {
                if let Some(Block::Text { text: acc }) = blocks.get_mut(&index) {
                    acc.push_str(&text);
                }
            }
            CodexReducerEvent::ToolStart { index, id, name } => {
                blocks.insert(
                    index,
                    Block::Tool {
                        id,
                        name,
                        args: String::new(),
                    },
                );
            }
            CodexReducerEvent::ToolDelta {
                index,
                partial_json,
            } => {
                if let Some(Block::Tool { args, .. }) = blocks.get_mut(&index) {
                    args.push_str(&partial_json);
                }
            }
            CodexReducerEvent::Finish {
                stop_reason: reason,
                terminal_type: done_type,
                continuation_eligible: eligible,
                usage,
                web_search_requests: searches,
                response_id: id,
                output_items: items,
            } => {
                stop_reason = Some(reason);
                terminal_type = Some(done_type);
                continuation_eligible = eligible;
                raw_usage = usage;
                response_id = id;
                output_items = items;
                web_search_requests = searches;
            }
            _ => {}
        }
    }

    let mut content = Vec::new();
    for (_, block) in blocks {
        match block {
            Block::Text { text } if !text.is_empty() => {
                content.push(json!({ "type": "text", "text": text }));
            }
            Block::Text { .. } => {}
            Block::Tool { id, name, args } => {
                content.push(json!({
                    "type": "tool_use",
                    "id": id,
                    "name": name,
                    "input": parse_tool_input_json_or_raw(&args)
                }));
            }
        }
    }

    Ok(AccumulatedResponse {
        response: AnthropicMessageResponse {
            id: message_id.to_string(),
            r#type: "message".to_string(),
            role: "assistant".to_string(),
            model: model.to_string(),
            content,
            stop_reason,
            stop_sequence: None,
            usage: map_usage_to_anthropic(raw_usage.as_ref(), web_search_requests),
        },
        raw_usage,
        terminal_type,
        continuation_eligible,
        response_id,
        output_items,
    })
}

fn parse_tool_input_json_or_raw(args: &str) -> Value {
    if args.is_empty() {
        return json!({});
    }
    serde_json::from_str(args).unwrap_or_else(|_| json!({ "_raw": args }))
}
