use serde::{Deserialize, Serialize};
use serde_json::Value;

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct AnthropicErrorBody {
    pub r#type: String,
    pub error: AnthropicError,
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct AnthropicError {
    pub r#type: String,
    pub message: String,
}

pub fn anthropic_error_body(error_type: impl Into<String>, message: impl Into<String>) -> Value {
    serde_json::json!({
        "type": "error",
        "error": {
            "type": error_type.into(),
            "message": message.into(),
        }
    })
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
pub struct AnthropicUsage {
    pub input_tokens: u64,
    pub output_tokens: u64,
    pub cache_creation_input_tokens: u64,
    pub cache_read_input_tokens: u64,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub server_tool_use: Option<AnthropicServerToolUse>,
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct AnthropicServerToolUse {
    #[serde(skip_serializing_if = "Option::is_none")]
    pub web_search_requests: Option<u64>,
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct AnthropicMessageResponse {
    pub id: String,
    pub r#type: String,
    pub role: String,
    pub model: String,
    pub content: Vec<Value>,
    pub stop_reason: Option<String>,
    pub stop_sequence: Option<Value>,
    pub usage: AnthropicUsage,
}
