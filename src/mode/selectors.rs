use std::path::{Component, Path};

use crate::error::{AgentpackError, Result};

#[derive(Clone, Debug, Eq, PartialEq)]
pub enum Selector {
    Package { module: String },
    PackagePath { module: String, rel_path: String },
    Mcp { name: String },
    DotAgents { rel_path: String },
}

#[derive(Clone, Copy, Debug, Eq, Ord, PartialEq, PartialOrd)]
pub struct MatchSpecificity(u16);

impl MatchSpecificity {
    fn package() -> Self {
        Self(1)
    }

    fn from_rel_path(rel_path: &str) -> Self {
        let depth = rel_path
            .split('/')
            .filter(|segment| !segment.is_empty())
            .count() as u16;
        Self(10 + depth)
    }
}

impl Selector {
    pub fn parse(raw: &str) -> Result<Self> {
        let trimmed = raw.trim();
        if let Some(module) = trimmed.strip_prefix("package:") {
            let module = normalize_module(module)?;
            return Ok(Self::Package { module });
        }
        if let Some(rest) = trimmed.strip_prefix("package-path:") {
            let (module, rel_path) = rest.split_once(':').ok_or_else(|| {
                AgentpackError::Mode(format!(
                    "invalid selector {trimmed:?}: expected package-path:<module>:<path>"
                ))
            })?;
            return Ok(Self::PackagePath {
                module: normalize_module(module)?,
                rel_path: normalize_relative_selector_path(rel_path)?,
            });
        }
        if let Some(name) = trimmed.strip_prefix("mcp:") {
            let name = name.trim();
            if name.is_empty() {
                return Err(AgentpackError::Mode(format!(
                    "invalid selector {trimmed:?}: MCP name cannot be empty"
                )));
            }
            return Ok(Self::Mcp {
                name: name.to_string(),
            });
        }
        if let Some(rel_path) = trimmed.strip_prefix(".agents:") {
            return Ok(Self::DotAgents {
                rel_path: normalize_relative_selector_path(rel_path)?,
            });
        }
        Err(AgentpackError::Mode(format!(
            "invalid selector {trimmed:?}: expected package:, package-path:, mcp:, or .agents:"
        )))
    }

    pub fn canonical_string(&self) -> String {
        match self {
            Self::Package { module } => format!("package:{module}"),
            Self::PackagePath { module, rel_path } => {
                format!("package-path:{module}:{rel_path}")
            }
            Self::Mcp { name } => format!("mcp:{name}"),
            Self::DotAgents { rel_path } => format!(".agents:{rel_path}"),
        }
    }

    pub fn matches_package_path(
        &self,
        module: &str,
        rel_path: &Path,
    ) -> Result<Option<MatchSpecificity>> {
        let rel_path = normalize_relative_runtime_path(rel_path)?;
        let matched = match self {
            Self::Package {
                module: selector_module,
            } => (selector_module == module).then_some(MatchSpecificity::package()),
            Self::PackagePath {
                module: selector_module,
                rel_path: selector_rel_path,
            } if selector_module == module
                && path_selector_matches(selector_rel_path, &rel_path) =>
            {
                Some(MatchSpecificity::from_rel_path(selector_rel_path))
            }
            _ => None,
        };
        Ok(matched)
    }

    pub fn matches_dot_agents_path(&self, rel_path: &Path) -> Result<Option<MatchSpecificity>> {
        let rel_path = normalize_relative_runtime_path(rel_path)?;
        let matched = match self {
            Self::DotAgents { rel_path: selector }
                if path_selector_matches(selector, &rel_path) =>
            {
                Some(MatchSpecificity::from_rel_path(selector))
            }
            _ => None,
        };
        Ok(matched)
    }

    pub fn matches_mcp(&self, name: &str) -> bool {
        matches!(self, Self::Mcp { name: selector_name } if selector_name == name)
    }

    pub fn module(&self) -> Option<&str> {
        match self {
            Self::Package { module } | Self::PackagePath { module, .. } => Some(module),
            _ => None,
        }
    }

    pub fn package_rel_path(&self) -> Option<&str> {
        match self {
            Self::PackagePath { rel_path, .. } => Some(rel_path),
            _ => None,
        }
    }

    pub fn dot_agents_rel_path(&self) -> Option<&str> {
        match self {
            Self::DotAgents { rel_path } => Some(rel_path),
            _ => None,
        }
    }

    pub fn mcp_name(&self) -> Option<&str> {
        match self {
            Self::Mcp { name } => Some(name),
            _ => None,
        }
    }
}

fn normalize_module(module: &str) -> Result<String> {
    let module = module.trim();
    if module.is_empty() {
        return Err(AgentpackError::Mode(
            "module selector cannot be empty".to_string(),
        ));
    }
    Ok(module.to_string())
}

pub fn normalize_relative_selector_path(input: &str) -> Result<String> {
    normalize_relative_path_impl(input, "selector path")
}

pub fn normalize_relative_runtime_path(path: &Path) -> Result<String> {
    normalize_relative_path_impl(&path.to_string_lossy(), "path")
}

fn normalize_relative_path_impl(input: &str, label: &str) -> Result<String> {
    let replaced = input.trim().replace('\\', "/");
    let stripped = replaced.trim_start_matches("./").trim_matches('/');
    if stripped.is_empty() {
        return Err(AgentpackError::Mode(format!("{label} cannot be empty")));
    }

    let mut segments = Vec::new();
    for component in Path::new(stripped).components() {
        match component {
            Component::Normal(segment) => {
                let piece = segment.to_string_lossy();
                if piece.is_empty() {
                    continue;
                }
                segments.push(piece.into_owned());
            }
            Component::CurDir => {}
            Component::ParentDir => {
                return Err(AgentpackError::Mode(format!(
                    "{label} cannot contain parent traversal: {input:?}"
                )));
            }
            Component::RootDir | Component::Prefix(_) => {
                return Err(AgentpackError::Mode(format!(
                    "{label} must be relative: {input:?}"
                )));
            }
        }
    }

    if segments.is_empty() {
        return Err(AgentpackError::Mode(format!("{label} cannot be empty")));
    }

    Ok(segments.join("/"))
}

fn path_selector_matches(selector: &str, target: &str) -> bool {
    target == selector || target.starts_with(&format!("{selector}/"))
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn parses_package_path_selector() {
        let selector =
            Selector::parse("package-path:github.com/acme/repo:hooks/hooks.json").unwrap();
        assert_eq!(
            selector,
            Selector::PackagePath {
                module: "github.com/acme/repo".into(),
                rel_path: "hooks/hooks.json".into(),
            }
        );
    }

    #[test]
    fn package_selector_matches_nested_paths() {
        let selector = Selector::parse("package:github.com/acme/repo").unwrap();
        assert_eq!(
            selector
                .matches_package_path(
                    "github.com/acme/repo",
                    Path::new("agents/code-simplifier.md")
                )
                .unwrap(),
            Some(MatchSpecificity::package())
        );
    }

    #[test]
    fn package_path_selector_matches_descendants() {
        let selector = Selector::parse("package-path:github.com/acme/repo:hooks").unwrap();
        assert_eq!(
            selector
                .matches_package_path("github.com/acme/repo", Path::new("hooks/nested/file.json"))
                .unwrap(),
            Some(MatchSpecificity::from_rel_path("hooks"))
        );
    }

    #[test]
    fn dot_agents_selector_rejects_parent_traversal() {
        let error = Selector::parse(".agents:../secret").unwrap_err();
        assert!(error.to_string().contains("parent traversal"));
    }
}
