use std::path::Path;

use reqwest::blocking::Client;

use crate::cache::verify_lock_cache_integrity;
use crate::error::{AgentpackError, Result};
use crate::lockfile::PackLock;
use crate::manifest::AgentpackManifest;
use crate::paths;
use crate::resolve::{resolve_lock_from_manifest, ResolveLockOpts};
use crate::staging;
use crate::ui::Ui;

use super::launch_fingerprint::{
    compute_launch_sync_digest, read_stored_launch_digest, write_launch_sync_state,
};

use super::add_fetch::{
    http_client, resolve_add_spec, resolve_existing_path, upsert_fetched_index,
};
use super::remove::resolve_remove_spec_to_key;

fn require_manifest(project_root: &Path) -> Result<AgentpackManifest> {
    AgentpackManifest::load(project_root)?
        .ok_or_else(|| AgentpackError::ManifestMissing(paths::manifest_path(project_root)))
}

fn resolve_and_save_lock(
    project_root: &Path,
    manifest: &AgentpackManifest,
    client: &Client,
    ui: &Ui,
    refresh_floating: bool,
) -> Result<PackLock> {
    let previous = PackLock::load(project_root).ok();
    let opts = ResolveLockOpts {
        previous: previous.as_ref(),
        refresh_floating,
    };
    let lock = resolve_lock_from_manifest(manifest, client, ui, &opts, project_root)?;
    lock.save(project_root)?;
    Ok(lock)
}

fn sync_unless_skipped(project_root: &Path, no_sync: bool, ui: &Ui) -> Result<()> {
    if no_sync {
        ui.message("Skipping sync (--no-sync).");
        return Ok(());
    }
    super::run_sync(project_root, false, false, false, ui)
}

pub fn run_add(project_root: &Path, spec: &str, no_sync: bool, ui: &Ui) -> Result<()> {
    paths::ensure_user_agentpack_layout()?;
    ui.message(format!("Adding: {spec}"));

    if let Some(canon) = resolve_existing_path(spec.trim()) {
        // Path dependency flow.
        let basename = canon.file_name().and_then(|s| s.to_str()).unwrap_or("pack");
        let rel_path = pathdiff::diff_paths(&canon, project_root).ok_or_else(|| {
            AgentpackError::Cache(
                "cannot compute relative path from project root to target directory".into(),
            )
        })?;
        let rel_str = rel_path
            .to_str()
            .ok_or_else(|| AgentpackError::Cache("path contains non-UTF8 characters".into()))?;
        let _ = require_manifest(project_root)?;
        AgentpackManifest::append_path_dependency(project_root, basename, rel_str)?;
        let manifest = require_manifest(project_root)?;
        let client = http_client()?;
        resolve_and_save_lock(project_root, &manifest, &client, ui, false)?;
        ui.message(format!(
            "Recorded {basename} = {{ path = \"{rel_str}\" }} in agentpack.toml and refreshed pack.lock."
        ));
        return sync_unless_skipped(project_root, no_sync, ui);
    }

    let client = http_client()?;
    let _ = require_manifest(project_root)?;
    let (fetched, shorthand) = resolve_add_spec(&client, spec, ui)?;
    let module_key = crate::cache::asset::dependency_key_for_entry(
        &fetched.module,
        &fetched.owner,
        &fetched.repo,
        &fetched.path,
    );
    AgentpackManifest::append_dependency_key(project_root, &module_key)?;
    let manifest = require_manifest(project_root)?;
    resolve_and_save_lock(project_root, &manifest, &client, ui, false)?;
    upsert_fetched_index(&fetched, shorthand.as_deref())?;
    ui.message(format!(
        "Recorded {module_key} in agentpack.toml and refreshed pack.lock."
    ));

    sync_unless_skipped(project_root, no_sync, ui)
}

pub fn run_remove(project_root: &Path, spec: &str, no_sync: bool, ui: &Ui) -> Result<()> {
    paths::ensure_user_agentpack_layout()?;
    let manifest = require_manifest(project_root)?;
    let key = resolve_remove_spec_to_key(spec, &manifest)?;
    AgentpackManifest::remove_dependency_entry(project_root, &key)?;
    let manifest = require_manifest(project_root)?;
    let client = http_client()?;
    resolve_and_save_lock(project_root, &manifest, &client, ui, false)?;
    if !ui.quiet {
        ui.message(format!(
            "Removed {} from {} and refreshed {}.",
            key,
            paths::manifest_path(project_root).display(),
            paths::lock_path(project_root).display()
        ));
    }
    sync_unless_skipped(project_root, no_sync, ui)
}

pub fn run_lock(project_root: &Path, refresh_floating: bool, ui: &Ui) -> Result<()> {
    paths::ensure_user_agentpack_layout()?;
    let manifest = require_manifest(project_root)?;
    let client = http_client()?;
    let lock = resolve_and_save_lock(project_root, &manifest, &client, ui, refresh_floating)?;
    if !ui.quiet {
        ui.message(format!(
            "Wrote {} ({} package(s)).",
            paths::lock_path(project_root).display(),
            lock.packages.len()
        ));
    }
    Ok(())
}

/// Used by launcher commands (`claude`, `agent`, `opencode`, `codex`) to sync before exec.
///
/// When inputs are unchanged, skips full resolve/stage and reuses existing cache + staging after
/// integrity checks.
pub fn sync_for_launch(project_root: &Path, ui: &Ui) -> Result<()> {
    paths::ensure_user_agentpack_layout()?;

    if let Some(stored) = read_stored_launch_digest(project_root)? {
        let current = compute_launch_sync_digest(project_root)?;
        if stored == current {
            let lock = PackLock::load(project_root)?;
            match verify_lock_cache_integrity(&lock) {
                Ok(()) => match staging::verify_staging(project_root, &lock) {
                    Ok(()) => {
                        ui.debug_message(
                            "Launch sync skipped — manifest, lock, cache, and staging look unchanged.",
                        );
                        return Ok(());
                    }
                    Err(e) => tracing::debug!(%e, "launch fast path: verify_staging failed"),
                },
                Err(e) => tracing::debug!(%e, "launch fast path: cache integrity failed"),
            }
        }
    }

    super::run_sync(project_root, false, false, false, ui)?;
    let digest = compute_launch_sync_digest(project_root)?;
    write_launch_sync_state(project_root, &digest)?;
    Ok(())
}
