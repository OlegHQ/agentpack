//! Codeload (HTTP) tarball sources: anonymous and authenticated.

use std::time::Duration;

use reqwest::blocking::Client;

use crate::error::AgentpackError;
use crate::ui::Ui;

use super::{FetchOutcome, TarballSource};

pub(crate) struct CodeloadAnon {
    client: Client,
}

impl CodeloadAnon {
    pub fn new(client: Client) -> Self {
        Self { client }
    }
}

impl TarballSource for CodeloadAnon {
    fn name(&self) -> &'static str {
        "codeload-anon"
    }

    fn fetch(&self, owner: &str, repo: &str, sha: &str, ui: &Ui) -> FetchOutcome {
        codeload_fetch(&self.client, owner, repo, sha, None, ui, self.name())
    }
}

pub(crate) struct CodeloadAuth {
    client: Client,
    token: String,
}

impl CodeloadAuth {
    pub fn new(client: Client, token: String) -> Self {
        Self { client, token }
    }
}

impl TarballSource for CodeloadAuth {
    fn name(&self) -> &'static str {
        "codeload-auth"
    }

    fn fetch(&self, owner: &str, repo: &str, sha: &str, ui: &Ui) -> FetchOutcome {
        codeload_fetch(
            &self.client,
            owner,
            repo,
            sha,
            Some(&self.token),
            ui,
            self.name(),
        )
    }
}

fn codeload_fetch(
    client: &Client,
    owner: &str,
    repo: &str,
    sha: &str,
    token: Option<&str>,
    ui: &Ui,
    source_name: &'static str,
) -> FetchOutcome {
    let url = format!("https://codeload.github.com/{owner}/{repo}/tar.gz/{sha}");
    let mut req = client.get(&url).timeout(Duration::from_secs(300));
    if let Some(t) = token {
        req = req.header("Authorization", format!("Bearer {t}"));
    }
    let resp = match req.send() {
        Ok(r) => r,
        Err(e) => {
            // All reqwest send-time errors are treated as transient for retry purposes.
            // Real auth/protocol problems surface in the response status, not here.
            return FetchOutcome::Skip(format!("network: {e}"));
        }
    };
    let status = resp.status();
    match status.as_u16() {
        200..=299 => {
            let total = resp.content_length();
            let mut reader = resp;
            match ui.read_to_end_with_progress(&mut reader, total, "Download tarball") {
                Ok(buf) => FetchOutcome::Ok(buf),
                Err(e) => FetchOutcome::Skip(format!("network: read body: {e}")),
            }
        }
        401 => FetchOutcome::Skip(format!("auth required (401) via {source_name}")),
        403 => FetchOutcome::Skip(format!("forbidden (403) via {source_name}")),
        404 => FetchOutcome::Skip(format!(
            "not found (404) via {source_name} — private repo, token lacking scope, or codeload cache miss"
        )),
        429 => FetchOutcome::Skip(format!("rate limit (429) via {source_name}")),
        s if (500..600).contains(&s) => FetchOutcome::Skip(format!("server {s} via {source_name}")),
        other => FetchOutcome::Fatal(AgentpackError::Archive(format!(
            "GET {url} -> {other} via {source_name}"
        ))),
    }
}
