use std::path::Path;

use reqwest::blocking::Client;

use crate::error::{AgentpackError, Result};
use crate::github::GitHubSource;
use crate::lockfile::{LockPackage, LockPlugin, LockSkill, PackageKind};
use crate::ui::Ui;

use super::layout::{
    claude_plugin_manifest_path, cursor_plugin_manifest_path, ensure_plugin_manifest,
    normalize_plugin_cache_layout,
};

#[derive(Debug, Clone)]
pub enum FetchedGithubAsset {
    Skill(LockSkill),
    Plugin(LockPlugin),
}

impl FetchedGithubAsset {
    pub fn cache_key(&self) -> &str {
        match self {
            Self::Skill(skill) => &skill.cache_key,
            Self::Plugin(plugin) => &plugin.cache_key,
        }
    }

    pub fn dependency_key(&self) -> String {
        match self {
            Self::Skill(skill) => {
                dependency_key_for_entry(&skill.module, &skill.owner, &skill.repo, &skill.path)
            }
            Self::Plugin(plugin) => {
                dependency_key_for_entry(&plugin.module, &plugin.owner, &plugin.repo, &plugin.path)
            }
        }
    }

    pub fn to_lock_package(&self, module: &str, direct: bool) -> LockPackage {
        match self {
            Self::Skill(skill) => LockPackage {
                module: module.to_string(),
                direct,
                kind: PackageKind::Skill,
                url: skill.url.clone(),
                owner: skill.owner.clone(),
                repo: skill.repo.clone(),
                path: skill.path.clone(),
                commit: skill.commit.clone(),
                cache_key: skill.cache_key.clone(),
                name: String::new(),
            },
            Self::Plugin(plugin) => LockPackage {
                module: module.to_string(),
                direct,
                kind: PackageKind::Plugin,
                url: plugin.url.clone(),
                owner: plugin.owner.clone(),
                repo: plugin.repo.clone(),
                path: plugin.path.clone(),
                commit: plugin.commit.clone(),
                cache_key: plugin.cache_key.clone(),
                name: plugin.name.clone(),
            },
        }
    }
}

fn dependency_key_for_entry(module: &str, owner: &str, repo: &str, path: &str) -> String {
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

fn lock_skill_from(
    display_url: &str,
    source: &GitHubSource,
    commit: String,
    cache_key: String,
) -> LockSkill {
    LockSkill {
        module: module_for_source(source),
        url: display_url.to_string(),
        owner: source.owner.clone(),
        repo: source.repo.clone(),
        path: source.path.clone(),
        commit,
        cache_key,
    }
}

fn lock_plugin_from(
    display_url: &str,
    source: &GitHubSource,
    commit: String,
    cache_key: String,
) -> LockPlugin {
    LockPlugin {
        module: module_for_source(source),
        name: String::new(),
        url: display_url.to_string(),
        owner: source.owner.clone(),
        repo: source.repo.clone(),
        path: source.path.clone(),
        commit,
        cache_key,
    }
}

/// After tree is on disk: skill vs full plugin.
pub(crate) fn classify_materialized(
    cache_root: &Path,
    display_url: &str,
    source: &GitHubSource,
    commit: String,
    cache_key: String,
) -> Result<FetchedGithubAsset> {
    normalize_plugin_cache_layout(cache_root)?;
    if claude_plugin_manifest_path(cache_root).is_file()
        || cursor_plugin_manifest_path(cache_root).is_file()
    {
        ensure_plugin_manifest(cache_root)?;
        return Ok(FetchedGithubAsset::Plugin(lock_plugin_from(
            display_url,
            source,
            commit,
            cache_key,
        )));
    }
    if cache_root.join("SKILL.md").is_file() {
        return Ok(FetchedGithubAsset::Skill(lock_skill_from(
            display_url,
            source,
            commit,
            cache_key,
        )));
    }
    Err(AgentpackError::InvalidCacheLayout(cache_root.to_path_buf()))
}

/// Fetch skill into cache if missing; returns paths and metadata for lock/db.
/// Errors if the URL points at a full `.claude-plugin` tree (use [`super::fetch_github_asset_from_url`]).
pub fn fetch_skill_from_url(client: &Client, raw_url: &str, ui: &Ui) -> Result<LockSkill> {
    match super::materialize::fetch_github_asset_from_url(client, raw_url, ui)? {
        FetchedGithubAsset::Skill(skill) => Ok(skill),
        FetchedGithubAsset::Plugin(_) => Err(AgentpackError::Cache(
            "this URL is a full Claude plugin directory (.claude-plugin present); \
             add it as a plugin entry instead of a bare skill"
                .into(),
        )),
    }
}

pub fn fetch_skill_from_parsed(
    client: &Client,
    source: &GitHubSource,
    display_url: &str,
    ui: &Ui,
) -> Result<LockSkill> {
    match super::materialize::materialize_github_tree(client, source, display_url, ui)? {
        FetchedGithubAsset::Skill(skill) => Ok(skill),
        FetchedGithubAsset::Plugin(_) => Err(AgentpackError::Cache(
            "this path is a full Claude plugin directory (.claude-plugin present)".into(),
        )),
    }
}

/// Fill missing pin fields for partially populated plugin rows.
pub fn backfill_plugin_lock_entry(client: &Client, plugin: &mut LockPlugin, ui: &Ui) -> Result<()> {
    if !plugin.needs_backfill() {
        return Ok(());
    }
    let url = plugin.url.clone();
    match super::materialize::fetch_github_asset_from_url(client, &url, ui)? {
        FetchedGithubAsset::Plugin(resolved) => *plugin = resolved,
        FetchedGithubAsset::Skill(_) => {
            return Err(AgentpackError::Cache(format!(
                "plugin URL {} resolved to a skill subtree, not a .claude-plugin package",
                plugin.url
            )));
        }
    }
    Ok(())
}
