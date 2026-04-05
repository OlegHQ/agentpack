use std::fs;
use std::path::{Path, PathBuf};

use reqwest::blocking::Client;

use crate::error::{AgentpackError, Result};
use crate::github::{download_and_extract, path_in_repo_looks_like_file};
use crate::lockfile::{LockPlugin, LockSkill, PackLock};
use crate::paths;
use crate::ui::Ui;

use super::layout::{
    cache_dir_is_package_root_in_filesystem, cache_entry_dir, cache_has_plugin_manifest,
    ensure_plugin_manifest, ensure_skill_md, normalize_plugin_cache_layout,
};
use super::tree::copy_source_tree;

// Common fields for skill and plugin lock entries, used to avoid duplicated match arms.
struct CachedLockEntry<'a> {
    cache_key: &'a str,
    url: &'a str,
    owner: &'a str,
    repo: &'a str,
    path: &'a str,
    commit: &'a str,
    is_plugin: bool,
}

impl<'a> CachedLockEntry<'a> {
    fn from_skill(s: &'a LockSkill) -> Self {
        Self {
            cache_key: &s.cache_key,
            url: &s.url,
            owner: &s.owner,
            repo: &s.repo,
            path: &s.path,
            commit: &s.commit,
            is_plugin: false,
        }
    }

    fn from_plugin(p: &'a LockPlugin) -> Self {
        Self {
            cache_key: &p.cache_key,
            url: &p.url,
            owner: &p.owner,
            repo: &p.repo,
            path: &p.path,
            commit: &p.commit,
            is_plugin: true,
        }
    }

    fn kind_label(&self) -> &'static str {
        if self.is_plugin {
            "plugin"
        } else {
            "skill"
        }
    }

    fn is_local_source(&self) -> bool {
        matches!(self.owner, "path" | "local") || self.url.starts_with("file:")
    }

    fn blob_hint(&self) -> Option<&str> {
        (self.url.contains("/blob/") && path_in_repo_looks_like_file(self.path))
            .then_some(self.path)
    }

    fn cache_ready(&self, out: &Path) -> Result<bool> {
        if self.is_plugin {
            normalize_plugin_cache_layout(out)?;
            Ok(cache_has_plugin_manifest(out))
        } else {
            Ok(out.join("SKILL.md").is_file() || cache_has_plugin_manifest(out))
        }
    }

    fn local_source_dir(&self) -> Option<PathBuf> {
        path_from_file_url(self.url).or_else(|| resolve_local_mirror_from_url(self.url))
    }

    fn restore_from_local_source(&self, out: &Path) -> Result<bool> {
        let Some(source) = self.local_source_dir() else {
            return Ok(false);
        };
        if !source.is_dir() {
            return Ok(false);
        }
        prepare_cache_output_dir(out)?;
        copy_source_tree(&source, out)?;
        normalize_plugin_cache_layout(out)?;
        Ok(true)
    }

    fn download_from_github(&self, client: &Client, out: &Path, ui: &Ui) -> Result<()> {
        let cache_dir = paths::cache_dir()?;
        fs::create_dir_all(&cache_dir).map_err(|err| AgentpackError::io(&cache_dir, err))?;
        download_and_extract(
            client,
            self.owner,
            self.repo,
            self.commit,
            self.path,
            out,
            ui,
            self.blob_hint(),
        )?;
        if self.is_plugin {
            ensure_plugin_manifest(out)
        } else {
            ensure_skill_md(out)
        }
    }

    fn ensure_cached(self, client: &Client, ui: &Ui) -> Result<bool> {
        let out = cache_entry_dir(self.cache_key)?;
        if self.cache_ready(&out)? {
            return Ok(true);
        }
        if self.restore_from_local_source(&out)? {
            return self.cache_ready(&out);
        }
        if self.is_local_source() {
            tracing::warn!(
                cache_key = %self.cache_key,
                url = %self.url,
                "{} cache missing and path/local source unavailable; skipping",
                self.kind_label(),
            );
            return Ok(false);
        }
        self.download_from_github(client, &out, ui)?;
        self.cache_ready(&out)
    }
}

fn prepare_cache_output_dir(out: &Path) -> Result<()> {
    let cache_dir = paths::cache_dir()?;
    fs::create_dir_all(&cache_dir).map_err(|err| AgentpackError::io(&cache_dir, err))?;
    if out.exists() {
        fs::remove_dir_all(out).map_err(|err| AgentpackError::io(out, err))?;
    }
    fs::create_dir_all(out).map_err(|err| AgentpackError::io(out, err))
}

fn resolve_local_mirror_from_url(url: &str) -> Option<PathBuf> {
    let prefix = "agentpack-local:";
    url.strip_prefix(prefix)
        .and_then(|rest| paths::local_mirror_path_from_shorthand(rest).ok())
}

fn path_from_file_url(url: &str) -> Option<PathBuf> {
    let parsed = url::Url::parse(url).ok()?;
    if parsed.scheme() != "file" {
        return None;
    }
    parsed.to_file_path().ok()
}

pub fn ensure_lock_skill_cached(client: &Client, skill: &LockSkill, ui: &Ui) -> Result<bool> {
    CachedLockEntry::from_skill(skill).ensure_cached(client, ui)
}

pub fn ensure_lock_plugin_cached(client: &Client, plugin: &LockPlugin, ui: &Ui) -> Result<bool> {
    CachedLockEntry::from_plugin(plugin).ensure_cached(client, ui)
}

/// Validate cache trees for every lock entry that [`crate::sync::pipeline`] would pass to [`ensure_lock_plugin_cached`] / [`ensure_lock_skill_cached`].
pub fn verify_lock_cache_integrity(lock: &PackLock) -> Result<()> {
    for plugin in &lock.plugins {
        if plugin.cache_key.is_empty() {
            continue;
        }
        let out = cache_entry_dir(&plugin.cache_key)?;
        normalize_plugin_cache_layout(&out)?;
        if !cache_has_plugin_manifest(&out) {
            return Err(AgentpackError::Cache(format!(
                "plugin cache not ready for {}",
                plugin.cache_key
            )));
        }
    }
    for skill in &lock.skills {
        if skill.cache_key.is_empty() {
            continue;
        }
        let out = cache_entry_dir(&skill.cache_key)?;
        let ok = cache_dir_is_package_root_in_filesystem(&out);
        if !ok {
            return Err(AgentpackError::Cache(format!(
                "skill cache not ready for {}",
                skill.cache_key
            )));
        }
    }
    Ok(())
}
