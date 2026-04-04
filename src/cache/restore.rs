use std::fs;
use std::path::{Path, PathBuf};

use reqwest::blocking::Client;

use crate::error::{AgentpackError, Result};
use crate::github::{download_and_extract, path_in_repo_looks_like_file};
use crate::lockfile::{LockPlugin, LockSkill};
use crate::paths;
use crate::ui::Ui;

use super::layout::{
    cache_entry_dir, cache_has_plugin_manifest, ensure_plugin_manifest, ensure_skill_md,
    normalize_plugin_cache_layout,
};
use super::tree::copy_tree_files;

// Facade over the closed skill/plugin lock entry set so cache restore behavior lives in one place.
enum CachedLockEntry<'a> {
    Skill(&'a LockSkill),
    Plugin(&'a LockPlugin),
}

impl<'a> CachedLockEntry<'a> {
    fn ensure_cached(self, client: &Client, ui: &Ui) -> Result<bool> {
        let out = cache_entry_dir(self.cache_key())?;
        if self.cache_ready(&out)? {
            return Ok(true);
        }

        if self.restore_from_local_source(&out)? {
            return self.cache_ready(&out);
        }

        if self.is_local_source() {
            tracing::warn!(
                cache_key = %self.cache_key(),
                url = %self.url(),
                "{} cache missing and path/local source unavailable; skipping",
                self.kind_label(),
            );
            return Ok(false);
        }

        self.download_from_github(client, &out, ui)?;
        self.cache_ready(&out)
    }

    fn cache_key(&self) -> &str {
        match self {
            Self::Skill(skill) => &skill.cache_key,
            Self::Plugin(plugin) => &plugin.cache_key,
        }
    }

    fn url(&self) -> &str {
        match self {
            Self::Skill(skill) => &skill.url,
            Self::Plugin(plugin) => &plugin.url,
        }
    }

    fn owner(&self) -> &str {
        match self {
            Self::Skill(skill) => &skill.owner,
            Self::Plugin(plugin) => &plugin.owner,
        }
    }

    fn repo(&self) -> &str {
        match self {
            Self::Skill(skill) => &skill.repo,
            Self::Plugin(plugin) => &plugin.repo,
        }
    }

    fn path(&self) -> &str {
        match self {
            Self::Skill(skill) => &skill.path,
            Self::Plugin(plugin) => &plugin.path,
        }
    }

    fn commit(&self) -> &str {
        match self {
            Self::Skill(skill) => &skill.commit,
            Self::Plugin(plugin) => &plugin.commit,
        }
    }

    fn kind_label(&self) -> &'static str {
        match self {
            Self::Skill(_) => "skill",
            Self::Plugin(_) => "plugin",
        }
    }

    fn is_local_source(&self) -> bool {
        matches!(self.owner(), "path" | "local") || self.url().starts_with("file:")
    }

    fn blob_hint(&self) -> Option<&str> {
        (self.url().contains("/blob/") && path_in_repo_looks_like_file(self.path()))
            .then_some(self.path())
    }

    fn cache_ready(&self, out: &Path) -> Result<bool> {
        match self {
            Self::Skill(_) => Ok(out.join("SKILL.md").is_file() || cache_has_plugin_manifest(out)),
            Self::Plugin(_) => {
                normalize_plugin_cache_layout(out)?;
                Ok(cache_has_plugin_manifest(out))
            }
        }
    }

    fn restore_from_local_source(&self, out: &Path) -> Result<bool> {
        let Some(source) = self.local_source_dir() else {
            return Ok(false);
        };
        if !source.is_dir() {
            return Ok(false);
        }

        prepare_cache_output_dir(out)?;
        copy_tree_files(&source, out)?;
        normalize_plugin_cache_layout(out)?;
        Ok(true)
    }

    fn download_from_github(&self, client: &Client, out: &Path, ui: &Ui) -> Result<()> {
        let cache_dir = paths::cache_dir()?;
        fs::create_dir_all(&cache_dir).map_err(|err| AgentpackError::io(&cache_dir, err))?;
        download_and_extract(
            client,
            self.owner(),
            self.repo(),
            self.commit(),
            self.path(),
            out,
            ui,
            self.blob_hint(),
        )?;

        match self {
            Self::Skill(_) => ensure_skill_md(out),
            Self::Plugin(_) => ensure_plugin_manifest(out),
        }
    }

    fn local_source_dir(&self) -> Option<PathBuf> {
        path_from_file_url(self.url()).or_else(|| resolve_local_mirror_from_url(self.url()))
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
    CachedLockEntry::Skill(skill).ensure_cached(client, ui)
}

pub fn ensure_lock_plugin_cached(client: &Client, plugin: &LockPlugin, ui: &Ui) -> Result<bool> {
    CachedLockEntry::Plugin(plugin).ensure_cached(client, ui)
}
