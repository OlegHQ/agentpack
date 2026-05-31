//! Go-style module paths: `github.com/<owner>/<repo>[/<path>][@<ref>]`.

use crate::error::{AgentpackError, Result};
use crate::github::{GitHubSource, GITHUB_HOST};

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
        if parts.len() < 3 || parts[0] != GITHUB_HOST {
            return Err(AgentpackError::Cache(format!(
                "module id must start with {GITHUB_HOST}/<owner>/<repo>[/…], got {module:?}"
            )));
        }
        // Lowercase only the `<host>/<owner>/<repo>` prefix. GitHub in-repo paths are
        // case-sensitive, so preserving `parts[3..]` verbatim keeps fetches resolvable.
        let mut canon = format!(
            "{GITHUB_HOST}/{}/{}",
            parts[1].to_lowercase(),
            parts[2].to_lowercase()
        );
        if parts.len() > 3 {
            canon.push('/');
            canon.push_str(&parts[3..].join("/"));
        }
        Ok(ModuleId(canon))
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

    /// Lowercase `github.com/owner/repo[/path]` from GitHub coordinates (no `@ref`).
    pub fn from_owner_repo_path(owner: &str, repo: &str, path: &str) -> Self {
        let path = path.trim_matches('/');
        let mut id = format!(
            "{GITHUB_HOST}/{}/{}",
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
    fn preserves_in_repo_path_case() {
        // owner/repo lowercased, but the case-sensitive GitHub path is preserved verbatim.
        let m = ModuleId::parse("github.com/Anthropics/Skills/skills/PDF-Tools").unwrap();
        assert_eq!(m.as_str(), "github.com/anthropics/skills/skills/PDF-Tools");
        assert_eq!(m.to_github_source("HEAD").path, "skills/PDF-Tools");
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
