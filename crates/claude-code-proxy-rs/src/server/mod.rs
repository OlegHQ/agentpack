mod config;
mod model;
mod stream;
mod websocket;

use std::sync::atomic::{AtomicU64, Ordering};
use std::sync::Arc;
use std::thread;
use std::time::Duration;

use anyhow::Context;
use http::{HeaderMap, HeaderValue};
use reqwest::blocking::{Client, Response as UpstreamResponse};
use serde_json::{json, Value};
use tiny_http::{Header, Method, Request, Response, Server, StatusCode};

use crate::anthropic::{anthropic_error_body, AnthropicRequest};
use crate::auth::ORIGINATOR;
use crate::codex::{translate_anthropic_to_codex, TranslateOptions};

pub use config::{ProxyConfig, TransportMode};
pub use model::{ModelMap, ProxyModel};
pub use websocket::{codex_websocket_headers, to_websocket_url, WEBSOCKET_PROTOCOL_HEADER};

static MESSAGE_COUNTER: AtomicU64 = AtomicU64::new(1);

#[derive(Clone, Debug)]
pub struct AuthSnapshot {
    pub access_token: String,
    pub account_id: Option<String>,
    pub endpoint_url: String,
}

pub trait AuthManager: Send + Sync {
    fn snapshot(&self) -> anyhow::Result<AuthSnapshot>;
    fn refresh_after_unauthorized(&self) -> anyhow::Result<bool>;
}

pub struct ProxyServer {
    server: Server,
    config: ProxyConfig,
    auth: Arc<dyn AuthManager>,
    http: Client,
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum ServeAction {
    Continue,
    Shutdown,
}

impl ProxyServer {
    pub fn bind(config: ProxyConfig, auth: Arc<dyn AuthManager>) -> anyhow::Result<Self> {
        let _ = rustls::crypto::aws_lc_rs::default_provider().install_default();
        let addr = format!("127.0.0.1:{}", config.bind_port);
        let server =
            Server::http(&addr).map_err(|err| anyhow::anyhow!("bind proxy {addr}: {err}"))?;
        let http = Client::builder()
            .timeout(config.request_timeout)
            .build()
            .context("build proxy HTTP client")?;
        Ok(Self {
            server,
            config,
            auth,
            http,
        })
    }

    pub fn base_url(&self) -> String {
        format!("http://{}", self.server.server_addr())
    }

    pub fn run(self: Arc<Self>) {
        for request in self.server.incoming_requests() {
            if self.handle(request) == ServeAction::Shutdown {
                break;
            }
        }
    }

    pub fn run_in_thread(self: Arc<Self>) -> anyhow::Result<thread::JoinHandle<()>> {
        thread::Builder::new()
            .name("agentpack-claude-proxy".into())
            .spawn(move || self.run())
            .context("start Claude proxy thread")
    }

    fn handle(&self, mut request: Request) -> ServeAction {
        let method = request.method().clone();
        let path = request
            .url()
            .split('?')
            .next()
            .unwrap_or(request.url())
            .to_string();

        if path == "/__agentpack/shutdown" {
            let _ = request.respond(json_response(StatusCode(200), json!({"ok": true})));
            return ServeAction::Shutdown;
        }

        if path == "/health" || path == "/healthz" {
            let upstream = self
                .auth
                .snapshot()
                .map(|snapshot| snapshot.endpoint_url)
                .unwrap_or_else(|_| "<unavailable>".into());
            let _ = request.respond(json_response(
                StatusCode(200),
                json!({"ok": true, "status": "healthy", "upstream": upstream}),
            ));
            return ServeAction::Continue;
        }

        if !self.authorized(&request) {
            let _ = request.respond(json_response(
                StatusCode(401),
                anthropic_error_body("authentication_error", "invalid proxy token"),
            ));
            return ServeAction::Continue;
        }

        match (method, path.as_str()) {
            (Method::Get, "/v1/models") => {
                let _ = request.respond(json_response(
                    StatusCode(200),
                    self.config.model_map.claude_models_json(),
                ));
            }
            (Method::Post, "/v1/messages/count_tokens") => {
                match read_anthropic_body(&mut request) {
                    Ok(body) => {
                        let _ = request.respond(json_response(
                            StatusCode(200),
                            json!({"input_tokens": count_tokens(&body)}),
                        ));
                    }
                    Err(err) => {
                        let _ = request.respond(error_response(400, "invalid_request_error", err));
                    }
                }
            }
            (Method::Post, "/v1/messages") => {
                let session_id = header_value(&request, "x-claude-code-session-id");
                match read_anthropic_body(&mut request) {
                    Ok(body) => self.respond_messages(request, body, session_id),
                    Err(err) => {
                        let _ = request.respond(error_response(400, "invalid_request_error", err));
                    }
                }
            }
            _ => {
                let _ = request.respond(error_response(
                    404,
                    "not_found_error",
                    anyhow::anyhow!("unknown proxy endpoint"),
                ));
            }
        }
        ServeAction::Continue
    }

