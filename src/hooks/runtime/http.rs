use anyhow::{anyhow, Context};
use reqwest::blocking::Client;
use serde_json::Value;

use super::bridge::{normalize_response, stdin_json};
use crate::hooks::ir::{ClaudeHandler, NormalizedHookResult};
use crate::hooks::runtime::bridge::HookExecutionSpec;

pub fn execute(
    spec: &HookExecutionSpec,
    stdin_bytes: &[u8],
) -> anyhow::Result<NormalizedHookResult> {
    let ClaudeHandler::Http(handler) = &spec.handler else {
        return Err(anyhow!("http executor received non-http hook"));
    };
    let method = handler.method.as_deref().unwrap_or("POST");
    let method = reqwest::Method::from_bytes(method.as_bytes()).context("invalid HTTP method")?;
    let client = Client::new();
    let mut req = client.request(method.clone(), &handler.url);
    for (key, value) in &handler.headers {
        req = req.header(key, value);
    }
    if let Some(body) = &handler.body {
        req = req.json(body);
    } else if method != reqwest::Method::GET && !stdin_bytes.is_empty() {
        req = req.header("content-type", "application/json");
        req = req.body(stdin_bytes.to_vec());
    }
    let response = req.send().context("send hook HTTP request")?;
    let status = response.status();
    let body = response.text().context("read hook HTTP response body")?;
    if body.trim().is_empty() {
        return Ok(NormalizedHookResult::default());
    }
    let parsed = serde_json::from_str::<Value>(&body)
        .or_else(|_| stdin_json(body.as_bytes()).ok_or_else(|| anyhow!("not JSON response")))?;
    let mut normalized = normalize_response(parsed);
    if !status.is_success() && normalized.message.is_none() {
        normalized.message = Some(format!("hook HTTP request failed with status {status}"));
    }
    Ok(normalized)
}
