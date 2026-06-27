use std::net::{TcpStream, ToSocketAddrs};
use std::time::Duration;

use anyhow::Context;
use http::{HeaderMap, HeaderValue};
use serde_json::Value;
use tungstenite::client::IntoClientRequest;
use tungstenite::handshake::HandshakeError;
use tungstenite::stream::MaybeTlsStream;
use tungstenite::{client_tls_with_config, Error as WsError, Message, WebSocket};
use url::Url;

use super::diagnostics::ProxyDiagnostics;

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
    connect_timeout: Duration,
    idle_timeout: Duration,
    diagnostics: &ProxyDiagnostics,
    request_id: u64,
) -> anyhow::Result<Vec<u8>> {
    let ws_url = to_websocket_url(url)?;
    let request_headers = codex_websocket_headers(headers);
    let mut socket = connect_timeout_aware(
        &ws_url,
        &request_headers,
        connect_timeout,
        idle_timeout,
        diagnostics,
        request_id,
    )?;
    let payload = websocket_payload(body);
    socket
        .send(Message::Text(payload.into()))
        .context("send Codex WebSocket response.create")?;
    diagnostics.event(
        "websocket_payload_sent",
        serde_json::json!({"request_id": request_id}),
    );

    let mut out = Vec::new();
    let mut request_sent = true;
    let mut frames = 0_u64;
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
        frames += 1;

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
                    diagnostics.event(
                        "websocket_terminal_frame",
                        serde_json::json!({
                            "request_id": request_id,
                            "frames": frames,
                            "terminal_type": frame_type(&text),
                        }),
                    );
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
                diagnostics.event(
                    "websocket_ping",
                    serde_json::json!({"request_id": request_id}),
                );
            }
            Message::Pong(_) | Message::Frame(_) => {}
        }
        request_sent = true;
    }
    Ok(out)
}

fn connect_timeout_aware(
    ws_url: &str,
    headers: &HeaderMap,
    connect_timeout: Duration,
    idle_timeout: Duration,
    diagnostics: &ProxyDiagnostics,
    request_id: u64,
) -> anyhow::Result<WebSocket<MaybeTlsStream<TcpStream>>> {
    let parsed =
        Url::parse(ws_url).with_context(|| format!("parse Codex WebSocket URL {ws_url}"))?;
    let host = parsed
        .host_str()
        .ok_or_else(|| anyhow::anyhow!("Codex WebSocket URL has no host"))?;
    let port = parsed.port_or_known_default().ok_or_else(|| {
        anyhow::anyhow!(
            "Codex WebSocket URL has no port for scheme {}",
            parsed.scheme()
        )
    })?;
    let addrs = (host, port)
        .to_socket_addrs()
        .with_context(|| format!("resolve Codex WebSocket host {host}:{port}"))?
        .collect::<Vec<_>>();
    if addrs.is_empty() {
        return Err(anyhow::anyhow!(
            "resolve Codex WebSocket host {host}:{port}: no addresses"
        ));
    }

    let mut last_error = None;
    for addr in addrs {
        diagnostics.event(
            "websocket_connect_attempt",
            serde_json::json!({"request_id": request_id, "addr": addr.to_string()}),
        );
        let stream = match TcpStream::connect_timeout(&addr, connect_timeout) {
            Ok(stream) => stream,
            Err(err) => {
                last_error = Some(anyhow::anyhow!("connect {addr}: {err}"));
                continue;
            }
        };
        stream
            .set_read_timeout(Some(idle_timeout))
            .with_context(|| format!("set Codex WebSocket read timeout on {addr}"))?;
        stream
            .set_write_timeout(Some(idle_timeout))
            .with_context(|| format!("set Codex WebSocket write timeout on {addr}"))?;

        let request = websocket_request(ws_url, headers)?;
        match client_tls_with_config(request, stream, None, None) {
            Ok((socket, _)) => {
                diagnostics.event(
                    "websocket_connected",
                    serde_json::json!({"request_id": request_id, "addr": addr.to_string()}),
                );
                return Ok(socket);
            }
            Err(err) => match websocket_handshake_error(err) {
                Ok(setup) => return Err(setup.into()),
                Err(err) => last_error = Some(err),
            },
        }
    }

    Err(last_error.unwrap_or_else(|| anyhow::anyhow!("connect Codex WebSocket failed")))
}

fn websocket_request(ws_url: &str, headers: &HeaderMap) -> anyhow::Result<http::Request<()>> {
    let mut request = ws_url
        .into_client_request()
        .context("build Codex WebSocket request")?;
    for (name, value) in headers.iter() {
        request.headers_mut().insert(name.clone(), value.clone());
    }
    Ok(request)
}

fn websocket_handshake_error(
    err: HandshakeError<tungstenite::ClientHandshake<MaybeTlsStream<TcpStream>>>,
) -> Result<CodexWebSocketSetupError, anyhow::Error> {
    match err {
        HandshakeError::Failure(WsError::Http(response)) => {
            let status = response.status().as_u16();
            Ok(CodexWebSocketSetupError {
                message: format!("Codex WebSocket handshake failed: HTTP {status}"),
                status: Some(status),
                code: None,
                retry_after: response
                    .headers()
                    .get("retry-after")
                    .and_then(|value| value.to_str().ok())
                    .map(str::to_string),
                request_sent: false,
            })
        }
        HandshakeError::Failure(err) => Err(anyhow::anyhow!("connect Codex WebSocket: {err}")),
        HandshakeError::Interrupted(_) => Err(anyhow::anyhow!(
            "connect Codex WebSocket interrupted during blocking handshake"
        )),
    }
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

fn frame_type(text: &str) -> Option<String> {
    serde_json::from_str::<Value>(text)
        .ok()
        .and_then(|payload| {
            payload
                .get("type")
                .and_then(Value::as_str)
                .map(str::to_string)
        })
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
