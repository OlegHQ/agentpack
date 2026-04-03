use std::time::Duration;

use reqwest::blocking::Client;
use serde::Deserialize;

use crate::error::{AgentpackError, Result};

#[derive(Deserialize)]
struct CommitBody {
    sha: String,
}

/// Resolve branch/tag/short SHA to full 40-char lowercase hex commit SHA.
pub fn resolve_ref_to_sha(
    client: &Client,
    owner: &str,
    repo: &str,
    git_ref: &str,
) -> Result<String> {
    let url = if git_ref == "HEAD" {
        format!("https://api.github.com/repos/{owner}/{repo}/commits/HEAD")
    } else {
        format!("https://api.github.com/repos/{owner}/{repo}/commits/{git_ref}")
    };
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
    let body: CommitBody = resp
        .json()
        .map_err(|e| AgentpackError::GitHubApi(format!("json: {e}")))?;
    let sha = body.sha.trim().to_lowercase();
    if sha.len() != 40 {
        return Err(AgentpackError::GitHubApi(format!(
            "unexpected sha length: {sha}"
        )));
    }
    Ok(sha)
}

#[derive(Deserialize)]
struct ApiRate {
    rate: RateLimit,
}

#[derive(Deserialize)]
struct RateLimit {
    remaining: u32,
}

pub fn check_rate_limit_hint(client: &Client) {
    let Ok(resp) = client
        .get("https://api.github.com/rate_limit")
        .header("Accept", "application/vnd.github+json")
        .timeout(Duration::from_secs(30))
        .send()
    else {
        return;
    };
    if !resp.status().is_success() {
        return;
    }
    let Ok(body) = resp.json::<ApiRate>() else {
        return;
    };
    if body.rate.remaining < 10 {
        tracing::warn!(
            remaining = body.rate.remaining,
            "GitHub API rate limit nearly exhausted; set GITHUB_TOKEN"
        );
    }
}
