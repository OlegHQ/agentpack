use std::collections::{BTreeMap, BTreeSet};
use std::path::Path;

use walkdir::WalkDir;

use crate::cache::cache_entry_dir;
use crate::error::{AgentpackError, Result};
use crate::lockfile::{LockPackage, PackLock};
use crate::manifest::AgentpackManifest;
use crate::staging::mcp::collect_merged_mcp;

use super::selectors::{normalize_relative_runtime_path, Selector};

#[derive(Clone, Debug, Default)]
pub struct DependencyNode {
    pub module: String,
    pub children: Vec<DependencyNode>,
}

#[derive(Clone, Debug, Default)]
pub struct CapabilityCatalog {
    package_modules: BTreeSet<String>,
    package_paths: BTreeMap<String, BTreeSet<String>>,
    dot_agents_paths: BTreeSet<String>,
    mcp_names: BTreeSet<String>,
    dependency_tree: Vec<DependencyNode>,
}

impl CapabilityCatalog {
    pub fn build(
        project_root: &Path,
        lock: Option<&PackLock>,
        manifest: Option<&AgentpackManifest>,
    ) -> Result<Self> {
        let mut package_modules = BTreeSet::new();
        let mut package_paths: BTreeMap<String, BTreeSet<String>> = BTreeMap::new();
        let mut lock_map: BTreeMap<String, &LockPackage> = BTreeMap::new();

        if let Some(lock) = lock {
            for pkg in &lock.packages {
                if pkg.module.is_empty() {
                    continue;
                }
                package_modules.insert(pkg.module.clone());
                package_paths
                    .entry(pkg.module.clone())
                    .or_default()
                    .extend(collect_package_paths(pkg)?);
                lock_map.insert(pkg.module.clone(), pkg);
            }
        }

        if let Some(manifest) = manifest {
            package_modules.extend(manifest.dependencies.keys().cloned());
        }

        let dot_agents_paths = collect_dot_agents_paths(&project_root.join(".agents"))?;
        let mcp_names = match (lock, manifest) {
            (Some(lock), _) => collect_merged_mcp(project_root, lock, manifest, None)?
                .into_keys()
                .collect(),
            (None, Some(manifest)) => manifest.mcp.servers.keys().cloned().collect(),
            (None, None) => BTreeSet::new(),
        };

        let dependency_tree = build_dependency_tree(manifest, &lock_map)?;

        Ok(Self {
            package_modules,
            package_paths,
            dot_agents_paths,
            mcp_names,
            dependency_tree,
        })
    }

    pub fn validate_selector(&self, selector: &Selector) -> Result<()> {
        match selector {
            Selector::Package { module } => {
                if self.package_modules.contains(module) {
                    return Ok(());
                }
                Err(AgentpackError::Mode(format!(
                    "unknown package selector target: {module}"
                )))
            }
            Selector::PackagePath { module, rel_path } => {
                if !self.package_modules.contains(module) {
                    return Err(AgentpackError::Mode(format!(
                        "unknown package selector target: {module}"
                    )));
                }
                if self
                    .package_paths
                    .get(module)
                    .is_some_and(|paths| paths.contains(rel_path))
                {
                    return Ok(());
                }
                Err(AgentpackError::Mode(format!(
                    "unknown package path selector target: {module}:{rel_path}"
                )))
            }
            Selector::Mcp { name } => {
                if self.mcp_names.contains(name) {
                    return Ok(());
                }
                Err(AgentpackError::Mode(format!(
                    "unknown MCP selector target: {name}"
                )))
            }
            Selector::DotAgents { rel_path } => {
                if self.dot_agents_paths.contains(rel_path) {
                    return Ok(());
                }
                Err(AgentpackError::Mode(format!(
                    "unknown .agents selector target: {rel_path}"
                )))
            }
        }
    }

    pub fn package_modules(&self) -> &BTreeSet<String> {
        &self.package_modules
    }

    pub fn package_paths(&self, module: &str) -> Option<&BTreeSet<String>> {
        self.package_paths.get(module)
    }

    pub fn dot_agents_paths(&self) -> &BTreeSet<String> {
        &self.dot_agents_paths
    }

    pub fn mcp_names(&self) -> &BTreeSet<String> {
        &self.mcp_names
    }

    pub fn dependency_tree(&self) -> &[DependencyNode] {
        &self.dependency_tree
    }
}

