use std::time::Duration;

use super::model::ModelMap;

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum TransportMode {
    Http,
    WebSocket,
    Auto,
}

#[derive(Clone, Debug)]
pub struct ProxyConfig {
    pub bind_port: u16,
    pub client_token: String,
    pub transport: TransportMode,
    pub request_timeout: Duration,
    pub connect_timeout: Duration,
    pub websocket_idle_timeout: Duration,
    pub model_map: ModelMap,
}

impl ProxyConfig {
    pub fn from_env(client_token: String) -> Self {
        Self {
            bind_port: env_u16("AGENTPACK_PROXY_PORT", 0),
            client_token,
            transport: TransportMode::from_env(),
            request_timeout: Duration::from_secs(env_u64(
                "AGENTPACK_PROXY_REQUEST_TIMEOUT_SECS",
                300,
            )),
            connect_timeout: Duration::from_secs(env_u64(
                "AGENTPACK_PROXY_WS_CONNECT_TIMEOUT_SECS",
                15,
            )),
            websocket_idle_timeout: Duration::from_secs(env_u64(
                "AGENTPACK_PROXY_WS_IDLE_TIMEOUT_SECS",
                300,
            )),
            model_map: ModelMap::from_env(),
        }
    }
}

impl TransportMode {
    pub fn from_env() -> Self {
        match std::env::var("AGENTPACK_PROXY_TRANSPORT")
            .unwrap_or_else(|_| "websocket".into())
            .trim()
            .to_ascii_lowercase()
            .as_str()
        {
            "http" => Self::Http,
            "auto" => Self::Auto,
            _ => Self::WebSocket,
        }
    }
}

fn env_u16(key: &str, default: u16) -> u16 {
    std::env::var(key)
        .ok()
        .and_then(|s| s.trim().parse::<u16>().ok())
        .unwrap_or(default)
}

fn env_u64(key: &str, default: u64) -> u64 {
    std::env::var(key)
        .ok()
        .and_then(|s| s.trim().parse::<u64>().ok())
        .unwrap_or(default)
}