    fn respond_messages(
        &self,
        request: Request,
        body: AnthropicRequest,
        session_id: Option<String>,
    ) {
        let wants_stream = body.stream == Some(true);
        let requested_model = body.model.clone();
        let message_id = next_message_id();

        if wants_stream {
            let this = self.clone_for_thread();
            let upstream_body = body.clone();
            let reader = stream::AnthropicSseReader::spawn(
                move || this.call_upstream_bytes(&upstream_body, session_id.as_deref()),
                message_id,
                requested_model,
            );
            let _ = request.respond(sse_stream_response(StatusCode(200), reader));
            return;
        }

        match self.call_upstream_bytes(&body, session_id.as_deref()) {
            Ok(bytes) => {
                match stream::accumulate_anthropic_response(&bytes, &message_id, &requested_model) {
                    Ok(value) => {
                        let _ = request.respond(json_response(StatusCode(200), value));
                    }
                    Err(err) => {
                        trace_proxy_error(&err);
                        let _ = request.respond(error_response(502, "api_error", err));
                    }
                }
            }
            Err(err) => {
                trace_proxy_error(&err);
                let _ = request.respond(error_response(502, "api_error", err));
            }
        }
    }

    fn clone_for_thread(&self) -> ProxyServerHandle {
        ProxyServerHandle {
            config: self.config.clone(),
            auth: Arc::clone(&self.auth),
            http: self.http.clone(),
        }
    }

    fn authorized(&self, request: &Request) -> bool {
        if self.config.client_token.is_empty() {
            return true;
        }
        request.headers().iter().any(|h| {
            h.field.equiv("authorization")
                && h.value
                    .as_str()
                    .strip_prefix("Bearer ")
                    .is_some_and(|token| token == self.config.client_token)
        }) || request
            .headers()
            .iter()
            .any(|h| h.field.equiv("x-api-key") && h.value.as_str() == self.config.client_token)
    }

    fn call_upstream_bytes(
        &self,
        anthropic: &AnthropicRequest,
        session_id: Option<&str>,
    ) -> anyhow::Result<Vec<u8>> {
        self.clone_for_thread()
            .call_upstream_bytes(anthropic, session_id)
    }
}

#[derive(Clone)]
struct ProxyServerHandle {
    config: ProxyConfig,
    auth: Arc<dyn AuthManager>,
    http: Client,
}

impl ProxyServerHandle {
    fn call_upstream_bytes(
        &self,
        anthropic: &AnthropicRequest,
        session_id: Option<&str>,
    ) -> anyhow::Result<Vec<u8>> {
        let snapshot = self.auth.snapshot()?;
        let (payload, requested) = self.translate_request(anthropic, session_id)?;
        match self.config.transport {
            TransportMode::Http => self.call_http(&snapshot, &payload, session_id),
            TransportMode::WebSocket => self.call_websocket(&snapshot, &payload, session_id),
            TransportMode::Auto => self
                .call_websocket(&snapshot, &payload, session_id)
                .or_else(|err| {
                    eprintln!(
                        "agentpack proxy: Codex WebSocket failed for {}; falling back to HTTP: {err:#}",
                        requested.upstream
                    );
                    self.call_http(&snapshot, &payload, session_id)
                }),
        }
    }