fn collect_package_paths(pkg: &LockPackage) -> Result<BTreeSet<String>> {
    if pkg.cache_key.is_empty() {
        return Ok(BTreeSet::new());
    }
    walk_relative_paths(&cache_entry_dir(&pkg.cache_key)?)
}

fn collect_dot_agents_paths(dot_agents_root: &Path) -> Result<BTreeSet<String>> {
    walk_relative_paths(dot_agents_root)
}

/// Walk `root` and return every path beneath it, rendered as a relative
/// runtime string ready for selector matching.
fn walk_relative_paths(root: &Path) -> Result<BTreeSet<String>> {
    let mut paths = BTreeSet::new();
    if !root.is_dir() {
        return Ok(paths);
    }
    for entry in WalkDir::new(root).follow_links(false) {
        let entry = entry.map_err(|error| AgentpackError::Staging(error.to_string()))?;
        let path = entry.path();
        if path == root {
            continue;
        }
        let rel = path.strip_prefix(root).map_err(|_| {
            AgentpackError::Staging(format!(
                "path outside {}: {}",
                root.display(),
                path.display()
            ))
        })?;
        paths.insert(normalize_relative_runtime_path(rel)?);
    }
    Ok(paths)
}

fn build_dependency_tree(
    manifest: Option<&AgentpackManifest>,
    lock_map: &BTreeMap<String, &LockPackage>,
) -> Result<Vec<DependencyNode>> {
    let Some(manifest) = manifest else {
        return Ok(Vec::new());
    };

    let mut visited = BTreeSet::new();
    manifest
        .dependencies
        .keys()
        .map(|module| build_dependency_node(module, lock_map, &mut visited))
        .collect()
}

fn build_dependency_node(
    module: &str,
    lock_map: &BTreeMap<String, &LockPackage>,
    visited: &mut BTreeSet<String>,
) -> Result<DependencyNode> {
    if !visited.insert(module.to_string()) {
        return Ok(DependencyNode {
            module: module.to_string(),
            children: Vec::new(),
        });
    }

    let mut children = Vec::new();
    if let Some(pkg) = lock_map.get(module).copied() {
        if !pkg.cache_key.is_empty() {
            let cache_root = cache_entry_dir(&pkg.cache_key)?;
            if let Some(dependencies) = AgentpackManifest::load_nested_dependencies(&cache_root)? {
                for child in dependencies.keys() {
                    children.push(build_dependency_node(child, lock_map, visited)?);
                }
            }
        }
    }
    visited.remove(module);

    Ok(DependencyNode {
        module: module.to_string(),
        children,
    })
}

#[cfg(test)]
mod tests {
    use std::fs;

    use serial_test::serial;
    use tempfile::tempdir;

    use super::*;
    use crate::lockfile::PackageKind;
    use crate::paths::project_dot_agents_dir;

    #[test]
    #[serial]
    fn validates_known_selector_targets() {
        let dir = tempdir().unwrap();
        let root = dir.path();
        let cache_root = root.join("cache");
        fs::create_dir_all(cache_root.join("k".repeat(64)).join("hooks")).unwrap();
        std::env::set_var("AGENTPACK_HOME", root);
        fs::write(
            cache_root
                .join("k".repeat(64))
                .join("hooks")
                .join("hooks.json"),
            "{}",
        )
        .unwrap();
        fs::create_dir_all(project_dot_agents_dir(root).join("rules")).unwrap();
        fs::write(project_dot_agents_dir(root).join("rules").join("a.mdc"), "").unwrap();

        let lock = PackLock {
            lockfile_version: 2,
            packages: vec![LockPackage {
                module: "github.com/acme/repo".into(),
                direct: true,
                kind: PackageKind::Plugin,
                url: String::new(),
                owner: "acme".into(),
                repo: "repo".into(),
                path: String::new(),
                commit: "c".repeat(40),
                cache_key: "k".repeat(64),
                name: String::new(),
            }],
            ..Default::default()
        };

        let catalog = CapabilityCatalog::build(root, Some(&lock), None).unwrap();
        catalog
            .validate_selector(
                &Selector::parse("package-path:github.com/acme/repo:hooks/hooks.json").unwrap(),
            )
            .unwrap();
        catalog
            .validate_selector(&Selector::parse(".agents:rules/a.mdc").unwrap())
            .unwrap();
    }
}
