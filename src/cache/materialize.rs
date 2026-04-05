use std::fs;
use std::path::Path;

use reqwest::blocking::Client;

use crate::error::{AgentpackError, Result};
use crate::github::{
    archive_no_files_for_repo_path, canonical_github_tree_url, choose_package_prefix_for_blob_path,
    collect_repo_relative_paths, download_tarball_bytes, extract_tarball_with_prefix,
    parent_dir_in_repo, parse_github_url, path_in_repo_looks_like_file, resolve_ref_to_sha,
    GitHubSource,
};
use crate::paths;
use crate::ui::Ui;

use super::asset::{classify_materialized, FetchedGithubAsset};
use super::layout::{cache_dir_is_package_root_in_filesystem, cache_entry_dir, compute_cache_key};

/// Parent directories of a repo-relative file path, deepest first, ending at repo root (`""`).
pub(crate) fn blob_path_parent_prefixes(blob_file_path: &str) -> Vec<String> {
    let path = blob_file_path.trim_matches('/');
    let mut prefixes = Vec::new();
    let mut current = Path::new(path).parent();
    while let Some(dir) = current {
        let trimmed = dir.to_string_lossy().trim_matches('/').to_string();
        if trimmed.is_empty() {
            break;
        }
        prefixes.push(trimmed);
        current = dir.parent();
    }
    prefixes.push(String::new());
    prefixes
}

/// Whether **`owner` / `repo` / `path_prefix` / `commit`** already has a plugin or skill tree in `AGENTPACK_HOME/cache`.
fn github_prefix_cache_ready(owner: &str, repo: &str, commit: &str, path_prefix: &str) -> bool {
    let effective = GitHubSource {
        owner: owner.to_string(),
        repo: repo.to_string(),
        git_ref: "HEAD".into(),
        path: path_prefix.trim_matches('/').to_string(),
    };
    let identity = crate::github::normalized_identity(&effective, commit);
    let cache_key = compute_cache_key(&identity);
    let Ok(out) = cache_entry_dir(&cache_key) else {
        return false;
    };
    cache_dir_is_package_root_in_filesystem(&out)
}

fn git_ref_is_full_commit_sha(git_ref: &str) -> bool {
    git_ref.len() == 40 && git_ref.chars().all(|c| c.is_ascii_hexdigit())
}

/// Pin ref, download if needed, detect full plugin vs skill at cache root.
pub fn materialize_github_tree(
    client: &Client,
    source: &GitHubSource,
    display_url: &str,
    ui: &Ui,
) -> Result<FetchedGithubAsset> {
    paths::ensure_user_agentpack_layout()?;
    let commit = if git_ref_is_full_commit_sha(&source.git_ref) {
        source.git_ref.to_lowercase()
    } else {
        let spinner = ui.spinner("Resolve Git ref → commit SHA");
        let c = resolve_ref_to_sha(client, &source.owner, &source.repo, &source.git_ref)?;
        Ui::finish_spinner(
            spinner.as_ref(),
            format!("Pinned {}…{}", &c[..4], &c[c.len() - 4..]),
        );
        c
    };

    let blob_file = display_url.contains("/blob/") && path_in_repo_looks_like_file(&source.path);

    let mut effective = source.clone();
    let mut prefetched_tarball: Option<Vec<u8>> = None;

    if blob_file {
        let mut cached_prefix = None;
        for prefix in blob_path_parent_prefixes(&source.path) {
            if github_prefix_cache_ready(&source.owner, &source.repo, &commit, &prefix) {
                cached_prefix = Some(prefix);
                break;
            }
        }
        effective.path = if let Some(prefix) = cached_prefix {
            prefix
        } else {
            prefetched_tarball = Some(download_tarball_bytes(
                client,
                &source.owner,
                &source.repo,
                &commit,
                ui,
            )?);
            let index = collect_repo_relative_paths(
                prefetched_tarball
                    .as_ref()
                    .expect("prefetched tarball set for blob path"),
            )?;
            choose_package_prefix_for_blob_path(&index, &source.path)
                .unwrap_or_else(|| parent_dir_in_repo(&source.path))
        };
    } else {
        effective.path = source.path.trim_matches('/').to_string();
    }

    let identity = crate::github::normalized_identity(&effective, &commit);
    let cache_key = compute_cache_key(&identity);
    let out = cache_entry_dir(&cache_key)?;
    let cache_ready = cache_dir_is_package_root_in_filesystem(&out);

    if !cache_ready {
        let cache_dir = paths::cache_dir()?;
        fs::create_dir_all(&cache_dir).map_err(|err| AgentpackError::io(&cache_dir, err))?;
        let tarball = match prefetched_tarball {
            Some(bytes) => bytes,
            None => download_tarball_bytes(client, &source.owner, &source.repo, &commit, ui)?,
        };
        let n = extract_tarball_with_prefix(&tarball, &effective.path, &out, ui)?;
        if n == 0 && !effective.path.is_empty() {
            return Err(archive_no_files_for_repo_path(
                &effective.owner,
                &effective.repo,
                &commit,
                &effective.path,
            ));
        }
    }

    let display = canonical_github_tree_url(&effective);
    classify_materialized(&out, &display, &effective, commit, cache_key)
}

pub fn fetch_github_asset_from_url(
    client: &Client,
    raw_url: &str,
    ui: &Ui,
) -> Result<FetchedGithubAsset> {
    let parsed = parse_github_url(raw_url)?;
    materialize_github_tree(client, &parsed, raw_url, ui)
}

#[cfg(test)]
mod commit_ref_tests {
    use super::git_ref_is_full_commit_sha;

    #[test]
    fn full_commit_sha_detection() {
        assert!(git_ref_is_full_commit_sha(&"a".repeat(40)));
        assert!(git_ref_is_full_commit_sha(
            "0000000000000000000000000000000000000000"
        ));
        assert!(!git_ref_is_full_commit_sha("main"));
        assert!(!git_ref_is_full_commit_sha(&"a".repeat(39)));
        assert!(!git_ref_is_full_commit_sha(&"g".repeat(40)));
    }
}
