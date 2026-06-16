use base64::engine::general_purpose::URL_SAFE_NO_PAD;
use base64::Engine;
use http::HeaderMap;
use rand::RngCore;
use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};
use thiserror::Error;
use url::Url;

pub const CLIENT_ID: &str = "app_EMoamEEZ73f0CkXaXp7hrann";
pub const ISSUER: &str = "https://auth.openai.com";
pub const CODEX_API_ENDPOINT: &str = "https://chatgpt.com/backend-api/codex/responses";
pub const OAUTH_PORT: u16 = 1455;
pub const OAUTH_REDIRECT_URI: &str = "http://localhost:1455/auth/callback";
pub const ORIGINATOR: &str = "claude-code-proxy";
pub const REFRESH_MARGIN_MS: i64 = 5 * 60 * 1000;

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct TokenResponse {
    pub access_token: String,
    pub refresh_token: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub expires_in: Option<i64>,
    #[serde(flatten)]
    pub extra: serde_json::Map<String, serde_json::Value>,
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct StoredAuth {
    pub access: String,
    pub refresh: String,
    pub expires: i64,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub account_id: Option<String>,
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct PkceCodes {
    pub verifier: String,
    pub challenge: String,
}

#[derive(Debug, Error)]
pub enum AuthError {
    #[error("not authenticated")]
    NotAuthenticated,
    #[error("invalid token response")]
    InvalidTokenResponse,
    #[error("{0}")]
    Store(String),
    #[error("{0}")]
    Http(String),
}

pub trait TokenStore {
    fn load_auth(&self) -> Result<Option<StoredAuth>, AuthError>;
    fn save_auth(&self, auth: &StoredAuth) -> Result<(), AuthError>;
}

pub trait AuthHttpClient {
    fn refresh(
        &self,
        refresh_token: &str,
    ) -> impl std::future::Future<Output = Result<TokenResponse, AuthError>> + Send;
}

#[derive(Debug, Clone)]
pub struct CodexAuthLifecycle<S, H> {
    store: S,
    http: H,
    cached: Option<StoredAuth>,
    refresh_margin_ms: i64,
}

impl<S, H> CodexAuthLifecycle<S, H>
where
    S: TokenStore,
    H: AuthHttpClient,
{
    pub fn new(store: S, http: H) -> Self {
        Self {
            store,
            http,
            cached: None,
            refresh_margin_ms: REFRESH_MARGIN_MS,
        }
    }

    pub async fn get_auth(&mut self, now_ms: i64) -> Result<StoredAuth, AuthError> {
        let current = match self.cached.clone() {
            Some(auth) => auth,
            None => self.store.load_auth()?.ok_or(AuthError::NotAuthenticated)?,
        };
        if current.expires - now_ms > self.refresh_margin_ms {
            self.cached = Some(current.clone());
            return Ok(current);
        }
        self.force_refresh(now_ms).await
    }

    pub async fn force_refresh(&mut self, now_ms: i64) -> Result<StoredAuth, AuthError> {
        let current = self
            .cached
            .clone()
            .or_else(|| self.store.load_auth().ok().flatten())
            .ok_or(AuthError::NotAuthenticated)?;
        let tokens = self.http.refresh(&current.refresh).await?;
        let auth = persist_tokens(&self.store, tokens, now_ms, current.account_id)?;
        self.cached = Some(auth.clone());
        Ok(auth)
    }
}

pub fn persist_tokens<S: TokenStore>(
    store: &S,
    tokens: TokenResponse,
    now_ms: i64,
    previous_account_id: Option<String>,
) -> Result<StoredAuth, AuthError> {
    validate_token_response(&tokens)?;
    let auth = StoredAuth {
        access: tokens.access_token,
        refresh: tokens.refresh_token,
        expires: now_ms + tokens.expires_in.unwrap_or(3600) * 1000,
        account_id: extract_account_id_from_extra(&tokens.extra).or(previous_account_id),
    };
    store.save_auth(&auth)?;
    Ok(auth)
}

pub fn validate_token_response(tokens: &TokenResponse) -> Result<(), AuthError> {
    if tokens.access_token.is_empty() || tokens.refresh_token.is_empty() {
        Err(AuthError::InvalidTokenResponse)
    } else {
        Ok(())
    }
}

pub fn generate_pkce() -> PkceCodes {
    let mut bytes = [0_u8; 32];
    rand::thread_rng().fill_bytes(&mut bytes);
    let verifier = URL_SAFE_NO_PAD.encode(bytes);
    let challenge = URL_SAFE_NO_PAD.encode(Sha256::digest(verifier.as_bytes()));
    PkceCodes {
        verifier,
        challenge,
    }
}

pub fn generate_state() -> String {
    let mut bytes = [0_u8; 32];
    rand::thread_rng().fill_bytes(&mut bytes);
    URL_SAFE_NO_PAD.encode(bytes)
}

pub fn build_authorize_url(pkce: &PkceCodes, state: &str) -> String {
    let mut url = Url::parse(&format!("{ISSUER}/oauth/authorize")).expect("valid issuer URL");
    url.query_pairs_mut()
        .append_pair("response_type", "code")
        .append_pair("client_id", CLIENT_ID)
        .append_pair("redirect_uri", OAUTH_REDIRECT_URI)
        .append_pair("scope", "openid profile email offline_access")
        .append_pair("code_challenge", &pkce.challenge)
        .append_pair("code_challenge_method", "S256")
        .append_pair("id_token_add_organizations", "true")
        .append_pair("codex_cli_simplified_flow", "true")
        .append_pair("state", state)
        .append_pair("originator", ORIGINATOR);
    url.to_string()
}

pub fn token_refresh_form(refresh_token: &str) -> Vec<(String, String)> {
    vec![
        ("grant_type".to_string(), "refresh_token".to_string()),
        ("refresh_token".to_string(), refresh_token.to_string()),
        ("client_id".to_string(), CLIENT_ID.to_string()),
    ]
}

pub fn device_init_body() -> serde_json::Value {
    serde_json::json!({ "client_id": CLIENT_ID })
}

pub fn device_poll_body(device_auth_id: &str, user_code: &str) -> serde_json::Value {
    serde_json::json!({
        "device_auth_id": device_auth_id,
        "user_code": user_code
    })
}

pub fn build_codex_headers(
    auth: &StoredAuth,
    session_id: Option<&str>,
    user_agent: Option<&str>,
) -> HeaderMap {
    let mut headers = HeaderMap::new();
    headers.insert("content-type", "application/json".parse().unwrap());
    headers.insert("accept", "text/event-stream".parse().unwrap());
    headers.insert(
        "authorization",
        format!("Bearer {}", auth.access).parse().unwrap(),
    );
    headers.insert("originator", ORIGINATOR.parse().unwrap());
    headers.insert("openai-beta", "responses=experimental".parse().unwrap());
    if let Some(user_agent) = user_agent {
        headers.insert("user-agent", user_agent.parse().unwrap());
    }
    if let Some(account_id) = &auth.account_id {
        headers.insert("chatgpt-account-id", account_id.parse().unwrap());
    }
    if let Some(session_id) = session_id {
        headers.insert("session_id", session_id.parse().unwrap());
        headers.insert("x-client-request-id", session_id.parse().unwrap());
        headers.insert(
            "x-codex-window-id",
            format!("{session_id}:0").parse().unwrap(),
        );
    }
    headers
}

pub fn extract_account_id_from_extra(
    extra: &serde_json::Map<String, serde_json::Value>,
) -> Option<String> {
    extra
        .get("account_id")
        .and_then(serde_json::Value::as_str)
        .map(str::to_string)
}
