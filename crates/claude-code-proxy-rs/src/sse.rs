use bytes::Bytes;
use futures_core::Stream;
use futures_util::StreamExt;
use serde::Serialize;
use std::pin::Pin;
use thiserror::Error;

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct SseEvent {
    pub event: Option<String>,
    pub data: String,
}

#[derive(Debug, Error)]
pub enum SseError {
    #[error("stream read failed: {0}")]
    Read(String),
    #[error("utf-8 decoding failed: {0}")]
    Utf8(String),
    #[error("json encoding failed: {0}")]
    Json(#[from] serde_json::Error),
}

pub type ByteStream = Pin<Box<dyn Stream<Item = Result<Bytes, SseError>> + Send>>;

pub fn encode_sse_event(event: &str, data: &impl Serialize) -> Bytes {
    let json = serde_json::to_string(data).expect("SSE event data must serialize");
    Bytes::from(format!("event: {event}\ndata: {json}\n\n"))
}

pub fn parse_sse_bytes(input: &[u8]) -> Result<Vec<SseEvent>, SseError> {
    let text = std::str::from_utf8(input).map_err(|err| SseError::Utf8(err.to_string()))?;
    Ok(parse_sse_text(text))
}

pub fn parse_sse_text(input: &str) -> Vec<SseEvent> {
    let normalized = input.replace("\r\n", "\n");
    let mut out = Vec::new();
    for raw in normalized.split("\n\n") {
        if raw.trim().is_empty() {
            continue;
        }
        let mut event = None;
        let mut data_lines = Vec::new();
        for line in raw.lines() {
            if line.starts_with(':') {
                continue;
            }
            if let Some(rest) = line.strip_prefix("event:") {
                event = Some(rest.trim_start().to_string());
            } else if let Some(rest) = line.strip_prefix("data:") {
                data_lines.push(rest.trim_start().to_string());
            }
        }
        out.push(SseEvent {
            event,
            data: data_lines.join("\n"),
        });
    }
    out
}

pub async fn collect_sse_stream<S, E>(stream: S) -> Result<Vec<SseEvent>, SseError>
where
    S: Stream<Item = Result<Bytes, E>> + Unpin,
    E: std::fmt::Display,
{
    let mut bytes = Vec::new();
    let mut stream = stream;
    while let Some(chunk) = stream.next().await {
        let chunk = chunk.map_err(|err| SseError::Read(err.to_string()))?;
        bytes.extend_from_slice(&chunk);
    }
    parse_sse_bytes(&bytes)
}
