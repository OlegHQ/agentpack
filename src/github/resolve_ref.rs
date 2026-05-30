use std::time::Duration;

use reqwest::blocking::Client;
use reqwest::header::HeaderMap;
use serde::Deserialize;

use crate::error::{AgentpackError, Result};

use super::fetch::try_git_protocol;
use super::git_protocol::GitProtocolClient;
use super::metadata_cache::{CachedRef, GitHubMetadataCache};

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
    if git_ref.len() == 40 && git_ref.chars().all(|c| c.is_ascii_hexdigit()) {
        return Ok(git_ref.to_lowercase());
    }

    let cached = GitHubMetadataCache::load_ref(owner, repo, git_ref)?;
    if let Some(entry) = cached
        .as_ref()
        .filter(|entry| GitHubMetadataCache::ref_is_fresh(entry))
    {
        return Ok(entry.sha.clone());
    }
    if let Some(tags) =
        GitHubMetadataCache::load_tags(owner, repo)?.filter(GitHubMetadataCache::tags_are_fresh)
    {
        if let Some((_, sha)) = tags.tags.iter().find(|(name, _)| name == git_ref) {
            return Ok(sha.clone());
        }
    }

    let url = if git_ref == "HEAD" {
        format!("https://api.github.com/repos/{owner}/{repo}/commits/HEAD")
    } else {
        format!("https://api.github.com/repos/{owner}/{repo}/commits/{git_ref}")
    };
    let mut req = client
        .get(&url)
        .header("Accept", "application/vnd.github+json");
    if let Some(token) = super::github_token() {
        req = req.header("Authorization", format!("Bearer {token}"));
    }
    let resp = req
        .timeout(Duration::from_secs(120))
        .send()
        .map_err(|e| AgentpackError::GitHubApi(e.to_string()));
    let resp = match resp {
        Ok(resp) => resp,
        Err(err) => {
            return git_protocol_or_stale_ref_fallback(
                cached.as_ref(),
                owner,
                repo,
                git_ref,
                err,
            );
        }
    };
    let status = resp.status();
    if !status.is_success() {
        let body = resp.text().unwrap_or_default();
        return git_protocol_or_stale_ref_fallback(
            cached.as_ref(),
            owner,
            repo,
            git_ref,
            AgentpackError::GitHubApi(format!(
                "GET {url} -> {status}: {}",
                crate::fs_util::truncate_str(&body, 500)
            )),
        );
    }
    warn_on_low_rate_limit(resp.headers());
    let body: CommitBody = resp
        .json()
        .map_err(|e| AgentpackError::GitHubApi(format!("json: {e}")))?;
    let sha = body.sha.trim().to_lowercase();
    if sha.len() != 40 {
        return Err(AgentpackError::GitHubApi(format!(
            "unexpected sha length: {sha}"
        )));
    }
    GitHubMetadataCache::store_ref(owner, repo, git_ref, &sha)?;
    Ok(sha)
}

fn git_protocol_or_stale_ref_fallback(
    cached: Option<&CachedRef>,
    owner: &str,
    repo: &str,
    git_ref: &str,
    error: AgentpackError,
) -> Result<String> {
    match try_git_protocol(owner, repo, git_ref, error, || {
        let sha = GitProtocolClient::resolve_ref_to_sha(owner, repo, git_ref)?;
        GitHubMetadataCache::store_ref(owner, repo, git_ref, &sha)?;
        Ok(sha)
    }) {
        Ok(sha) => Ok(sha),
        Err(rest_error) => {
            if let Some(entry) = cached {
                tracing::warn!(
                    owner,
                    repo,
                    git_ref,
                    sha = %entry.sha,
                    "GitHub ref resolution failed; using stale cached ref"
                );
                Ok(entry.sha.clone())
            } else {
                Err(rest_error)
            }
        }
    }
}

pub(crate) fn warn_on_low_rate_limit(headers: &HeaderMap) {
    let Some(remaining) = headers.get("x-ratelimit-remaining") else {
        return;
    };
    let Ok(remaining) = remaining.to_str() else {
        return;
    };
    let Ok(remaining) = remaining.parse::<u32>() else {
        return;
    };
    if remaining < 10 {
        tracing::warn!(
            remaining,
            "GitHub API rate limit nearly exhausted; set GITHUB_TOKEN"
        );
    }
}

#[cfg(test)]
mod tests {
    use reqwest::blocking::Client;
    use serial_test::serial;
    use tempfile::tempdir;

    use crate::github::metadata_cache::GitHubMetadataCache;

    use super::resolve_ref_to_sha;

    #[test]
    #[serial]
    fn resolve_ref_uses_cached_value_without_network() {
        let dir = tempdir().unwrap();
        std::env::set_var("AGENTPACK_HOME", dir.path());
        GitHubMetadataCache::store_ref("owner", "repo", "main", &"a".repeat(40)).unwrap();

        let client = Client::builder().build().unwrap();
        let sha = resolve_ref_to_sha(&client, "owner", "repo", "main").unwrap();
        assert_eq!(sha, "a".repeat(40));
    }

    #[test]
    #[serial]
    fn resolve_ref_uses_fresh_cached_tags_for_exact_tag_names() {
        let dir = tempdir().unwrap();
        std::env::set_var("AGENTPACK_HOME", dir.path());
        GitHubMetadataCache::store_tags("owner", "repo", &[("v1.2.3".into(), "b".repeat(40))])
            .unwrap();

        let client = Client::builder().build().unwrap();
        let sha = resolve_ref_to_sha(&client, "owner", "repo", "v1.2.3").unwrap();
        assert_eq!(sha, "b".repeat(40));
    }
}
