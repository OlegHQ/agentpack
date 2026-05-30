//! Capability tree: the data model rendered in the middle pane and the builder that turns a
//! [`CapabilityCatalog`] into a forest of selectable nodes.

use std::collections::BTreeSet;

use crate::mode::catalog::CapabilityCatalog;
use crate::mode::selectors::Selector;

#[derive(Clone, Debug)]
pub struct TreeNode {
    pub id: String,
    pub label: String,
    /// Dimmed context shown after `label` (e.g. `owner/repo` for a package leaf).
    pub subtitle: Option<String>,
    pub selector: Option<Selector>,
    pub children: Vec<TreeNode>,
}

impl TreeNode {
    fn leaf(id: impl Into<String>, label: impl Into<String>, selector: Selector) -> Self {
        Self {
            id: id.into(),
            label: label.into(),
            subtitle: None,
            selector: Some(selector),
            children: Vec::new(),
        }
    }

    fn section(id: impl Into<String>, label: impl Into<String>) -> Self {
        Self {
            id: id.into(),
            label: label.into(),
            subtitle: None,
            selector: None,
            children: Vec::new(),
        }
    }
}

/// Splits a module id into a short, scannable leaf label and a dimmed parent
/// path. `github.com/anthropics/claude-plugins-official/plugins/code-review`
/// becomes `("code-review", Some("anthropics/claude-plugins-official/plugins"))`.
fn package_display(module: &str) -> (String, Option<String>) {
    if let Some(rest) = module.strip_prefix("github.com/") {
        let segments: Vec<&str> = rest.split('/').filter(|s| !s.is_empty()).collect();
        match segments.as_slice() {
            [] => (module.to_string(), None),
            [owner] => (module.to_string(), Some((*owner).into())),
            [owner, repo] => ((*repo).into(), Some((*owner).into())),
            [owner, repo, mid @ .., leaf] => {
                let parent = if mid.is_empty() {
                    format!("{owner}/{repo}")
                } else {
                    format!("{owner}/{repo}/{}", mid.join("/"))
                };
                ((*leaf).into(), Some(parent))
            }
        }
    } else {
        (module.to_string(), None)
    }
}

pub fn build_tree(catalog: &CapabilityCatalog) -> Vec<TreeNode> {
    let mut roots = Vec::new();

    let mut packages = TreeNode::section("section:packages", "Packages");
    for module in catalog.package_modules() {
        let (label, subtitle) = package_display(module);
        let mut node = TreeNode {
            id: format!("package:{module}"),
            label,
            subtitle,
            selector: Some(Selector::Package {
                module: module.clone(),
            }),
            children: Vec::new(),
        };
        if let Some(paths) = catalog.package_paths(module) {
            node.children = build_path_children(&format!("package-path:{module}:"), module, paths);
        }
        packages.children.push(node);
    }
    if !packages.children.is_empty() {
        roots.push(packages);
    }

    let mut mcp = TreeNode::section("section:mcp", "MCP servers");
    for name in catalog.mcp_names() {
        mcp.children.push(TreeNode::leaf(
            format!("mcp:{name}"),
            name.clone(),
            Selector::Mcp { name: name.clone() },
        ));
    }
    if !mcp.children.is_empty() {
        roots.push(mcp);
    }

    let mut dot_agents = TreeNode::section("section:.agents", ".agents");
    if !catalog.dot_agents_paths().is_empty() {
        dot_agents.children = build_dot_agents_children(catalog.dot_agents_paths());
    }
    if !dot_agents.children.is_empty() {
        roots.push(dot_agents);
    }

    roots
}

fn build_path_children(id_prefix: &str, module: &str, paths: &BTreeSet<String>) -> Vec<TreeNode> {
    let mut forest: Vec<TreeNode> = Vec::new();
    for rel in paths {
        let segments: Vec<&str> = rel.split('/').filter(|s| !s.is_empty()).collect();
        if segments.is_empty() {
            continue;
        }
        insert_path_node(&mut forest, &segments, 0, id_prefix, rel, |rel_path| {
            Selector::PackagePath {
                module: module.to_string(),
                rel_path: rel_path.to_string(),
            }
        });
    }
    forest
}

fn build_dot_agents_children(paths: &BTreeSet<String>) -> Vec<TreeNode> {
    let mut forest: Vec<TreeNode> = Vec::new();
    for rel in paths {
        let segments: Vec<&str> = rel.split('/').filter(|s| !s.is_empty()).collect();
        if segments.is_empty() {
            continue;
        }
        insert_path_node(&mut forest, &segments, 0, ".agents:", rel, |rel_path| {
            Selector::DotAgents {
                rel_path: rel_path.to_string(),
            }
        });
    }
    forest
}

/// Inserts a path into an existing forest, creating interior nodes as needed.
/// Each interior node receives a selector so "enable subtree" works at every
/// level.
fn insert_path_node(
    forest: &mut Vec<TreeNode>,
    segments: &[&str],
    depth: usize,
    id_prefix: &str,
    full_rel: &str,
    make_selector: impl Fn(&str) -> Selector + Copy,
) {
    if depth >= segments.len() {
        return;
    }
    let current_rel = segments[..=depth].join("/");
    let node_id = format!("{id_prefix}{current_rel}");
    let label = segments[depth].to_string();
    let pos = forest.iter().position(|node| node.id == node_id);
    let index = match pos {
        Some(index) => index,
        None => {
            forest.push(TreeNode {
                id: node_id,
                label,
                subtitle: None,
                selector: Some(make_selector(&current_rel)),
                children: Vec::new(),
            });
            forest.len() - 1
        }
    };
    if depth + 1 < segments.len() {
        insert_path_node(
            &mut forest[index].children,
            segments,
            depth + 1,
            id_prefix,
            full_rel,
            make_selector,
        );
    } else {
        // Leaf — already inserted above. No further children.
        debug_assert_eq!(forest[index].id, format!("{id_prefix}{full_rel}"));
    }
}

/// Depth-first lookup of a node by id (used by subtree operations).
pub fn find_node<'a>(nodes: &'a [TreeNode], id: &str) -> Option<&'a TreeNode> {
    for node in nodes {
        if node.id == id {
            return Some(node);
        }
        if let Some(found) = find_node(&node.children, id) {
            return Some(found);
        }
    }
    None
}

pub fn collect_subtree_selectors(node: &TreeNode, out: &mut Vec<String>) {
    if let Some(selector) = &node.selector {
        out.push(selector.canonical_string());
    }
    for child in &node.children {
        collect_subtree_selectors(child, out);
    }
}

#[cfg(test)]
mod tree_tests {
    use super::*;
    use crate::lockfile::{LockPackage, PackLock, PackageKind};
    use crate::paths::project_dot_agents_dir;
    use serial_test::serial;
    use std::fs;
    use tempfile::tempdir;

    #[test]
    #[serial]
    fn tree_contains_sections_and_leaves() {
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
        let tree = build_tree(&catalog);
        let section_ids: Vec<_> = tree.iter().map(|n| n.id.clone()).collect();
        assert!(section_ids.contains(&"section:packages".into()));
        assert!(section_ids.contains(&"section:.agents".into()));

        let mut collected = Vec::new();
        for root in &tree {
            walk(root, &mut collected);
        }
        assert!(collected
            .iter()
            .any(|id| id == "package-path:github.com/acme/repo:hooks/hooks.json"));
        assert!(collected.iter().any(|id| id == ".agents:rules/a.mdc"));
    }

    fn walk(node: &TreeNode, out: &mut Vec<String>) {
        out.push(node.id.clone());
        for child in &node.children {
            walk(child, out);
        }
    }
}
