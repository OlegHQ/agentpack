//! Download orchestration and repo-path helpers.
//!
//! Tar.gz decoding/extraction lives in [`super::extract`]; this module owns the fetch chain,
//! the blob-vs-tree path reasoning, and the high-level [`download_and_extract`] entry point.

use std::collections::HashSet;
use std::path::Path;

use reqwest::blocking::Client;

use crate::cache::repo_dir_is_package_root;
use crate::error::{AgentpackError, Result};
use crate::ui::Ui;

use super::extract::{
    collect_paths_and_entries, extract_tarball_with_prefix, write_entries_with_prefix,
};
use super::tarball_sources::{default_chain, run_chain};

/// Fetch `<owner>/<repo>@<commit_sha>` as a codeload-shaped `tar.gz` byte buffer.
///
/// Delegates to a chain of [`super::tarball_sources::TarballSource`]
/// strategies: anonymous codeload → authenticated codeload (only if a token
/// is set) → gix git-protocol clone. Anonymous-first deliberately sidesteps
/// codeload's "fine-grained PAT → 404 instead of 401" trap that breaks
/// public-repo fetches when an unrelated `GITHUB_TOKEN` is in the environment.
pub fn download_tarball_bytes(
    client: &Client,
    owner: &str,
    repo: &str,
    commit_sha: &str,
    ui: &Ui,
) -> Result<Vec<u8>> {
    let chain = default_chain(client);
    run_chain(&chain, owner, repo, commit_sha, ui)
}

/// Repo-relative blob file path (`plugins/foo/agents/bar.md`): walk parent dirs and return the deepest that is a package root in the archive index.
pub fn choose_package_prefix_for_blob_path(
    index: &HashSet<String>,
    blob_file_path: &str,
) -> Option<String> {
    let path = blob_file_path.trim_matches('/');
    if path.is_empty() {
        return None;
    }
    let mut cur = Path::new(path).parent();
    while let Some(dir) = cur {
        let trimmed = dir.to_string_lossy().trim_matches('/').to_string();
        if repo_dir_is_package_root(index, &trimmed) {
            return Some(trimmed);
        }
        cur = dir.parent();
    }
    if repo_dir_is_package_root(index, "") {
        return Some(String::new());
    }
    None
}

/// `true` when the last path segment looks like a file (has an extension). Excludes `SKILL.md` (blob URLs are normalized to the skill directory before this runs).
pub fn path_in_repo_looks_like_file(path: &str) -> bool {
    let p = Path::new(path.trim_matches('/'));
    let Some(name) = p.file_name().and_then(|n| n.to_str()) else {
        return false;
    };
    if name == "SKILL.md" {
        return false;
    }
    p.extension().is_some()
}

pub fn parent_dir_in_repo(path: &str) -> String {
    Path::new(path.trim_matches('/'))
        .parent()
        .map(|p| p.to_string_lossy().trim_matches('/').to_string())
        .unwrap_or_default()
}

pub fn archive_no_files_for_repo_path(
    owner: &str,
    repo: &str,
    commit_sha: &str,
    path_prefix: &str,
) -> AgentpackError {
    let short = commit_sha
        .get(..8)
        .filter(|s| !s.is_empty())
        .unwrap_or(commit_sha);
    AgentpackError::Archive(format!(
        "no files matched repository path {path_prefix:?} in {owner}/{repo} archive at {short} — \
the directory may not exist at this commit or may have been renamed/moved (update the dependency path or pin an older commit)"
    ))
}

/// Download a repo tarball and extract a subtree. Pass **`blob_target_file`** when `path_prefix` points at a single file in the repo: the archive is scanned and the deepest enclosing **package root** (`.claude-plugin`, `.cursor-plugin`, `SKILL.md`, or `agentpack.toml`) is extracted instead.
///
/// When `blob_target_file` is set, uses a single-decompression strategy: the tarball is decoded
/// once, entries are buffered in memory, the prefix is determined from the path index, and only
/// matching entries are written to disk.
#[allow(clippy::too_many_arguments)]
pub fn download_and_extract(
    client: &Client,
    owner: &str,
    repo: &str,
    commit_sha: &str,
    path_prefix: &str,
    out_dir: &Path,
    ui: &Ui,
    blob_target_file: Option<&str>,
) -> Result<()> {
    let buf = download_tarball_bytes(client, owner, repo, commit_sha, ui)?;

    if let Some(blob) = blob_target_file {
        // Single-pass: decompress once, collect paths and entry data simultaneously,
        // then determine prefix and write matching entries.
        let (index, entries) = collect_paths_and_entries(&buf)?;
        let prefix = choose_package_prefix_for_blob_path(&index, blob)
            .unwrap_or_else(|| parent_dir_in_repo(blob));
        let n = write_entries_with_prefix(&entries, &prefix, out_dir, ui)?;
        if n == 0 && !prefix.is_empty() {
            return Err(archive_no_files_for_repo_path(
                owner, repo, commit_sha, &prefix,
            ));
        }
        return Ok(());
    }

    let path_prefix = path_prefix.trim_matches('/').to_string();
    let n = extract_tarball_with_prefix(&buf, &path_prefix, out_dir, ui)?;
    if n == 0 && !path_prefix.is_empty() {
        return Err(archive_no_files_for_repo_path(
            owner,
            repo,
            commit_sha,
            &path_prefix,
        ));
    }
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn chooses_deepest_package_root_for_nested_file() {
        let mut idx = HashSet::new();
        idx.insert("plugins/code-simplifier/.claude-plugin/plugin.json".into());
        idx.insert("plugins/code-simplifier/agents/code-simplifier.md".into());
        let p = choose_package_prefix_for_blob_path(
            &idx,
            "plugins/code-simplifier/agents/code-simplifier.md",
        );
        assert_eq!(p.as_deref(), Some("plugins/code-simplifier"));
    }

    #[test]
    fn skill_only_root_is_detected() {
        let mut idx = HashSet::new();
        idx.insert("skills/foo/SKILL.md".into());
        idx.insert("skills/foo/extra.md".into());
        let p = choose_package_prefix_for_blob_path(&idx, "skills/foo/extra.md");
        assert_eq!(p.as_deref(), Some("skills/foo"));
    }
}
