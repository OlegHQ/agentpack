use std::path::Path;

use reqwest::blocking::Client;

use crate::error::{AgentpackError, Result};
use crate::github::GitHubSource;
use crate::lockfile::{LockPackage, PackageKind};
use crate::ui::Ui;

use super::layout::{
    claude_plugin_manifest_path, codex_plugin_manifest_path, cursor_plugin_manifest_path,
    ensure_plugin_manifest, normalize_plugin_cache_layout,
};

pub fn dependency_key_for_entry(module: &str, owner: &str, repo: &str, path: &str) -> String {
    if !module.is_empty() {
        return module.to_string();
    }
    if owner == "path" {
        return repo.to_string();
    }
    crate::resolve::module_id::ModuleId::from_owner_repo_path(owner, repo, path)
        .as_str()
        .to_string()
}

fn module_for_source(source: &GitHubSource) -> String {
    if source.owner == "path" || source.owner == "local" {
        return String::new();
    }
    crate::resolve::module_id::ModuleId::from_owner_repo_path(
        &source.owner,
        &source.repo,
        &source.path,
    )
    .as_str()
    .to_string()
}

fn lock_package_from(
    kind: PackageKind,
    display_url: &str,
    source: &GitHubSource,
    commit: String,
    cache_key: String,
) -> LockPackage {
    LockPackage {
        module: module_for_source(source),
        direct: false,
        kind,
        url: display_url.to_string(),
        owner: source.owner.clone(),
        repo: source.repo.clone(),
        path: source.path.clone(),
        commit,
        cache_key,
        name: String::new(),
    }
}

/// After tree is on disk: skill vs full plugin.
pub(crate) fn classify_materialized(
    cache_root: &Path,
    display_url: &str,
    source: &GitHubSource,
    commit: String,
    cache_key: String,
) -> Result<LockPackage> {
    normalize_plugin_cache_layout(cache_root)?;
    if claude_plugin_manifest_path(cache_root).is_file()
        || cursor_plugin_manifest_path(cache_root).is_file()
        || codex_plugin_manifest_path(cache_root).is_file()
    {
        ensure_plugin_manifest(cache_root)?;
        return Ok(lock_package_from(
            PackageKind::Plugin,
            display_url,
            source,
            commit,
            cache_key,
        ));
    }
    if cache_root.join("SKILL.md").is_file() {
        return Ok(lock_package_from(
            PackageKind::Skill,
            display_url,
            source,
            commit,
            cache_key,
        ));
    }
    Err(AgentpackError::InvalidCacheLayout(cache_root.to_path_buf()))
}

fn ensure_skill(pkg: LockPackage) -> Result<LockPackage> {
    if pkg.kind == PackageKind::Plugin {
        return Err(AgentpackError::Cache(
            "this path is a full plugin directory (native plugin manifest present); \
             add it as a plugin entry instead of a bare skill"
                .into(),
        ));
    }
    Ok(pkg)
}

/// Fetch skill into cache if missing; returns paths and metadata for lock/db.
/// Errors if the URL points at a full `.claude-plugin` tree.
pub fn fetch_skill_from_url(client: &Client, raw_url: &str, ui: &Ui) -> Result<LockPackage> {
    ensure_skill(super::materialize::fetch_github_asset_from_url(
        client, raw_url, ui,
    )?)
}

pub fn fetch_skill_from_parsed(
    client: &Client,
    source: &GitHubSource,
    display_url: &str,
    ui: &Ui,
) -> Result<LockPackage> {
    ensure_skill(super::materialize::materialize_github_tree(
        client,
        source,
        display_url,
        ui,
        false,
    )?)
}

/// Fill missing pin fields for partially populated plugin rows.
pub fn backfill_plugin_lock_entry(
    client: &Client,
    plugin: &mut LockPackage,
    ui: &Ui,
) -> Result<()> {
    if !plugin.needs_backfill() {
        return Ok(());
    }
    let url = plugin.url.clone();
    let resolved = super::materialize::fetch_github_asset_from_url(client, &url, ui)?;
    if resolved.kind == PackageKind::Skill {
        return Err(AgentpackError::Cache(format!(
            "plugin URL {} resolved to a skill subtree, not a plugin package",
            plugin.url
        )));
    }
    *plugin = resolved;
    Ok(())
}