    fn translate_request(
        &self,
        anthropic: &AnthropicRequest,
        session_id: Option<&str>,
    ) -> anyhow::Result<(Value, ProxyModel)> {
        let requested = self.config.model_map.upstream_model_for(&anthropic.model);
        let mut translated_request = anthropic.clone();
        translated_request.model = requested.upstream.clone();
        translated_request.stream = Some(true);
        let codex = translate_anthropic_to_codex(
            &translated_request,
            TranslateOptions {
                session_id: session_id.map(str::to_string),
                service_tier: requested.service_tier.clone(),
                ..TranslateOptions::default()
            },
        )
        .context("translate Anthropic request to Codex Responses request")?;
        Ok((serde_json::to_value(codex)?, requested))
    }

    fn call_http(
        &self,
        snapshot: &AuthSnapshot,
        payload: &Value,
        session_id: Option<&str>,
    ) -> anyhow::Result<Vec<u8>> {
        let mut response = self.send_http_once(snapshot, payload, session_id)?;
        if response.status() == reqwest::StatusCode::UNAUTHORIZED
            && self.auth.refresh_after_unauthorized()?
        {
            let refreshed = self.auth.snapshot()?;
            response = self.send_http_once(&refreshed, payload, session_id)?;
        }
        let status = response.status();
        if !status.is_success() {
            let text = response.text().context("read upstream error body")?;
            return Err(anyhow::anyhow!(
                "upstream Codex request failed: {status}; body={}",
                truncate(&text, 1000)
            ));
        }
        response
            .bytes()
            .map(|bytes| bytes.to_vec())
            .context("read upstream Codex SSE body")
    }

    fn send_http_once(
        &self,
        snapshot: &AuthSnapshot,
        payload: &Value,
        session_id: Option<&str>,
    ) -> anyhow::Result<UpstreamResponse> {
        let headers = codex_headers(snapshot, session_id, false)?;
        self.http
            .post(&snapshot.endpoint_url)
            .headers(headers)
            .json(payload)
            .send()
            .context("send upstream Codex HTTP request")
    }

