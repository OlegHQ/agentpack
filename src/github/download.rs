use std::collections::HashSet;
use std::fs;
use std::io::Cursor;
use std::path::{Path, PathBuf};
use std::time::Duration;

use flate2::read::GzDecoder;
use reqwest::blocking::Client;
use tar::Archive;

use crate::cache::repo_dir_is_package_root;
use crate::error::{AgentpackError, Result};
use crate::ui::Ui;

pub fn download_tarball_bytes(
    client: &Client,
    owner: &str,
    repo: &str,
    commit_sha: &str,
    ui: &Ui,
) -> Result<Vec<u8>> {
    let url = format!("https://codeload.github.com/{owner}/{repo}/tar.gz/{commit_sha}");
    let mut req = client.get(&url);
    if let Ok(token) = std::env::var("GITHUB_TOKEN").or_else(|_| std::env::var("GH_TOKEN")) {
        req = req.header("Authorization", format!("Bearer {token}"));
    }
    let resp = req
        .timeout(Duration::from_secs(300))
        .send()
        .map_err(|e| AgentpackError::Archive(e.to_string()))?;
    if !resp.status().is_success() {
        return Err(AgentpackError::Archive(format!(
            "GET {url} -> {}",
            resp.status()
        )));
    }
    let total = resp.content_length();
    let mut reader = resp;
    let buf = ui
        .read_to_end_with_progress(&mut reader, total, "Download tarball")
        .map_err(|e| AgentpackError::Archive(e.to_string()))?;
    Ok(buf)
}

pub fn collect_repo_relative_paths(buf: &[u8]) -> Result<HashSet<String>> {
    let tar_gz = GzDecoder::new(Cursor::new(buf));
    let mut archive = Archive::new(tar_gz);
    let mut out = HashSet::new();
    for entry in archive
        .entries()
        .map_err(|e| AgentpackError::Archive(e.to_string()))?
    {
        let entry = entry.map_err(|e| AgentpackError::Archive(e.to_string()))?;
        let path = entry
            .path()
            .map_err(|e| AgentpackError::Archive(e.to_string()))?
            .to_string_lossy()
            .to_string();
        let components: Vec<&str> = path.split('/').collect();
        if components.len() < 2 {
            continue;
        }
        let rel_in_repo = components[1..].join("/");
        out.insert(rel_in_repo);
    }
    Ok(out)
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

pub fn extract_tarball_with_prefix(
    buf: &[u8],
    path_prefix: &str,
    out_dir: &Path,
    ui: &Ui,
) -> Result<()> {
    let extract_pb = ui.spinner("Extracting archive…");

    if out_dir.exists() {
        fs::remove_dir_all(out_dir).map_err(|e| AgentpackError::io(out_dir, e))?;
    }
    fs::create_dir_all(out_dir).map_err(|e| AgentpackError::io(out_dir, e))?;

    let tar_gz = GzDecoder::new(Cursor::new(buf));
    let mut archive = Archive::new(tar_gz);

    let path_prefix = path_prefix.trim_matches('/').to_string();
    let prefix_dir = if path_prefix.is_empty() {
        String::new()
    } else {
        format!("{path_prefix}/")
    };

    for entry in archive
        .entries()
        .map_err(|e| AgentpackError::Archive(e.to_string()))?
    {
        let mut entry = entry.map_err(|e| AgentpackError::Archive(e.to_string()))?;
        let path = entry
            .path()
            .map_err(|e| AgentpackError::Archive(e.to_string()))?
            .to_string_lossy()
            .to_string();

        let components: Vec<&str> = path.split('/').collect();
        if components.len() < 2 {
            continue;
        }
        let rel_in_repo = components[1..].join("/");

        let extract_rel = if path_prefix.is_empty() {
            rel_in_repo
        } else if rel_in_repo == path_prefix {
            continue;
        } else if let Some(stripped) = rel_in_repo.strip_prefix(&prefix_dir) {
            stripped.to_string()
        } else {
            continue;
        };

        if extract_rel.is_empty() {
            continue;
        }

        let out_path: PathBuf = out_dir.join(Path::new(&extract_rel));
        if entry.header().entry_type().is_dir() {
            fs::create_dir_all(&out_path).map_err(|e| AgentpackError::io(&out_path, e))?;
        } else {
            if let Some(parent) = out_path.parent() {
                fs::create_dir_all(parent).map_err(|e| AgentpackError::io(parent, e))?;
            }
            let mut f =
                fs::File::create(&out_path).map_err(|e| AgentpackError::io(&out_path, e))?;
            let _: u64 =
                std::io::copy(&mut entry, &mut f).map_err(|e| AgentpackError::io(&out_path, e))?;
        }
    }

    Ui::finish_spinner(extract_pb.as_ref(), "Extracted files");

    Ok(())
}

/// Download a repo tarball and extract a subtree. Pass **`blob_target_file`** when `path_prefix` points at a single file in the repo: the archive is scanned and the deepest enclosing **package root** (`.claude-plugin`, `.cursor-plugin`, `SKILL.md`, or `agentpack.toml`) is extracted instead.
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

    let path_prefix = if let Some(blob) = blob_target_file {
        let index = collect_repo_relative_paths(&buf)?;
        choose_package_prefix_for_blob_path(&index, blob)
            .unwrap_or_else(|| parent_dir_in_repo(blob))
    } else {
        path_prefix.trim_matches('/').to_string()
    };

    extract_tarball_with_prefix(&buf, &path_prefix, out_dir, ui)
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
