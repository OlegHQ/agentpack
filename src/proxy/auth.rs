use std::fs::{self, OpenOptions};
use std::io::Write;
use std::path::{Path, PathBuf};
use std::sync::Mutex;

use anyhow::Context;
use claude_code_proxy::server::{AuthManager, AuthSnapshot, TransportMode};
use keyring::Entry;
use reqwest::blocking::Client;
use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};

const CHATGPT_CODEX_RESPONSES_URL: &str = claude_code_proxy::auth::CODEX_API_ENDPOINT;
const OPENAI_RESPONSES_URL: &str = "https://api.openai.com/v1/responses";
const REFRESH_TOKEN_URL: &str = "https://auth.openai.com/oauth/token";
const CLIENT_ID: &str = claude_code_proxy::auth::CLIENT_ID;
const CODEX_AUTH_KEYRING_SERVICE: &str = "Codex Auth";

pub(crate) struct UpstreamAuth {
    state: Mutex<AuthState>,
    http: Client,
}

#[derive(Clone, Debug)]
struct AuthState {
    token: String,
    url: String,
    account_id: Option<String>,
    auth_file: Option<PathBuf>,
    refresh_token: Option<String>,
    source: AuthSource,
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
enum AuthSource {
    ApiKey,
    ChatGpt,
}

#[derive(Deserialize, Serialize, Default)]
struct AuthDotJson {
    #[serde(rename = "OPENAI_API_KEY")]
    openai_api_key: Option<String>,
    tokens: Option<TokenData>,
    last_refresh: Option<String>,
    personal_access_token: Option<String>,
}

#[derive(Deserialize, Serialize, Default)]
struct TokenData {
    id_token: Option<serde_json::Value>,
    access_token: String,
    refresh_token: String,
    account_id: Option<String>,
}

#[derive(Deserialize)]
struct RefreshResponse {
    access_token: Option<String>,
    refresh_token: Option<String>,
    id_token: Option<String>,
}

impl UpstreamAuth {
    pub(crate) fn load() -> anyhow::Result<Self> {
        let mut state = load_from_env_or_codex_auth()?;
        if let Ok(override_url) = std::env::var("AGENTPACK_PROXY_UPSTREAM_URL") {
            if !override_url.trim().is_empty() {
                state.url = override_url.trim().to_string();
            }
        }
        Ok(Self {
            state: Mutex::new(state),
            http: Client::builder()
                .build()
                .context("build proxy auth HTTP client")?,
        })
    }

    pub(crate) fn default_transport(&self) -> TransportMode {
        let state = self.state.lock().expect("proxy auth mutex poisoned");
        match state.source {
            AuthSource::ApiKey => TransportMode::Http,
            AuthSource::ChatGpt => TransportMode::WebSocket,
        }
    }
}

impl AuthManager for UpstreamAuth {
    fn snapshot(&self) -> anyhow::Result<AuthSnapshot> {
        let state = self.state.lock().expect("proxy auth mutex poisoned");
        Ok(AuthSnapshot {
            access_token: state.token.clone(),
            account_id: state.account_id.clone(),
            endpoint_url: state.url.clone(),
        })
    }

