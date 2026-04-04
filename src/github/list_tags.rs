//! List repository tags for semver resolution.

use std::time::Duration;

use reqwest::blocking::Client;
use serde::Deserialize;

use crate::error::{AgentpackError, Result};

use super::git_protocol::GitProtocolClient;
use super::metadata_cache::{CachedTags, GitHubMetadataCache};
use super::resolve_ref::warn_on_low_rate_limit;

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
    let cached = GitHubMetadataCache::load_tags(owner, repo)?;
    if let Some(entry) = cached
        .as_ref()
        .filter(|entry| GitHubMetadataCache::tags_are_fresh(entry))
    {
        return Ok(entry.tags.clone());
    }

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
        .map_err(|e| AgentpackError::GitHubApi(e.to_string()));
    let resp = match resp {
        Ok(resp) => resp,
        Err(err) => return git_protocol_or_stale_tags_fallback(cached.as_ref(), owner, repo, err),
    };
    let status = resp.status();
    if !status.is_success() {
        let body = resp.text().unwrap_or_default();
        return git_protocol_or_stale_tags_fallback(
            cached.as_ref(),
            owner,
            repo,
            AgentpackError::GitHubApi(format!(
                "GET {url} -> {status}: {}",
                crate::fs_util::truncate_str(&body, 500)
            )),
        );
    }
    warn_on_low_rate_limit(resp.headers());
    let body: Vec<TagRef> = resp
        .json()
        .map_err(|e| AgentpackError::GitHubApi(format!("tags json: {e}")))?;
    let mut out: Vec<(String, String)> = body
        .into_iter()
        .map(|t| (t.name, t.commit.sha.to_lowercase()))
        .collect();
    out.sort_by(|a, b| a.0.cmp(&b.0));
    GitHubMetadataCache::store_tags(owner, repo, &out)?;
    Ok(out)
}

fn git_protocol_or_stale_tags_fallback(
    cached: Option<&CachedTags>,
    owner: &str,
    repo: &str,
    error: AgentpackError,
) -> Result<Vec<(String, String)>> {
    tracing::warn!(
        owner,
        repo,
        error = %error,
        "GitHub REST tag listing failed; trying git protocol fallback"
    );
    match GitProtocolClient::list_tags(owner, repo) {
        Ok(tags) => {
            GitHubMetadataCache::store_tags(owner, repo, &tags)?;
            return Ok(tags);
        }
        Err(gix_error) => {
            tracing::warn!(
                owner,
                repo,
                error = %gix_error,
                "Git protocol fallback failed; considering stale cached tags"
            );
        }
    }
    if let Some(entry) = cached {
        tracing::warn!(
            owner,
            repo,
            tags = entry.tags.len(),
            error = %error,
            "GitHub tag listing failed; using stale cached tags"
        );
        return Ok(entry.tags.clone());
    }
    Err(error)
}

#[cfg(test)]
mod tests {
    use reqwest::blocking::Client;
    use serial_test::serial;
    use tempfile::tempdir;

    use crate::github::metadata_cache::GitHubMetadataCache;

    use super::list_tags;

    #[test]
    #[serial]
    fn list_tags_uses_cached_value_without_network() {
        let dir = tempdir().unwrap();
        std::env::set_var("AGENTPACK_HOME", dir.path());
        let expected = vec![("v1.2.3".to_string(), "b".repeat(40))];
        GitHubMetadataCache::store_tags("owner", "repo", &expected).unwrap();

        let client = Client::builder().build().unwrap();
        let tags = list_tags(&client, "owner", "repo").unwrap();
        assert_eq!(tags, expected);
    }
}
