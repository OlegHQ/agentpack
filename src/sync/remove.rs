use crate::cache::blob_path_parent_prefixes;
use crate::error::{AgentpackError, Result};
use crate::github::{parse_github_url, path_in_repo_looks_like_file};
use crate::manifest::AgentpackManifest;
use crate::resolve::module_id::{split_module_at_ref, ModuleId};

fn module_key_candidates(owner: &str, repo: &str, path: &str) -> Vec<String> {
    let owner = owner.to_lowercase();
    let repo = repo.to_lowercase();
    if path_in_repo_looks_like_file(path) {
        blob_path_parent_prefixes(path)
            .into_iter()
            .map(|p| {
                ModuleId::from_owner_repo_path(&owner, &repo, &p)
                    .as_str()
                    .to_string()
            })
            .collect()
    } else {
        vec![ModuleId::from_owner_repo_path(&owner, &repo, path)
            .as_str()
            .to_string()]
    }
}

pub(super) fn resolve_remove_spec_to_key(
    spec: &str,
    manifest: &AgentpackManifest,
) -> Result<String> {
    let spec = spec.trim();
    if spec.is_empty() {
        return Err(AgentpackError::Cache("empty remove spec".into()));
    }

    // Check if spec is a filesystem path — match by basename.
    if let Some(canon) = super::add_fetch::resolve_existing_path(spec) {
        if let Some(basename) = canon.file_name().and_then(|s| s.to_str()) {
            if manifest.dependencies.contains_key(basename) {
                return Ok(basename.to_string());
            }
        }
    }

    if spec.starts_with("http://") || spec.starts_with("https://") {
        let src = parse_github_url(spec)?;
        for k in module_key_candidates(&src.owner, &src.repo, &src.path) {
            if manifest.dependencies.contains_key(&k) {
                return Ok(k);
            }
        }
        return Err(AgentpackError::DependencyNotFound(spec.to_string()));
    }

    let parts: Vec<&str> = spec.split('/').filter(|s| !s.is_empty()).collect();
    if parts.len() == 1 {
        let tail = parts[0].to_lowercase();
        for k in manifest.dependencies.keys() {
            let kl = k.to_lowercase();
            if kl == tail || kl.ends_with(&format!("/{tail}")) {
                return Ok(k.clone());
            }
        }
    }

    let (base, _) = split_module_at_ref(spec);
    if parts.len() >= 2 && parts[0] != "github.com" {
        let path = parts[2..].join("/");
        for k in module_key_candidates(parts[0], parts[1], &path) {
            if manifest.dependencies.contains_key(&k) {
                return Ok(k);
            }
        }
        return Err(AgentpackError::DependencyNotFound(spec.to_string()));
    }

    let id = ModuleId::parse(base)?;
    let (owner, repo, path) = id.owner_repo_path_parts();
    for k in module_key_candidates(&owner, &repo, &path) {
        if manifest.dependencies.contains_key(&k) {
            return Ok(k);
        }
    }

    Err(AgentpackError::DependencyNotFound(spec.to_string()))
}

#[cfg(test)]
mod remove_spec_tests {
    use std::collections::BTreeMap;

    use super::*;
    use crate::manifest::DepSpecToml;

    fn man_with(dep_keys: &[&str]) -> AgentpackManifest {
        let mut deps = BTreeMap::new();
        for k in dep_keys {
            deps.insert(k.to_string(), DepSpecToml::Short(String::new()));
        }
        AgentpackManifest {
            name: "t".into(),
            version: "1".into(),
            description: String::new(),
            dependencies: deps,
            mcp: Default::default(),
            modes: BTreeMap::new(),
        }
    }

    #[test]
    fn remove_key_from_owner_repo_shorthand() {
        let m =
            man_with(&["github.com/anthropics/claude-plugins-official/plugins/code-simplifier"]);
        let k = resolve_remove_spec_to_key(
            "anthropics/claude-plugins-official/plugins/code-simplifier",
            &m,
        )
        .unwrap();
        assert_eq!(
            k,
            "github.com/anthropics/claude-plugins-official/plugins/code-simplifier"
        );
    }

    #[test]
    fn remove_key_from_blob_file_url() {
        let m =
            man_with(&["github.com/anthropics/claude-plugins-official/plugins/code-simplifier"]);
        let k = resolve_remove_spec_to_key(
            "https://github.com/anthropics/claude-plugins-official/blob/main/plugins/code-simplifier/agents/code-simplifier.md",
            &m,
        )
        .unwrap();
        assert_eq!(
            k,
            "github.com/anthropics/claude-plugins-official/plugins/code-simplifier"
        );
    }
}
