use serde::{Deserialize, Serialize};
use serde_json::Value;

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct AnthropicTextBlock {
    pub r#type: String,
    pub text: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub cache_control: Option<Value>,
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
#[serde(tag = "type", rename_all = "lowercase")]
pub enum AnthropicImageSource {
    Base64 { media_type: String, data: String },
    Url { url: String },
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct AnthropicImageBlock {
    pub r#type: String,
    pub source: AnthropicImageSource,
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct AnthropicToolUseBlock {
    pub r#type: String,
    pub id: String,
    pub name: String,
    pub input: Value,
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct AnthropicToolResultBlock {
    pub r#type: String,
    pub tool_use_id: String,
    pub content: AnthropicToolResultContent,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub is_error: Option<bool>,
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
#[serde(untagged)]
pub enum AnthropicToolResultContent {
    Text(String),
    Blocks(Vec<Value>),
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
#[serde(untagged)]
pub enum AnthropicContentBlock {
    Text(AnthropicTextBlock),
    Image(AnthropicImageBlock),
    ToolUse(AnthropicToolUseBlock),
    ToolResult(AnthropicToolResultBlock),
    Other(Value),
}

impl AnthropicContentBlock {
    pub fn block_type(&self) -> Option<&str> {
        match self {
            Self::Text(block) => Some(&block.r#type),
            Self::Image(block) => Some(&block.r#type),
            Self::ToolUse(block) => Some(&block.r#type),
            Self::ToolResult(block) => Some(&block.r#type),
            Self::Other(value) => value.get("type").and_then(Value::as_str),
        }
    }
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
#[serde(untagged)]
pub enum AnthropicMessageContent {
    Text(String),
    Blocks(Vec<AnthropicContentBlock>),
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct AnthropicMessage {
    pub role: String,
    pub content: AnthropicMessageContent,
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct AnthropicFunctionTool {
    #[serde(skip_serializing_if = "Option::is_none")]
    pub r#type: Option<String>,
    pub name: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub description: Option<String>,
    pub input_schema: Value,
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct AnthropicWebSearchTool {
    pub r#type: String,
    pub name: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub allowed_domains: Option<Vec<String>>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub blocked_domains: Option<Vec<String>>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub max_uses: Option<u64>,
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
#[serde(untagged)]
pub enum AnthropicTool {
    Function(AnthropicFunctionTool),
    WebSearch(AnthropicWebSearchTool),
}

impl AnthropicTool {
    pub fn name(&self) -> &str {
        match self {
            Self::WebSearch(tool) => &tool.name,
            Self::Function(tool) => &tool.name,
        }
    }

    pub fn tool_type(&self) -> Option<&str> {
        match self {
            Self::WebSearch(tool) => Some(&tool.r#type),
            Self::Function(tool) => tool.r#type.as_deref(),
        }
    }
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct AnthropicToolChoice {
    pub r#type: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub name: Option<String>,
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
#[serde(untagged)]
pub enum AnthropicSystem {
    Text(String),
    Blocks(Vec<AnthropicTextBlock>),
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct AnthropicOutputConfig {
    #[serde(skip_serializing_if = "Option::is_none")]
    pub effort: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub format: Option<AnthropicOutputFormat>,
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct AnthropicOutputFormat {
    pub r#type: String,
    pub schema: Value,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub name: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub strict: Option<bool>,
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct AnthropicRequest {
    pub model: String,
    pub messages: Vec<AnthropicMessage>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub system: Option<AnthropicSystem>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub tools: Option<Vec<AnthropicTool>>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub tool_choice: Option<AnthropicToolChoice>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub max_tokens: Option<u64>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub temperature: Option<f64>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub top_p: Option<f64>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub stream: Option<bool>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub thinking: Option<Value>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub output_config: Option<AnthropicOutputConfig>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub context_management: Option<Value>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub metadata: Option<Value>,
}
