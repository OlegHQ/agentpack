use url::Url;

use crate::error::{AgentpackError, Result};
use crate::github::{DEFAULT_GIT_REF, GITHUB_HOST};

/// Parsed GitHub repository pointer: branch/tag/commit + path within repo.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct GitHubSource {
    pub owner: String,
    pub repo: String,
    /// Ref as it appears in the URL (branch name, tag, or commit sha).
    pub git_ref: String,
    /// Directory path within the repo (no leading slash, use "" for repo root).
    pub path: String,
}

fn strip_git_suffix(segment: &str) -> String {
    segment.trim_end_matches(".git").to_string()
}

/// `owner` / `repo[/path...]` from golden-spec shorthand (segments split on `/`).
pub fn github_source_from_segments(owner: &str, repo: &str, in_repo_path: &str) -> GitHubSource {
    github_source_from_segments_ref(owner, repo, in_repo_path, "HEAD")
}

/// Like [`github_source_from_segments`] but pins an explicit ref (branch/tag/commit) instead of
/// defaulting to `HEAD`. Used by `add owner/repo[/path]@<ref>`.
pub fn github_source_from_segments_ref(
    owner: &str,
    repo: &str,
    in_repo_path: &str,
    git_ref: &str,
) -> GitHubSource {
    let git_ref = git_ref.trim();
    GitHubSource {
        owner: owner.to_string(),
        repo: repo.to_string(),
        git_ref: if git_ref.is_empty() {
            DEFAULT_GIT_REF.to_string()
        } else {
            git_ref.to_string()
        },
        path: in_repo_path.to_string(),
    }
}

/// Canonical `https://github.com/.../tree/<ref>/path` for lockfile display.
pub fn canonical_github_tree_url(source: &GitHubSource) -> String {
    let path = source.path.trim_matches('/');
    if path.is_empty() {
        format!(
            "https://github.com/{}/{}/tree/{}",
            source.owner, source.repo, source.git_ref
        )
    } else {
        format!(
            "https://github.com/{}/{}/tree/{}/{}",
            source.owner, source.repo, source.git_ref, path
        )
    }
}

/// Parse `https://github.com/owner/repo/tree/ref/rest/of/path` or `/blob/ref/...`.
pub fn parse_github_url(raw: &str) -> Result<GitHubSource> {
    let u = Url::parse(raw.trim())
        .map_err(|e| AgentpackError::GitHubUrl(format!("invalid URL: {e}")))?;
    let host = u.host_str().unwrap_or("");
    if host != GITHUB_HOST && !host.ends_with(&format!(".{GITHUB_HOST}")) {
        return Err(AgentpackError::GitHubUrl(format!(
            "only {GITHUB_HOST} URLs supported, got {host}"
        )));
    }
    let segments: Vec<String> = u
        .path_segments()
        .ok_or_else(|| AgentpackError::GitHubUrl("URL has no path".into()))?
        .map(|s| s.to_string())
        .collect();
    if segments.len() < 2 {
        return Err(AgentpackError::GitHubUrl("expected /owner/repo/...".into()));
    }
    let owner = segments[0].clone();
    let repo = strip_git_suffix(&segments[1]);

    // https://github.com/o/r
    if segments.len() == 2 {
        return Ok(GitHubSource {
            owner,
            repo,
            git_ref: DEFAULT_GIT_REF.to_string(),
            path: String::new(),
        });
    }

    let kind = &segments[2];
    if kind == "tree" || kind == "blob" {
        if segments.len() < 5 {
            return Err(AgentpackError::GitHubUrl(format!(
                "expected /{kind}/<ref>/path"
            )));
        }
        let git_ref = segments[3].clone();
        let path_parts = &segments[4..];
        let mut path = path_parts.join("/");

        if kind == "blob" {
            let pth = std::path::Path::new(&path);
            if pth.ends_with("plugin.json")
                && pth
                    .parent()
                    .map(|par| par.ends_with(".claude-plugin") || par.ends_with(".cursor-plugin"))
                    .unwrap_or(false)
            {
                path = pth
                    .parent()
                    .and_then(|x| x.parent())
                    .map(|p| p.to_string_lossy().to_string())
                    .unwrap_or_default();
            } else if path.ends_with("SKILL.md") {
                path = pth
                    .parent()
                    .map(|p| p.to_string_lossy().to_string())
                    .unwrap_or_default();
            }
        }

        return Ok(GitHubSource {
            owner,
            repo,
            git_ref,
            path,
        });
    }

    Err(AgentpackError::GitHubUrl(format!(
        "unsupported GitHub path (expected .../tree/... or .../blob/...): {}",
        u.path()
    )))
}

/// Identity string for cache key: **resolved `commit_hex` only** (no branch/tag name).
/// `owner` / `repo` are lowercased to match GitHub's case-insensitive routing (and the
/// canonical lowercased form used by [`crate::resolve::module_id::ModuleId`]), so a URL spec
/// like `OlegHQ/agent-configs` and a module id `github.com/oleghq/agent-configs` share one
/// `cache_key`. Same `owner` / `repo` / `path` / **SHA** always dedupes — `main`, `HEAD`, or
/// a full SHA in the URL are equivalent after resolution.
pub fn normalized_identity(source: &GitHubSource, commit_hex: &str) -> String {
    format!(
        "github:{}/{}\0{}\0{}",
        source.owner.to_lowercase(),
        source.repo.to_lowercase(),
        source.path,
        commit_hex.trim().to_lowercase()
    )
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn parses_tree() {
        let s =
            parse_github_url("https://github.com/anthropics/skills/tree/main/skills/canvas-design")
                .unwrap();
        assert_eq!(s.owner, "anthropics");
        assert_eq!(s.repo, "skills");
        assert_eq!(s.git_ref, "main");
        assert_eq!(s.path, "skills/canvas-design");
    }

    #[test]
    fn parses_blob_skill_md() {
        let s = parse_github_url("https://github.com/foo/bar/blob/main/skill/SKILL.md").unwrap();
        assert_eq!(s.path, "skill");
    }

    #[test]
    fn parses_blob_plugin_json() {
        let s = parse_github_url(
            "https://github.com/foo/bar/blob/main/plugins/hookify/.claude-plugin/plugin.json",
        )
        .unwrap();
        assert_eq!(s.path, "plugins/hookify");
    }

    #[test]
    fn parses_blob_cursor_plugin_json() {
        let s =
            parse_github_url("https://github.com/foo/bar/blob/main/p/.cursor-plugin/plugin.json")
                .unwrap();
        assert_eq!(s.path, "p");
    }

    #[test]
    fn parses_blob_nested_markdown_file_keeps_full_path_for_materialize() {
        let s = parse_github_url("https://github.com/o/r/blob/main/plugins/pkg/agents/agent.md")
            .unwrap();
        assert_eq!(s.path, "plugins/pkg/agents/agent.md");
    }

    #[test]
    fn normalized_identity_is_case_insensitive_on_owner_repo() {
        let sha = "0123456789abcdef0123456789abcdef01234567";
        let mixed = GitHubSource {
            owner: "OlegHQ".into(),
            repo: "Agent-Configs".into(),
            git_ref: "dev".into(),
            path: "plugins/rust-dev".into(),
        };
        let lower = GitHubSource {
            owner: "oleghq".into(),
            repo: "agent-configs".into(),
            git_ref: "HEAD".into(),
            path: "plugins/rust-dev".into(),
        };
        assert_eq!(
            normalized_identity(&mixed, sha),
            normalized_identity(&lower, sha)
        );
    }
}
