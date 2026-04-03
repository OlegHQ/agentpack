//! List repository tags for semver resolution.

use std::time::Duration;

use reqwest::blocking::Client;
use serde::Deserialize;

use crate::error::{AgentpackError, Result};

#[derive(Deserialize)]
struct TagRef {
    name: String,
    commit: TagCommit,
}

#[derive(Deserialize)]
struct TagCommit {
    sha: String,
}

/// `(tag_name, commit_sha)` for each tag (API returns newest-first-ish; we sort in caller).
pub fn list_tags(client: &Client, owner: &str, repo: &str) -> Result<Vec<(String, String)>> {
    let url = format!("https://api.github.com/repos/{owner}/{repo}/tags?per_page=100");
    let mut req = client
        .get(&url)
        .header("Accept", "application/vnd.github+json");
    if let Ok(token) = std::env::var("GITHUB_TOKEN").or_else(|_| std::env::var("GH_TOKEN")) {
        req = req.header("Authorization", format!("Bearer {token}"));
    }
    let resp = req
        .timeout(Duration::from_secs(120))
        .send()
        .map_err(|e| AgentpackError::GitHubApi(e.to_string()))?;
    let status = resp.status();
    if !status.is_success() {
        let body = resp.text().unwrap_or_default();
        return Err(AgentpackError::GitHubApi(format!(
            "GET {url} -> {status}: {}",
            body.chars().take(500).collect::<String>()
        )));
    }
    let body: Vec<TagRef> = resp
        .json()
        .map_err(|e| AgentpackError::GitHubApi(format!("tags json: {e}")))?;
    let mut out: Vec<(String, String)> = body
        .into_iter()
        .map(|t| (t.name, t.commit.sha.to_lowercase()))
        .collect();
    out.sort_by(|a, b| a.0.cmp(&b.0));
    Ok(out)
}
