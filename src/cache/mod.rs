pub(crate) mod asset;
pub(crate) mod index;
mod layout;
mod marketplace;
mod materialize;
mod restore;
mod tree;

pub(crate) use asset::classify_materialized;
pub use asset::{backfill_plugin_lock_entry, fetch_skill_from_parsed, fetch_skill_from_url};
pub use layout::{
    cache_dir_is_package_root_in_filesystem, cache_entry_dir, cache_has_plugin_manifest,
    claude_plugin_manifest_path, codex_plugin_manifest_path, compute_cache_key,
    cursor_plugin_manifest_path, ensure_plugin_manifest, ensure_skill_md, hash_directory_contents,
    normalize_plugin_cache_layout, repo_dir_is_package_root,
};
pub(crate) use materialize::blob_path_parent_prefixes;
pub use materialize::{fetch_github_asset_from_url, materialize_github_tree};
pub use restore::{ensure_lock_cached, verify_lock_cache_integrity};
pub use tree::copy_package_dir_to_cache;

#[cfg(test)]
mod tests {
    use std::fs;

    use super::*;
    use crate::github::GitHubSource;

    #[test]
    fn cache_key_stable() {
        let id = "github:foo/bar\0path\0".to_string() + &"a".repeat(40);
        let a = compute_cache_key(&id);
        let b = compute_cache_key(&id);
        assert_eq!(a, b);
        assert_eq!(a.len(), 64);
    }

    #[test]
    fn normalized_identity_ignores_git_ref_same_commit() {
        let src = GitHubSource {
            owner: "o".into(),
            repo: "r".into(),
            git_ref: "main".into(),
            path: "skills/x".into(),
        };
        let commit = "c".repeat(40);
        let k1 = compute_cache_key(&crate::github::normalized_identity(&src, &commit));
        let mut src2 = src.clone();
        src2.git_ref = "HEAD".into();
        let k2 = compute_cache_key(&crate::github::normalized_identity(&src2, &commit));
        assert_eq!(k1, k2);
        let mut src3 = src.clone();
        src3.path = "skills/y".into();
        let k3 = compute_cache_key(&crate::github::normalized_identity(&src3, &commit));
        assert_ne!(k1, k3);
    }

    #[test]
    fn classify_prefers_plugin_when_manifest_present() {
        let dir = tempfile::tempdir().unwrap();
        let root = dir.path().join("c");
        fs::create_dir_all(root.join(".claude-plugin")).unwrap();
        fs::write(root.join(".claude-plugin/plugin.json"), "{}").unwrap();
        fs::write(root.join("SKILL.md"), "# x").unwrap();
        assert!(cache_has_plugin_manifest(&root));
    }

    #[test]
    fn blob_path_parent_prefixes_deepest_first() {
        let p = blob_path_parent_prefixes("plugins/p/agents/a.md");
        assert_eq!(
            p,
            vec![
                "plugins/p/agents".into(),
                "plugins/p".into(),
                "plugins".into(),
                String::new(),
            ]
        );
    }

    #[test]
    fn package_root_detectors_agree_for_skill_dir() {
        let dir = tempfile::tempdir().unwrap();
        let root = dir.path();
        fs::write(root.join("SKILL.md"), "# s").unwrap();
        assert!(cache_dir_is_package_root_in_filesystem(root));
        let mut idx = std::collections::HashSet::new();
        idx.insert("SKILL.md".into());
        assert!(repo_dir_is_package_root(&idx, ""));
    }

    #[test]
    fn package_root_detectors_recognize_marketplaces_and_codex_plugins() {
        let dir = tempfile::tempdir().unwrap();
        let marketplace = dir.path().join("marketplace");
        fs::create_dir_all(marketplace.join(".agents/plugins")).unwrap();
        fs::write(
            marketplace.join(".agents/plugins/marketplace.json"),
            r#"{"plugins":[]}"#,
        )
        .unwrap();
        assert!(cache_dir_is_package_root_in_filesystem(&marketplace));

        let codex = dir.path().join("codex");
        fs::create_dir_all(codex.join(".codex-plugin")).unwrap();
        fs::write(codex.join(".codex-plugin/plugin.json"), "{}").unwrap();
        assert!(cache_dir_is_package_root_in_filesystem(&codex));

        let mut idx = std::collections::HashSet::new();
        idx.insert(".agents/plugins/marketplace.json".into());
        assert!(repo_dir_is_package_root(&idx, ""));
        idx.clear();
        idx.insert("plugins/p/.codex-plugin/plugin.json".into());
        assert!(repo_dir_is_package_root(&idx, "plugins/p"));
    }

    #[test]
    fn codex_only_plugin_is_normalized_for_all_harnesses() {
        let dir = tempfile::tempdir().unwrap();
        let root = dir.path();
        fs::create_dir_all(root.join(".codex-plugin")).unwrap();
        fs::write(
            root.join(".codex-plugin/plugin.json"),
            r#"{"name":"native-codex","version":"2.0.0"}"#,
        )
        .unwrap();
        fs::write(
            root.join(".mcp.json"),
            r#"{"mcpServers":{"docs":{"url":"https://example.com"}}}"#,
        )
        .unwrap();

        normalize_plugin_cache_layout(root).unwrap();

        assert!(root.join(".claude-plugin/plugin.json").is_file());
        assert!(root.join(".cursor-plugin/plugin.json").is_file());
        assert!(root.join(".codex-plugin/plugin.json").is_file());
        assert!(root.join("mcp.json").is_file());
    }

    #[test]
    fn agentpack_toml_only_synthesizes_plugin_manifests() {
        let dir = tempfile::tempdir().unwrap();
        let root = dir.path();
        fs::write(
            root.join("agentpack.toml"),
            r#"name = "pkg-a"
version = "2.0.0"
description = "test plugin from manifest only"

[dependencies]
"#,
        )
        .unwrap();
        normalize_plugin_cache_layout(root).unwrap();
        assert!(root.join(".claude-plugin/plugin.json").is_file());
        assert!(root.join(".cursor-plugin/plugin.json").is_file());
        assert!(root.join(".codex-plugin/plugin.json").is_file());
    }
}
