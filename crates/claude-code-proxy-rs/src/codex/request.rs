use crate::anthropic::{
    AnthropicContentBlock, AnthropicImageSource, AnthropicMessageContent, AnthropicRequest,
    AnthropicSystem, AnthropicTool, AnthropicToolResultContent,
};
use serde::{Deserialize, Serialize};
use serde_json::{json, Map, Value};
use thiserror::Error;

#[derive(Debug, Error)]
pub enum TranslateError {
    #[error(
        "Invalid output_config.effort: {0}. Must be one of: none, low, medium, high, xhigh, max"
    )]
    InvalidEffort(String),
    #[error("Invalid service tier override: \"{0}\". Must be one of: fast, priority, flex")]
    InvalidServiceTier(String),
}

#[derive(Debug, Clone, Default)]
pub struct TranslateOptions {
    pub session_id: Option<String>,
    pub service_tier: Option<String>,
    pub service_tier_override: Option<String>,
    pub effort_override: Option<String>,
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct CodexResponsesRequest {
    pub model: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub instructions: Option<String>,
    pub input: Vec<CodexInputItem>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub tools: Option<Vec<CodexTool>>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub tool_choice: Option<Value>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub parallel_tool_calls: Option<bool>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub reasoning: Option<Value>,
    pub store: bool,
    pub stream: bool,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub include: Option<Vec<String>>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub service_tier: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub prompt_cache_key: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub text: Option<Value>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub client_metadata: Option<Map<String, Value>>,
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
#[serde(tag = "type")]
pub enum CodexInputItem {
    #[serde(rename = "message")]
    Message {
        role: String,
        content: Vec<CodexContentPart>,
    },
    #[serde(rename = "function_call")]
    FunctionCall {
        call_id: String,
        name: String,
        arguments: String,
    },
    #[serde(rename = "function_call_output")]
    FunctionCallOutput { call_id: String, output: String },
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
#[serde(tag = "type")]
pub enum CodexContentPart {
    #[serde(rename = "input_text")]
    InputText { text: String },
    #[serde(rename = "output_text")]
    OutputText { text: String },
    #[serde(rename = "input_image")]
    InputImage {
        image_url: String,
        #[serde(skip_serializing_if = "Option::is_none")]
        detail: Option<String>,
    },
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
#[serde(tag = "type")]
pub enum CodexTool {
    #[serde(rename = "function")]
    Function {
        name: String,
        #[serde(skip_serializing_if = "Option::is_none")]
        description: Option<String>,
        parameters: Value,
        #[serde(skip_serializing_if = "Option::is_none")]
        strict: Option<bool>,
    },
    #[serde(rename = "web_search")]
    WebSearch {
        external_web_access: bool,
        search_content_types: Vec<String>,
        #[serde(skip_serializing_if = "Option::is_none")]
        filters: Option<Value>,
    },
}

pub fn translate_anthropic_to_codex(
    request: &AnthropicRequest,
    opts: TranslateOptions,
) -> Result<CodexResponsesRequest, TranslateError> {
    let instructions = flatten_system_text(request.system.as_ref()).unwrap_or_default();
    let input = build_input(request);
    let tools = request
        .tools
        .as_ref()
        .map(|tools| tools.iter().map(to_codex_tool).collect::<Vec<CodexTool>>());

    let mut text = json!({ "verbosity": "low" });
    if let Some(format) = request
        .output_config
        .as_ref()
        .and_then(|output| output.format.as_ref())
        .filter(|format| format.r#type == "json_schema")
    {
        text["format"] = json!({
            "type": "json_schema",
            "name": format.name.as_deref().unwrap_or("response"),
            "schema": normalize_strict_json_schema(&format.schema),
            "strict": true
        });
    }

    let mut out = CodexResponsesRequest {
        model: request.model.clone(),
        instructions: Some(instructions),
        input,
        tools: tools.filter(|tools| !tools.is_empty()),
        tool_choice: Some(map_tool_choice(
            request
                .tool_choice
                .as_ref()
                .map(|choice| (&choice.r#type, choice.name.as_deref())),
            request.tools.as_deref(),
        )),
        parallel_tool_calls: Some(true),
        reasoning: None,
        store: false,
        stream: true,
        include: None,
        service_tier: resolve_service_tier(
            opts.service_tier.as_deref(),
            opts.service_tier_override.as_deref(),
        )?,
        prompt_cache_key: opts.session_id,
        text: Some(text),
        client_metadata: None,
    };

    let effort = request
        .output_config
        .as_ref()
        .and_then(|output| output.effort.as_deref());
    assert_valid_effort(effort)?;
    let effort = resolve_effort(effort, opts.effort_override.as_deref())?;
    if let Some(effort) = effort {
        out.reasoning = Some(json!({ "effort": effort }));
        out.include = Some(vec!["reasoning.encrypted_content".to_string()]);
    }

    Ok(out)
}

pub fn normalize_strict_json_schema(schema: &Value) -> Value {
    match schema {
        Value::Array(items) => {
            Value::Array(items.iter().map(normalize_strict_json_schema).collect())
        }
        Value::Object(map) => {
            let mut out = Map::new();
            for (key, value) in map {
                out.insert(key.clone(), normalize_strict_json_schema(value));
            }
            if let Some(Value::Object(properties)) = out.get("properties") {
                out.insert(
                    "required".to_string(),
                    Value::Array(properties.keys().cloned().map(Value::String).collect()),
                );
            }
            Value::Object(out)
        }
        other => other.clone(),
    }
}

fn assert_valid_effort(effort: Option<&str>) -> Result<(), TranslateError> {
    match effort {
        None | Some("none" | "low" | "medium" | "high" | "xhigh" | "max") => Ok(()),
        Some(value) => Err(TranslateError::InvalidEffort(
            serde_json::to_string(value).unwrap(),
        )),
    }
}

fn resolve_effort(
    effort: Option<&str>,
    override_effort: Option<&str>,
) -> Result<Option<String>, TranslateError> {
    let selected = override_effort.or(effort);
    match selected {
        None => Ok(None),
        Some("none" | "low" | "medium" | "high" | "xhigh") => Ok(selected.map(str::to_string)),
        Some("max") => Ok(Some("xhigh".to_string())),
        Some(value) => Err(TranslateError::InvalidEffort(
            serde_json::to_string(value).unwrap(),
        )),
    }
}

fn resolve_service_tier(
    model_service_tier: Option<&str>,
    override_tier: Option<&str>,
) -> Result<Option<String>, TranslateError> {
    match override_tier.or(model_service_tier) {
        None => Ok(None),
        Some("fast" | "priority") => Ok(Some("priority".to_string())),
        Some("flex") => Ok(Some("flex".to_string())),
        Some(value) => Err(TranslateError::InvalidServiceTier(value.to_string())),
    }
}

fn flatten_system_text(system: Option<&AnthropicSystem>) -> Option<String> {
    let texts: Vec<String> = match system {
        None => Vec::new(),
        Some(AnthropicSystem::Text(text)) => vec![text.clone()],
        Some(AnthropicSystem::Blocks(blocks)) => blocks
            .iter()
            .filter(|block| block.r#type == "text")
            .map(|block| block.text.clone())
            .collect(),
    }
    .into_iter()
    .filter(|text| !text.starts_with("x-anthropic-billing-header:"))
    .collect();
    (!texts.is_empty()).then(|| texts.join("\n\n"))
}

fn normalize_content(content: &AnthropicMessageContent) -> Vec<AnthropicContentBlock> {
    match content {
        AnthropicMessageContent::Text(text) => vec![AnthropicContentBlock::Text(
            crate::anthropic::AnthropicTextBlock {
                r#type: "text".to_string(),
                text: text.clone(),
                cache_control: None,
            },
        )],
        AnthropicMessageContent::Blocks(blocks) => blocks.clone(),
    }
}

fn image_block_to_url(block: &crate::anthropic::AnthropicImageBlock) -> String {
    match &block.source {
        AnthropicImageSource::Url { url } => url.clone(),
        AnthropicImageSource::Base64 { media_type, data } => {
            format!("data:{media_type};base64,{data}")
        }
    }
}

fn build_input(request: &AnthropicRequest) -> Vec<CodexInputItem> {
    let mut out = Vec::new();
    for message in &request.messages {
        let blocks = normalize_content(&message.content);
        if message.role == "user" {
            let mut parts = Vec::new();
            for block in blocks {
                match block {
                    AnthropicContentBlock::Text(block) if block.r#type == "text" => {
                        parts.push(CodexContentPart::InputText { text: block.text });
                    }
                    AnthropicContentBlock::Image(block) if block.r#type == "image" => {
                        parts.push(CodexContentPart::InputImage {
                            image_url: image_block_to_url(&block),
                            detail: None,
                        });
                    }
                    AnthropicContentBlock::ToolResult(block) if block.r#type == "tool_result" => {
                        if !parts.is_empty() {
                            out.push(CodexInputItem::Message {
                                role: "user".to_string(),
                                content: std::mem::take(&mut parts),
                            });
                        }
                        let body = tool_result_to_string(&block.content);
                        let output = if block.is_error == Some(true) {
                            format!("[tool execution error]\n{body}")
                        } else {
                            body
                        };
                        out.push(CodexInputItem::FunctionCallOutput {
                            call_id: block.tool_use_id,
                            output,
                        });
                    }
                    _ => {}
                }
            }
            if !parts.is_empty() {
                out.push(CodexInputItem::Message {
                    role: "user".to_string(),
                    content: parts,
                });
            }
        } else if message.role == "system" {
            let parts = blocks
                .into_iter()
                .filter_map(|block| match block {
                    AnthropicContentBlock::Text(block) if block.r#type == "text" => {
                        Some(CodexContentPart::InputText { text: block.text })
                    }
                    _ => None,
                })
                .collect::<Vec<_>>();
            if !parts.is_empty() {
                out.push(CodexInputItem::Message {
                    role: "developer".to_string(),
                    content: parts,
                });
            }
        } else {
            let mut text_parts = Vec::new();
            for block in blocks {
                match block {
                    AnthropicContentBlock::Text(block) if block.r#type == "text" => {
                        text_parts.push(CodexContentPart::OutputText { text: block.text });
                    }
                    AnthropicContentBlock::ToolUse(block) if block.r#type == "tool_use" => {
                        if !text_parts.is_empty() {
                            out.push(CodexInputItem::Message {
                                role: "assistant".to_string(),
                                content: std::mem::take(&mut text_parts),
                            });
                        }
                        out.push(CodexInputItem::FunctionCall {
                            call_id: block.id,
                            name: block.name,
                            arguments: serde_json::to_string(&block.input)
                                .unwrap_or_else(|_| "{}".to_string()),
                        });
                    }
                    _ => {}
                }
            }
            if !text_parts.is_empty() {
                out.push(CodexInputItem::Message {
                    role: "assistant".to_string(),
                    content: text_parts,
                });
            }
        }
    }
    out
}

pub fn tool_result_to_string(content: &AnthropicToolResultContent) -> String {
    match content {
        AnthropicToolResultContent::Text(text) => text.clone(),
        AnthropicToolResultContent::Blocks(blocks) => blocks
            .iter()
            .map(|block| {
                if block.get("type").and_then(Value::as_str) == Some("text") {
                    if let Some(text) = block.get("text").and_then(Value::as_str) {
                        return text.to_string();
                    }
                }
                if block.get("type").and_then(Value::as_str) == Some("image") {
                    if let Some(source) = block.get("source").and_then(Value::as_object) {
                        if source.get("type").and_then(Value::as_str) == Some("url") {
                            return "[image omitted: url]".to_string();
                        }
                        if source.get("type").and_then(Value::as_str) == Some("base64") {
                            if let Some(media_type) =
                                source.get("media_type").and_then(Value::as_str)
                            {
                                return format!("[image omitted: {media_type}]");
                            }
                        }
                    }
                }
                let block_type = block
                    .get("type")
                    .and_then(Value::as_str)
                    .unwrap_or("unknown");
                format!("[unsupported content block omitted: {block_type}]")
            })
            .collect::<Vec<_>>()
            .join("\n"),
    }
}

fn map_tool_choice(
    choice: Option<(&String, Option<&str>)>,
    tools: Option<&[AnthropicTool]>,
) -> Value {
    match choice {
        None => json!("auto"),
        Some((kind, _)) if kind == "auto" => json!("auto"),
        Some((kind, _)) if kind == "none" => json!("none"),
        Some((kind, _)) if kind == "any" => json!("required"),
        Some((kind, name)) if kind == "tool" => {
            if let Some(name) = name {
                if tools.unwrap_or_default().iter().any(|tool| {
                    tool.tool_type() == Some("web_search_20250305") && tool.name() == name
                }) {
                    json!({ "type": "web_search" })
                } else {
                    json!({ "type": "function", "name": name })
                }
            } else {
                json!("required")
            }
        }
        _ => json!("auto"),
    }
}

fn to_codex_tool(tool: &AnthropicTool) -> CodexTool {
    match tool {
        AnthropicTool::WebSearch(tool) if tool.r#type == "web_search_20250305" => {
            let mut filters = Map::new();
            if let Some(domains) = tool
                .allowed_domains
                .as_ref()
                .filter(|domains| !domains.is_empty())
            {
                filters.insert("allowed_domains".to_string(), json!(domains));
            }
            if let Some(domains) = tool
                .blocked_domains
                .as_ref()
                .filter(|domains| !domains.is_empty())
            {
                filters.insert("blocked_domains".to_string(), json!(domains));
            }
            CodexTool::WebSearch {
                external_web_access: false,
                search_content_types: vec!["text".to_string(), "image".to_string()],
                filters: (!filters.is_empty()).then_some(Value::Object(filters)),
            }
        }
        AnthropicTool::WebSearch(tool) => CodexTool::Function {
            name: tool.name.clone(),
            description: None,
            parameters: json!({}),
            strict: None,
        },
        AnthropicTool::Function(tool) => CodexTool::Function {
            name: tool.name.clone(),
            description: tool.description.clone(),
            parameters: tool.input_schema.clone(),
            strict: None,
        },
    }
}