    fn call_websocket(
        &self,
        snapshot: &AuthSnapshot,
        payload: &Value,
        session_id: Option<&str>,
    ) -> anyhow::Result<Vec<u8>> {
        let headers = codex_headers(snapshot, session_id, true)?;
        match websocket::codex_websocket_request(
            &snapshot.endpoint_url,
            &headers,
            payload,
            self.config.connect_timeout,
            self.config.websocket_idle_timeout,
        ) {
            Ok(bytes) => Ok(bytes),
            Err(err) => {
                if let Some(ws) = err.downcast_ref::<websocket::CodexWebSocketSetupError>() {
                    if matches!(ws.status, Some(401 | 403))
                        && !ws.request_sent
                        && self.auth.refresh_after_unauthorized()?
                    {
                        let refreshed = self.auth.snapshot()?;
                        let refreshed_headers = codex_headers(&refreshed, session_id, true)?;
                        return websocket::codex_websocket_request(
                            &refreshed.endpoint_url,
                            &refreshed_headers,
                            payload,
                            self.config.connect_timeout,
                            self.config.websocket_idle_timeout,
                        );
                    }
                }
                Err(err)
            }
        }
    }
}

fn codex_headers(
    snapshot: &AuthSnapshot,
    session_id: Option<&str>,
    websocket: bool,
) -> anyhow::Result<HeaderMap> {
    let mut headers = HeaderMap::new();
    headers.insert("content-type", HeaderValue::from_static("application/json"));
    headers.insert("accept", HeaderValue::from_static("text/event-stream"));
    headers.insert(
        "authorization",
        HeaderValue::from_str(&format!("Bearer {}", snapshot.access_token))?,
    );
    headers.insert("originator", HeaderValue::from_static(ORIGINATOR));
    headers.insert(
        "openai-beta",
        HeaderValue::from_static("responses=experimental"),
    );
    headers.insert(
        "user-agent",
        HeaderValue::from_static("agentpack-claude-proxy"),
    );
    if let Some(account_id) = &snapshot.account_id {
        headers.insert("chatgpt-account-id", HeaderValue::from_str(account_id)?);
    }
    if let Some(session_id) = session_id {
        headers.insert("session_id", HeaderValue::from_str(session_id)?);
        headers.insert("x-client-request-id", HeaderValue::from_str(session_id)?);
        headers.insert(
            "x-codex-window-id",
            HeaderValue::from_str(&format!("{session_id}:0"))?,
        );
    }
    if websocket {
        Ok(websocket::codex_websocket_headers(&headers))
    } else {
        Ok(headers)
    }
}

fn read_anthropic_body(request: &mut Request) -> anyhow::Result<AnthropicRequest> {
    let mut body = String::new();
    request
        .as_reader()
        .read_to_string(&mut body)
        .context("read request body")?;
    serde_json::from_str(&body).context("parse Anthropic request JSON")
}

fn json_response(status: StatusCode, value: Value) -> Response<std::io::Cursor<Vec<u8>>> {
    let body = serde_json::to_vec(&value).unwrap_or_else(|_| b"{}".to_vec());
    let mut response = Response::from_data(body).with_status_code(status);
    response.add_header(json_header());
    response
}

fn sse_stream_response(
    status: StatusCode,
    body: stream::AnthropicSseReader,
) -> Response<stream::AnthropicSseReader> {
    Response::new(
        status,
        vec![
            Header::from_bytes("Content-Type", "text/event-stream")
                .expect("static header is valid"),
            Header::from_bytes("Cache-Control", "no-cache").expect("static header is valid"),
        ],
        body,
        None,
        None,
    )
}

fn error_response(
    status: u16,
    error_type: &str,
    err: anyhow::Error,
) -> Response<std::io::Cursor<Vec<u8>>> {
    json_response(
        StatusCode(status),
        anthropic_error_body(error_type, err.to_string()),
    )
}

fn json_header() -> Header {
    Header::from_bytes("Content-Type", "application/json").expect("static header is valid")
}

fn header_value(request: &Request, name: &str) -> Option<String> {
    request
        .headers()
        .iter()
        .find(|header| header.field.to_string().eq_ignore_ascii_case(name))
        .map(|header| header.value.as_str().to_string())
}

fn count_tokens(body: &AnthropicRequest) -> usize {
    let mut chars = 0usize;
    chars += serde_json::to_string(&body.system)
        .map(|s| s.len())
        .unwrap_or(0);
    for message in &body.messages {
        chars += serde_json::to_string(&message.content)
            .map(|s| s.len())
            .unwrap_or(0);
    }
    (chars / 4).max(1)
}

fn next_message_id() -> String {
    let n = MESSAGE_COUNTER.fetch_add(1, Ordering::Relaxed);
    let millis = chrono::Utc::now().timestamp_millis();
    format!("msg_agentpack_{millis}_{n}")
}

fn truncate(s: &str, max: usize) -> String {
    if s.len() <= max {
        s.to_string()
    } else {
        format!("{}...", &s[..max])
    }
}

fn trace_proxy_error(err: &anyhow::Error) {
    if std::env::var("AGENTPACK_PROXY_TRACE_ERRORS")
        .ok()
        .as_deref()
        == Some("1")
    {
        eprintln!("agentpack proxy upstream error: {err:#}");
    }
}

#[allow(dead_code)]
fn sleep_for_retry(duration: Duration) {
    std::thread::sleep(duration);
}

#[cfg(test)]
mod tests {
    use std::sync::atomic::{AtomicBool, Ordering};
    use std::sync::{mpsc, Arc};
    use std::thread;
    use std::time::Duration;

    use tiny_http::Server;

    use super::*;

    struct StaticAuth {
        endpoint_url: String,
        refreshed: AtomicBool,
    }

    impl AuthManager for StaticAuth {
        fn snapshot(&self) -> anyhow::Result<AuthSnapshot> {
            Ok(AuthSnapshot {
                access_token: "upstream-token".into(),
                account_id: Some("acct_1".into()),
                endpoint_url: self.endpoint_url.clone(),
            })
        }

