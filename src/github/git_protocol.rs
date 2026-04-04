use std::fs;
use std::path::PathBuf;

use gix::bstr::ByteSlice;

use crate::error::{AgentpackError, Result};
use crate::paths;

#[derive(Debug, Clone, PartialEq, Eq)]
struct RemoteRefInfo {
    full_ref_name: String,
    target: Option<String>,
    peeled: Option<String>,
}

// Facade over the embedded git transport so REST callers can ask for refs/tags without knowing
// about repository bootstrap or the git protocol.
pub(crate) struct GitProtocolClient;

impl GitProtocolClient {
    pub(crate) fn resolve_ref_to_sha(owner: &str, repo: &str, git_ref: &str) -> Result<String> {
        let refs = load_remote_refs(owner, repo)?;
        resolve_sha_from_remote_refs(&refs, git_ref).ok_or_else(|| {
            AgentpackError::GitHubApi(format!(
                "git protocol fallback could not resolve ref {git_ref} for {owner}/{repo}"
            ))
        })
    }

    pub(crate) fn list_tags(owner: &str, repo: &str) -> Result<Vec<(String, String)>> {
        let refs = load_remote_refs(owner, repo)?;
        Ok(tag_pairs_from_remote_refs(&refs))
    }
}

fn load_remote_refs(owner: &str, repo: &str) -> Result<Vec<RemoteRefInfo>> {
    let helper_repo = helper_repo()?;
    let remote_url = format!("https://github.com/{owner}/{repo}.git");
    let remote = helper_repo.remote_at(remote_url.as_str()).map_err(|e| {
        AgentpackError::GitHubApi(format!("gix remote setup for {remote_url}: {e}"))
    })?;
    let connection = remote
        .connect(gix::remote::Direction::Fetch)
        .map_err(|e| AgentpackError::GitHubApi(format!("gix connect for {remote_url}: {e}")))?;
    let (ref_map, _handshake) = connection
        .ref_map(gix::progress::Discard, Default::default())
        .map_err(|e| AgentpackError::GitHubApi(format!("gix ls-refs for {remote_url}: {e}")))?;

    Ok(ref_map
        .remote_refs
        .into_iter()
        .map(|remote_ref| {
            let (name, target, peeled) = remote_ref.unpack();
            RemoteRefInfo {
                full_ref_name: name.to_str_lossy().into_owned(),
                target: target.map(|oid| oid.to_string()),
                peeled: peeled.map(|oid| oid.to_string()),
            }
        })
        .collect())
}

fn resolve_sha_from_remote_refs(refs: &[RemoteRefInfo], git_ref: &str) -> Option<String> {
    if git_ref.len() == 40 && git_ref.chars().all(|c| c.is_ascii_hexdigit()) {
        return Some(git_ref.to_lowercase());
    }

    if git_ref == "HEAD" {
        return refs
            .iter()
            .find(|remote_ref| remote_ref.full_ref_name == "HEAD")
            .and_then(remote_ref_sha);
    }

    let wanted_names = [
        git_ref.to_string(),
        format!("refs/heads/{git_ref}"),
        format!("refs/tags/{git_ref}"),
    ];

    refs.iter()
        .find(|remote_ref| {
            wanted_names
                .iter()
                .any(|wanted| wanted == &remote_ref.full_ref_name)
        })
        .and_then(remote_ref_sha)
}

fn tag_pairs_from_remote_refs(refs: &[RemoteRefInfo]) -> Vec<(String, String)> {
    let mut tags: Vec<(String, String)> = refs
        .iter()
        .filter(|remote_ref| remote_ref.full_ref_name.starts_with("refs/tags/"))
        .filter_map(|remote_ref| {
            remote_ref_sha(remote_ref).map(|sha| {
                (
                    remote_ref
                        .full_ref_name
                        .trim_start_matches("refs/tags/")
                        .to_string(),
                    sha,
                )
            })
        })
        .collect();
    tags.sort_by(|a, b| a.0.cmp(&b.0));
    tags.dedup_by(|left, right| left.0 == right.0 && left.1 == right.1);
    tags
}

fn remote_ref_sha(remote_ref: &RemoteRefInfo) -> Option<String> {
    remote_ref
        .peeled
        .as_ref()
        .or(remote_ref.target.as_ref())
        .map(|sha| sha.to_lowercase())
}

fn helper_repo() -> Result<gix::Repository> {
    let repo_dir = helper_repo_dir()?;
    if repo_dir.is_dir() {
        return gix::open(&repo_dir)
            .map_err(|e| AgentpackError::GitHubApi(format!("open git protocol helper repo: {e}")));
    }

    if let Some(parent) = repo_dir.parent() {
        fs::create_dir_all(parent).map_err(|e| AgentpackError::io(parent, e))?;
    }
    gix::init_bare(&repo_dir)
        .map_err(|e| AgentpackError::GitHubApi(format!("init git protocol helper repo: {e}")))
}

fn helper_repo_dir() -> Result<PathBuf> {
    Ok(paths::ensure_user_agentpack_layout()?
        .join("git-protocol")
        .join("probe.git"))
}

#[cfg(test)]
mod tests {
    use super::{resolve_sha_from_remote_refs, tag_pairs_from_remote_refs, RemoteRefInfo};

    #[test]
    fn resolves_head_and_named_refs() {
        let refs = vec![
            RemoteRefInfo {
                full_ref_name: "HEAD".into(),
                target: Some("a".repeat(40)),
                peeled: None,
            },
            RemoteRefInfo {
                full_ref_name: "refs/heads/main".into(),
                target: Some("b".repeat(40)),
                peeled: None,
            },
            RemoteRefInfo {
                full_ref_name: "refs/tags/v1.0.0".into(),
                target: Some("c".repeat(40)),
                peeled: Some("d".repeat(40)),
            },
        ];

        assert_eq!(
            resolve_sha_from_remote_refs(&refs, "HEAD").as_deref(),
            Some("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
        );
        assert_eq!(
            resolve_sha_from_remote_refs(&refs, "main").as_deref(),
            Some("bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
        );
        assert_eq!(
            resolve_sha_from_remote_refs(&refs, "v1.0.0").as_deref(),
            Some("dddddddddddddddddddddddddddddddddddddddd")
        );
    }

    #[test]
    fn extracts_tag_pairs_with_peeled_targets() {
        let refs = vec![
            RemoteRefInfo {
                full_ref_name: "refs/tags/v2.0.0".into(),
                target: Some("a".repeat(40)),
                peeled: Some("b".repeat(40)),
            },
            RemoteRefInfo {
                full_ref_name: "refs/tags/v1.0.0".into(),
                target: Some("c".repeat(40)),
                peeled: None,
            },
            RemoteRefInfo {
                full_ref_name: "refs/heads/main".into(),
                target: Some("d".repeat(40)),
                peeled: None,
            },
        ];

        assert_eq!(
            tag_pairs_from_remote_refs(&refs),
            vec![
                ("v1.0.0".into(), "c".repeat(40)),
                ("v2.0.0".into(), "b".repeat(40)),
            ]
        );
    }
}
