use crate::anthropic::AnthropicRequest;
use serde_json::Value;

pub trait TrafficCapture {
    fn write_json(&self, _name: &str, _value: &Value) {}
    fn write_text(&self, _name: &str, _value: &str) {}
    fn write_bytes(&self, _name: &str, _value: &[u8]) {}
    fn write_json_event(&self, _name: &str, _value: &Value) {}
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct RequestContext {
    pub req_id: String,
    pub session_id: Option<String>,
    pub session_seq: Option<u64>,
}

pub trait Provider {
    type Error;
    type MessageOutput;
    type CountTokensOutput;

    fn name(&self) -> &'static str;
    fn supported_models(&self) -> &[&'static str];
    fn handle_messages(
        &self,
        body: &AnthropicRequest,
        ctx: &RequestContext,
    ) -> Result<Self::MessageOutput, Self::Error>;
    fn handle_count_tokens(
        &self,
        body: &AnthropicRequest,
        ctx: &RequestContext,
    ) -> Result<Self::CountTokensOutput, Self::Error>;
}
