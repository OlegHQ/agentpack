//! Go-style module paths: `github.com/<owner>/<repo>[/<path>][@<ref>]`.

use crate::error::{AgentpackError, Result};
use crate::github::{parse_github_url, GitHubSource};

/// Canonical module path without `@ref` (e.g. `github.com/anthropics/skills/skills/foo`).
#[derive(Debug, Clone, PartialEq, Eq, Hash, PartialOrd, Ord)]
pub struct ModuleId(pub String);

impl ModuleId {
    pub fn as_str(&self) -> &str {
        &self.0
    }

    /// `github.com/owner/repo` or `github.com/owner/repo/p1/p2`
    pub fn parse(module: &str) -> Result<Self> {
        let s = module.trim();
        let mut base = s.split('@').next().unwrap_or(s).trim();
        // Tolerate mistaken double-quoted keys from older agentpack `append_dependency_key` (quotes
        // were embedded in the key string).
        if let Some(inner) = base.strip_prefix('"').and_then(|x| x.strip_suffix('"')) {
            base = inner.trim();
        }
        if base.is_empty() {
            return Err(AgentpackError::Cache("empty module id".into()));
        }
        let parts: Vec<&str> = base.split('/').filter(|x| !x.is_empty()).collect();
        if parts.len() < 3 || parts[0] != "github.com" {
            return Err(AgentpackError::Cache(format!(
                "module id must start with github.com/<owner>/<repo>[/…], got {module:?}"
            )));
        }
        Ok(ModuleId(base.to_lowercase()))
    }

    pub fn owner_repo_path_parts(&self) -> (String, String, String) {
        let p: Vec<&str> = self.0.split('/').collect();
        let owner = p[1].to_string();
        let repo = p[2].to_string();
        let path = if p.len() > 3 {
            p[3..].join("/")
        } else {
            String::new()
        };
        (owner, repo, path)
    }

    /// `https://github.com/...` → `github.com/o/r/subdir`
    #[allow(dead_code)]
    pub fn from_github_url(url: &str) -> Result<Self> {
        let gh = parse_github_url(url.trim())?;
        let path = gh.path.trim_matches('/').to_string();
        let mut id = format!(
            "github.com/{}/{}",
            gh.owner.to_lowercase(),
            gh.repo.to_lowercase()
        );
        if !path.is_empty() {
            id.push('/');
            id.push_str(&path);
        }
        Ok(ModuleId(id))
    }

    /// Lowercase `github.com/owner/repo[/path]` from GitHub coordinates (no `@ref`).
    pub fn from_owner_repo_path(owner: &str, repo: &str, path: &str) -> Self {
        let path = path.trim_matches('/');
        let mut id = format!(
            "github.com/{}/{}",
            owner.to_lowercase(),
            repo.to_lowercase()
        );
        if !path.is_empty() {
            id.push('/');
            id.push_str(path);
        }
        ModuleId(id)
    }

    pub fn to_github_source(&self, git_ref: &str) -> GitHubSource {
        let (owner, repo, path) = self.owner_repo_path_parts();
        GitHubSource {
            owner,
            repo,
            git_ref: git_ref.to_string(),
            path,
        }
    }
}

/// Optional `@ref` suffix on a spec string.
pub fn split_module_at_ref(spec: &str) -> (&str, Option<&str>) {
    if let Some((a, b)) = spec.rsplit_once('@') {
        if !b.is_empty() && !a.is_empty() && !a.contains('@') {
            return (a.trim(), Some(b.trim()));
        }
    }
    (spec.trim(), None)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn parses_github_module() {
        let m = ModuleId::parse("github.com/Anthropics/skills/skills/foo").unwrap();
        assert_eq!(m.as_str(), "github.com/anthropics/skills/skills/foo");
    }

    #[test]
    fn parses_module_key_with_extra_quotes() {
        let m = ModuleId::parse("\"github.com/o/r/p\"").unwrap();
        assert_eq!(m.as_str(), "github.com/o/r/p");
    }

    #[test]
    fn owner_repo_path() {
        let m = ModuleId::parse("github.com/o/r/a/b").unwrap();
        assert_eq!(
            m.owner_repo_path_parts(),
            ("o".into(), "r".into(), "a/b".into())
        );
    }
}
