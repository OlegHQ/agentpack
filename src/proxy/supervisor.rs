use std::path::Path;
use std::process::Command;
use std::sync::Arc;
use std::thread::JoinHandle;
use std::time::Duration;
use std::time::{SystemTime, UNIX_EPOCH};

use anyhow::Context;
use claude_code_proxy::server::{ProxyConfig, ProxyServer};

use super::auth::UpstreamAuth;
use crate::ui::Ui;

pub(crate) struct RunningProxy {
    base_url: String,
    token: String,
    join: Option<JoinHandle<()>>,
}

impl RunningProxy {
    pub(crate) fn apply_claude_env(&self, cmd: &mut Command) {
        cmd.env("ANTHROPIC_BASE_URL", &self.base_url);
        cmd.env("ANTHROPIC_AUTH_TOKEN", &self.token);
        cmd.env_remove("ANTHROPIC_API_KEY");
        cmd.env("ANTHROPIC_DEFAULT_OPUS_MODEL", "claude-opus-4-7");
        cmd.env("ANTHROPIC_DEFAULT_SONNET_MODEL", "claude-sonnet-4-6");
        cmd.env("ANTHROPIC_DEFAULT_HAIKU_MODEL", "claude-haiku-4-5");
        cmd.env("ANTHROPIC_SMALL_FAST_MODEL", "claude-haiku-4-5");
        cmd.env("CLAUDE_CODE_ENABLE_GATEWAY_MODEL_DISCOVERY", "1");
    }

    pub(crate) fn shutdown(mut self) {
        if let Ok(client) = reqwest::blocking::Client::builder()
            .timeout(Duration::from_secs(2))
            .build()
        {
            let _ = client
                .get(format!("{}/__agentpack/shutdown", self.base_url))
                .send();
        }
        if let Some(join) = self.join.take() {
            let _ = join.join();
        }
    }
}

pub(crate) fn start(project_root: &Path, ui: &Ui) -> anyhow::Result<RunningProxy> {
    let token = make_client_token();
    let auth = Arc::new(UpstreamAuth::load()?);
    let mut config = ProxyConfig::from_env(token.clone());
    if std::env::var("AGENTPACK_PROXY_TRANSPORT").is_err() {
        config.transport = auth.default_transport();
    }
    config.diagnostics.log_dir = Some(crate::paths::proxy_log_dir(project_root)?);
    let server = Arc::new(ProxyServer::bind(config, auth)?);
    let base_url = server.base_url();
    ui.debug_message(format!("Claude proxy listening on {base_url}"));
    if let Some(path) = server.diagnostics_path() {
        ui.debug_message(format!("Claude proxy log: {}", path.display()));
    }
    let join = server.run_in_thread().context("start Claude proxy")?;
    Ok(RunningProxy {
        base_url,
        token,
        join: Some(join),
    })
}

fn make_client_token() -> String {
    let nanos = SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map(|d| d.as_nanos())
        .unwrap_or_default();
    format!("agentpack-proxy-{}-{nanos}", std::process::id())
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn claude_proxy_env_uses_token_without_api_key_conflict() {
        let proxy = RunningProxy {
            base_url: "http://127.0.0.1:1234".into(),
            token: "proxy-token".into(),
            join: None,
        };
        let mut cmd = Command::new("claude");
        cmd.env("ANTHROPIC_API_KEY", "old-key");
        proxy.apply_claude_env(&mut cmd);

        let envs: Vec<_> = cmd
            .get_envs()
            .map(|(key, value)| {
                (
                    key.to_string_lossy().into_owned(),
                    value.map(|v| v.to_string_lossy().into_owned()),
                )
            })
            .collect();
        assert!(envs.contains(&("ANTHROPIC_AUTH_TOKEN".into(), Some("proxy-token".into()))));
        assert!(envs.contains(&("ANTHROPIC_API_KEY".into(), None)));
    }
}
