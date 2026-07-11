use std::fs;
use std::path::{Path, PathBuf};

use reqwest::blocking::Client;

use crate::error::{AgentpackError, Result};
use crate::github::{download_and_extract, path_in_repo_looks_like_file};
use crate::lockfile::{LockPackage, PackLock, PackageKind};
use crate::paths;
use crate::ui::Ui;

use super::layout::{
    cache_dir_is_package_root_in_filesystem, cache_entry_dir, cache_has_plugin_manifest,
    ensure_plugin_manifest, ensure_skill_md, normalize_plugin_cache_layout,
};
use super::tree::copy_source_tree;

fn is_local_source(pkg: &LockPackage) -> bool {
    matches!(pkg.owner.as_str(), "path" | "local") || pkg.url.starts_with("file:")
}

fn blob_hint(pkg: &LockPackage) -> Option<&str> {
    (pkg.url.contains("/blob/") && path_in_repo_looks_like_file(&pkg.path)).then_some(&pkg.path)
}

fn cache_ready(pkg: &LockPackage, out: &Path) -> Result<bool> {
    if pkg.kind == PackageKind::Plugin {
        normalize_plugin_cache_layout(out)?;
        Ok(cache_has_plugin_manifest(out))
    } else {
        Ok(out.join("SKILL.md").is_file() || cache_has_plugin_manifest(out))
    }
}

fn local_source_dir(pkg: &LockPackage) -> Option<PathBuf> {
    path_from_file_url(&pkg.url).or_else(|| resolve_local_mirror_from_url(&pkg.url))
}

fn restore_from_local_source(pkg: &LockPackage, out: &Path) -> Result<bool> {
    let Some(source) = local_source_dir(pkg) else {
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

fn download_from_github(pkg: &LockPackage, client: &Client, out: &Path, ui: &Ui) -> Result<()> {
    let cache_dir = paths::cache_dir()?;
    fs::create_dir_all(&cache_dir).map_err(|err| AgentpackError::io(&cache_dir, err))?;
    download_and_extract(
        client,
        &pkg.owner,
        &pkg.repo,
        &pkg.commit,
        &pkg.path,
        out,
        ui,
        blob_hint(pkg),
    )?;
    normalize_plugin_cache_layout(out)?;
    if pkg.kind == PackageKind::Plugin {
        ensure_plugin_manifest(out)
    } else {
        ensure_skill_md(out)
    }
}

/// Ensure a lock package is cached; returns true if cache is ready.
pub fn ensure_lock_cached(client: &Client, pkg: &LockPackage, ui: &Ui) -> Result<bool> {
    let out = cache_entry_dir(&pkg.cache_key)?;
    if cache_ready(pkg, &out)? {
        return Ok(true);
    }
    if restore_from_local_source(pkg, &out)? {
        return cache_ready(pkg, &out);
    }
    if is_local_source(pkg) {
        tracing::warn!(
            cache_key = %pkg.cache_key,
            url = %pkg.url,
            "{} cache missing and path/local source unavailable; skipping",
            pkg.kind_label(),
        );
        return Ok(false);
    }
    download_from_github(pkg, client, &out, ui)?;
    cache_ready(pkg, &out)
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

/// Validate cache trees for every lock entry.
pub fn verify_lock_cache_integrity(lock: &PackLock) -> Result<()> {
    for pkg in &lock.packages {
        if pkg.cache_key.is_empty() {
            continue;
        }
        let out = cache_entry_dir(&pkg.cache_key)?;
        match pkg.kind {
            PackageKind::Plugin => {
                normalize_plugin_cache_layout(&out)?;
                if !cache_has_plugin_manifest(&out) {
                    return Err(AgentpackError::Cache(format!(
                        "plugin cache not ready for {}",
                        pkg.cache_key
                    )));
                }
            }
            PackageKind::Skill => {
                if !cache_dir_is_package_root_in_filesystem(&out) {
                    return Err(AgentpackError::Cache(format!(
                        "skill cache not ready for {}",
                        pkg.cache_key
                    )));
                }
            }
        }
    }
    Ok(())
}
