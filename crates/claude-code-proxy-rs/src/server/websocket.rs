use std::time::Duration;

use anyhow::Context;
use http::{HeaderMap, HeaderValue};
use serde_json::Value;
use tungstenite::client::IntoClientRequest;
use tungstenite::{connect, Message};
use url::Url;

pub const WEBSOCKET_PROTOCOL_HEADER: &str = "responses_websockets=2026-02-06";

#[derive(Debug, thiserror::Error)]
#[error("{message}")]
pub struct CodexWebSocketSetupError {
    pub message: String,
    pub status: Option<u16>,
    pub code: Option<String>,
    pub retry_after: Option<String>,
    pub request_sent: bool,
}

pub fn to_websocket_url(url: &str) -> anyhow::Result<String> {
    let mut parsed = Url::parse(url).with_context(|| format!("parse Codex URL {url}"))?;
    match parsed.scheme() {
        "http" => {
            parsed
                .set_scheme("ws")
                .map_err(|_| anyhow::anyhow!("unsupported Codex WebSocket URL scheme: http"))?;
        }
        "https" => {
            parsed
                .set_scheme("wss")
                .map_err(|_| anyhow::anyhow!("unsupported Codex WebSocket URL scheme: https"))?;
        }
        scheme => {
            return Err(anyhow::anyhow!(
                "unsupported Codex WebSocket URL scheme: {scheme}"
            ))
        }
    }
    Ok(parsed.to_string())
}

pub fn codex_websocket_headers(headers: &HeaderMap) -> HeaderMap {
    let mut out = headers.clone();
    out.insert(
        "openai-beta",
        HeaderValue::from_static(WEBSOCKET_PROTOCOL_HEADER),
    );
    out.remove("content-length");
    out
}

pub fn codex_websocket_request(
    url: &str,
    headers: &HeaderMap,
    body: &Value,
    _connect_timeout: Duration,
    _idle_timeout: Duration,
) -> anyhow::Result<Vec<u8>> {
    let ws_url = to_websocket_url(url)?;
    let request_headers = codex_websocket_headers(headers);
    let mut request = ws_url
        .into_client_request()
        .context("build Codex WebSocket request")?;
    for (name, value) in request_headers.iter() {
        request.headers_mut().insert(name.clone(), value.clone());
    }

    let (mut socket, _) = connect(request).context("connect Codex WebSocket")?;
    let payload = websocket_payload(body);
    socket
        .send(Message::Text(payload.into()))
        .context("send Codex WebSocket response.create")?;

    let mut out = Vec::new();
    let mut request_sent = true;
    loop {
        let message = match socket.read() {
            Ok(message) => message,
            Err(err) => {
                if out.is_empty() {
                    return Err(CodexWebSocketSetupError {
                        message: format!("Codex WebSocket closed before terminal event: {err}"),
                        status: None,
                        code: None,
                        retry_after: None,
                        request_sent,
                    }
                    .into());
                }
                break;
            }
        };

        match message {
            Message::Text(text) => {
                let text = text.to_string();
                if out.is_empty() {
                    if let Some(err) = setup_error_from_frame(&text, request_sent) {
                        return Err(err.into());
                    }
                }
                out.extend_from_slice(encode_frame_as_sse(&text).as_bytes());
                if is_terminal_frame(&text) {
                    break;
                }
            }
            Message::Binary(_) => {
                return Err(anyhow::anyhow!("unexpected binary Codex WebSocket frame"));
            }
            Message::Close(_) => {
                if out.is_empty() {
                    return Err(CodexWebSocketSetupError {
                        message: "Codex WebSocket closed before terminal event".into(),
                        status: None,
                        code: None,
                        retry_after: None,
                        request_sent,
                    }
                    .into());
                }
                break;
            }
            Message::Ping(bytes) => {
                let _ = socket.send(Message::Pong(bytes));
            }
            Message::Pong(_) | Message::Frame(_) => {}
        }
        request_sent = true;
    }
    Ok(out)
}

fn websocket_payload(body: &Value) -> String {
    let mut value = body.clone();
    if let Some(obj) = value.as_object_mut() {
        obj.remove("stream");
        obj.insert(
            "type".to_string(),
            Value::String("response.create".to_string()),
        );
    }
    serde_json::to_string(&value).unwrap_or_else(|_| "{\"type\":\"response.create\"}".into())
}

fn encode_frame_as_sse(text: &str) -> String {
    let mut out = String::new();
    for line in text.split('\n') {
        out.push_str("data: ");
        out.push_str(line);
        out.push('\n');
    }
    out.push('\n');
    out
}

fn setup_error_from_frame(text: &str, request_sent: bool) -> Option<CodexWebSocketSetupError> {
    let payload = serde_json::from_str::<Value>(text).ok()?;
    let status = payload
        .get("status")
        .or_else(|| payload.get("status_code"))
        .and_then(Value::as_u64)
        .map(|n| n as u16);
    let code = payload
        .pointer("/error/code")
        .and_then(Value::as_str)
        .map(str::to_string);
    let message = payload
        .pointer("/error/message")
        .and_then(Value::as_str)
        .or(code.as_deref())
        .unwrap_or("Codex WebSocket setup error")
        .to_string();
    let retry_after = payload
        .pointer("/headers/retry-after")
        .and_then(Value::as_str)
        .map(str::to_string);
    let is_setup = matches!(status, Some(401 | 403 | 429))
        || code.as_deref() == Some("previous_response_not_found");
    is_setup.then_some(CodexWebSocketSetupError {
        message,
        status,
        code,
        retry_after,
        request_sent,
    })
}

fn is_terminal_frame(text: &str) -> bool {
    let Ok(payload) = serde_json::from_str::<Value>(text) else {
        return false;
    };
    matches!(
        payload.get("type").and_then(Value::as_str),
        Some(
            "response.completed"
                | "response.failed"
                | "response.incomplete"
                | "response.done"
                | "error"
        )
    )
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn converts_http_urls_to_websocket_urls() {
        assert_eq!(
            to_websocket_url("https://chatgpt.com/backend-api/codex/responses").unwrap(),
            "wss://chatgpt.com/backend-api/codex/responses"
        );
        assert_eq!(
            to_websocket_url("http://127.0.0.1:1234/backend-api/codex/responses").unwrap(),
            "ws://127.0.0.1:1234/backend-api/codex/responses"
        );
    }

    #[test]
    fn websocket_headers_set_beta_and_drop_content_length() {
        let mut headers = HeaderMap::new();
        headers.insert(
            "openai-beta",
            HeaderValue::from_static("responses=experimental"),
        );
        headers.insert("content-length", HeaderValue::from_static("123"));

        let got = codex_websocket_headers(&headers);

        assert_eq!(
            got.get("openai-beta").and_then(|v| v.to_str().ok()),
            Some(WEBSOCKET_PROTOCOL_HEADER)
        );
        assert!(!got.contains_key("content-length"));
    }

    #[test]
    fn websocket_payload_removes_stream_and_sets_create_type() {
        let body = serde_json::json!({"model":"gpt-5.5","stream":true,"input":[]});
        let got: Value = serde_json::from_str(&websocket_payload(&body)).unwrap();

        assert_eq!(got["type"], "response.create");
        assert!(got.get("stream").is_none());
    }
}