    fn refresh_after_unauthorized(&self) -> anyhow::Result<bool> {
        let mut state = self.state.lock().expect("proxy auth mutex poisoned");
        if state.source != AuthSource::ChatGpt {
            return Ok(false);
        }
        let Some(refresh_token) = state.refresh_token.clone() else {
            return Ok(false);
        };

        let body = serde_json::json!({
            "client_id": CLIENT_ID,
            "grant_type": "refresh_token",
            "refresh_token": refresh_token
        });
        let response = self
            .http
            .post(REFRESH_TOKEN_URL)
            .json(&body)
            .send()
            .context("refresh Codex OAuth token")?;
        if !response.status().is_success() {
            return Ok(false);
        }
        let refreshed: RefreshResponse = response.json().context("parse OAuth refresh response")?;
        let Some(access_token) = refreshed.access_token else {
            return Ok(false);
        };
        state.token = access_token.clone();
        if let Some(refresh) = refreshed.refresh_token {
            state.refresh_token = Some(refresh);
        }
        persist_refreshed_tokens(&state, refreshed.id_token, access_token)?;
        Ok(true)
    }
}

fn load_from_env_or_codex_auth() -> anyhow::Result<AuthState> {
    if let Ok(key) = std::env::var("OPENAI_API_KEY") {
        if !key.trim().is_empty() {
            return Ok(AuthState {
                token: key.trim().to_string(),
                url: OPENAI_RESPONSES_URL.into(),
                account_id: None,
                auth_file: None,
                refresh_token: None,
                source: AuthSource::ApiKey,
            });
        }
    }

    let auth_file = resolve_auth_json()
        .context("OPENAI_API_KEY is unset and no Codex auth.json is available for proxy auth")?;
    let raw = fs::read_to_string(&auth_file)
        .with_context(|| format!("read Codex auth file {}", auth_file.display()))?;
    let parsed: AuthDotJson = serde_json::from_str(&raw)
        .with_context(|| format!("parse Codex auth file {}", auth_file.display()))?;

    if let Some(token) = parsed
        .personal_access_token
        .filter(|s| !s.trim().is_empty())
    {
        return Ok(AuthState {
            token,
            url: CHATGPT_CODEX_RESPONSES_URL.into(),
            account_id: None,
            auth_file: Some(auth_file),
            refresh_token: None,
            source: AuthSource::ChatGpt,
        });
    }

    if let Some(tokens) = parsed.tokens {
        if !tokens.access_token.trim().is_empty() {
            return Ok(AuthState {
                token: tokens.access_token,
                url: CHATGPT_CODEX_RESPONSES_URL.into(),
                account_id: tokens.account_id,
                auth_file: Some(auth_file),
                refresh_token: Some(tokens.refresh_token),
                source: AuthSource::ChatGpt,
            });
        }
    }

    if let Some(key) = parsed.openai_api_key.filter(|s| !s.trim().is_empty()) {
        return Ok(AuthState {
            token: key,
            url: OPENAI_RESPONSES_URL.into(),
            account_id: None,
            auth_file: Some(auth_file),
            refresh_token: None,
            source: AuthSource::ApiKey,
        });
    }

    Err(anyhow::anyhow!(
        "Codex auth file did not contain an API key, ChatGPT access token, or personal access token"
    ))
}

fn resolve_auth_json() -> Option<PathBuf> {
    if let Ok(path) = std::env::var("AGENTPACK_PROXY_AUTH_JSON") {
        let path = PathBuf::from(path);
        if path.is_file() {
            return Some(path);
        }
    }
    if let Some(home) = dirs::home_dir() {
        let user = home.join(".codex/auth.json");
        if user.is_file() {
            return Some(user);
        }
        if let Some(shared) = materialize_keyring_auth(&home.join(".codex")) {
            return Some(shared);
        }
    }
    crate::paths::shared_codex_auth_path()
        .ok()
        .filter(|p| p.is_file())
}

fn materialize_keyring_auth(user_codex_home: &Path) -> Option<PathBuf> {
    let account = codex_cli_keyring_account(user_codex_home);
    let entry = Entry::new(CODEX_AUTH_KEYRING_SERVICE, &account).ok()?;
    let json = match entry.get_password() {
        Ok(json) => json,
        Err(err) => {
            tracing::debug!("could not read Codex keyring auth for proxy: {err}");
            return None;
        }
    };
    let value = serde_json::from_str::<serde_json::Value>(&json).ok()?;
    let shared = crate::paths::shared_codex_auth_path().ok()?;
    if let Some(parent) = shared.parent() {
        if let Err(err) = fs::create_dir_all(parent) {
            tracing::debug!("could not create shared Codex auth dir for proxy: {err}");
            return None;
        }
    }
    if let Err(err) = write_json_atomic(&shared, &value) {
        tracing::debug!("could not materialize shared Codex auth for proxy: {err}");
        return None;
    }
    Some(shared)
}

fn codex_cli_keyring_account(codex_home: &Path) -> String {
    let canonical = codex_home
        .canonicalize()
        .unwrap_or_else(|_| codex_home.to_path_buf());
    let path_str = canonical.to_string_lossy();
    let mut hasher = Sha256::new();
    hasher.update(path_str.as_bytes());
    let digest = hasher.finalize();
    let hex = format!("{digest:x}");
    let truncated = hex.get(..16).unwrap_or(&hex);
    format!("cli|{truncated}")
}

fn persist_refreshed_tokens(
    state: &AuthState,
    id_token: Option<String>,
    access_token: String,
) -> anyhow::Result<()> {
    let Some(path) = &state.auth_file else {
        return Ok(());
    };
    let raw = fs::read_to_string(path).unwrap_or_else(|_| "{}".into());
    let mut auth: serde_json::Value =
        serde_json::from_str(&raw).unwrap_or_else(|_| serde_json::json!({}));
    let obj = auth
        .as_object_mut()
        .ok_or_else(|| anyhow::anyhow!("Codex auth file root is not an object"))?;
    let tokens = obj
        .entry("tokens")
        .or_insert_with(|| serde_json::json!({}))
        .as_object_mut()
        .ok_or_else(|| anyhow::anyhow!("Codex auth tokens field is not an object"))?;
    tokens.insert("access_token".into(), serde_json::json!(access_token));
    if let Some(refresh) = &state.refresh_token {
        tokens.insert("refresh_token".into(), serde_json::json!(refresh));
    }
    if let Some(account_id) = &state.account_id {
        tokens.insert("account_id".into(), serde_json::json!(account_id));
    }
    if let Some(jwt) = id_token {
        tokens.insert("id_token".into(), serde_json::json!({"raw_jwt": jwt}));
    }
    obj.insert(
        "last_refresh".into(),
        serde_json::json!(chrono::Utc::now().to_rfc3339()),
    );
    write_json_atomic(path, &auth)
}

fn write_json_atomic(path: &Path, value: &serde_json::Value) -> anyhow::Result<()> {
    let parent = path
        .parent()
        .ok_or_else(|| anyhow::anyhow!("auth path has no parent: {}", path.display()))?;
    fs::create_dir_all(parent)?;
    let tmp = parent.join(format!(".auth.json.tmp.{}", std::process::id()));
    let mut opts = OpenOptions::new();
    opts.write(true).create(true).truncate(true);
    #[cfg(unix)]
    {
        use std::os::unix::fs::OpenOptionsExt;
        opts.mode(0o600);
    }
    let mut file = opts.open(&tmp)?;
    file.write_all(serde_json::to_string_pretty(value)?.as_bytes())?;
    file.flush()?;
    drop(file);
    fs::rename(&tmp, path).inspect_err(|_| {
        let _ = fs::remove_file(&tmp);
    })?;
    Ok(())
}