        fn refresh_after_unauthorized(&self) -> anyhow::Result<bool> {
            self.refreshed.store(true, Ordering::SeqCst);
            Ok(true)
        }
    }

    fn test_config(token: &str) -> ProxyConfig {
        ProxyConfig {
            bind_port: 0,
            client_token: token.to_string(),
            transport: TransportMode::Http,
            request_timeout: Duration::from_secs(30),
            connect_timeout: Duration::from_secs(1),
            websocket_idle_timeout: Duration::from_secs(1),
            model_map: ModelMap::default(),
        }
    }

    #[test]
    fn proxy_messages_route_translates_through_codex_reducer() {
        let upstream = Server::http("127.0.0.1:0").unwrap();
        let endpoint = format!(
            "http://{}/backend-api/codex/responses",
            upstream.server_addr()
        );
        let (tx, rx) = mpsc::channel();
        let upstream_thread = thread::spawn(move || {
            let mut request = upstream.recv().unwrap();
            assert_eq!(request.url(), "/backend-api/codex/responses");
            let mut body = String::new();
            request.as_reader().read_to_string(&mut body).unwrap();
            tx.send(body).unwrap();
            let mut response =
                Response::from_string(include_str!("../../tests/golden/fixtures/codex_text.sse"));
            response.add_header(
                Header::from_bytes("Content-Type", "text/event-stream")
                    .expect("static header is valid"),
            );
            request.respond(response).unwrap();
        });

        let auth = Arc::new(StaticAuth {
            endpoint_url: endpoint,
            refreshed: AtomicBool::new(false),
        });
        let proxy = Arc::new(ProxyServer::bind(test_config("client-token"), auth).unwrap());
        let base_url = proxy.base_url();
        let proxy_thread = proxy.run_in_thread().unwrap();

        let response = Client::new()
            .post(format!("{base_url}/v1/messages"))
            .bearer_auth("client-token")
            .json(&json!({
                "model": "claude-sonnet-4-6",
                "messages": [{"role": "user", "content": "hello"}],
                "max_tokens": 10
            }))
            .send()
            .unwrap();
        assert!(response.status().is_success());
        let body: Value = response.json().unwrap();
        assert_eq!(body["content"][0]["text"], "Hello world");
        assert_eq!(body["usage"]["cache_read_input_tokens"], 5);

        let upstream_body: Value = serde_json::from_str(&rx.recv().unwrap()).unwrap();
        assert_eq!(upstream_body["model"], "gpt-5.4");
        assert_eq!(upstream_body["stream"], true);
        assert_eq!(upstream_body["input"][0]["content"][0]["text"], "hello");
        assert_eq!(
            upstream_body["prompt_cache_key"],
            Value::Null,
            "no session id should omit prompt cache key"
        );

        let _ = reqwest::blocking::get(format!("{base_url}/__agentpack/shutdown"));
        proxy_thread.join().unwrap();
        upstream_thread.join().unwrap();
    }

    #[test]
    fn proxy_rejects_invalid_client_token() {
        let auth = Arc::new(StaticAuth {
            endpoint_url: "http://127.0.0.1:9/backend-api/codex/responses".into(),
            refreshed: AtomicBool::new(false),
        });
        let proxy = Arc::new(ProxyServer::bind(test_config("client-token"), auth).unwrap());
        let base_url = proxy.base_url();
        let proxy_thread = proxy.run_in_thread().unwrap();

        let response = Client::new()
            .post(format!("{base_url}/v1/messages/count_tokens"))
            .bearer_auth("wrong")
            .json(&json!({
                "model": "claude-sonnet-4-6",
                "messages": [{"role": "user", "content": "hello"}]
            }))
            .send()
            .unwrap();
        assert_eq!(response.status(), reqwest::StatusCode::UNAUTHORIZED);

        let _ = reqwest::blocking::get(format!("{base_url}/__agentpack/shutdown"));
        proxy_thread.join().unwrap();
    }
}
