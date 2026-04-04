use std::path::Path;

use reqwest::blocking::Client;

use crate::error::{AgentpackError, Result};
use crate::github::check_rate_limit_hint;
use crate::lockfile::PackLock;
use crate::manifest::AgentpackManifest;
use crate::paths::{self};
use crate::resolve::resolve_lock_from_manifest;
use crate::ui::Ui;

use super::add_fetch::{
    http_client, resolve_add_spec, resolve_existing_path_for_add, upsert_fetched_index,
};
use super::remove::resolve_remove_spec_to_key;

fn sync_client() -> Result<Client> {
    let client = http_client()?;
    check_rate_limit_hint(&client);
    Ok(client)
}

fn require_manifest(project_root: &Path) -> Result<AgentpackManifest> {
    AgentpackManifest::load(project_root)?
        .ok_or_else(|| AgentpackError::ManifestMissing(paths::manifest_path(project_root)))
}

fn resolve_and_save_lock(
    project_root: &Path,
    manifest: &AgentpackManifest,
    client: &Client,
    ui: &Ui,
) -> Result<PackLock> {
    let lock = resolve_lock_from_manifest(manifest, client, ui)?;
    lock.save(project_root)?;
    Ok(lock)
}

fn sync_unless_skipped(project_root: &Path, no_sync: bool, ui: &Ui) -> Result<()> {
    if no_sync {
        ui.message("Skipping sync (--no-sync).");
        return Ok(());
    }
    super::run_sync(project_root, false, false, ui)
}

pub fn run_add(project_root: &Path, spec: &str, no_sync: bool, ui: &Ui) -> Result<()> {
    paths::ensure_user_agentpack_layout()?;
    ui.message(format!("Adding: {spec}"));
    if resolve_existing_path_for_add(spec).is_some() {
        return Err(AgentpackError::Cache(
            "filesystem package: add an entry under [dependencies] in agentpack.toml manually (file: pins are not auto-edited)"
                .into(),
        ));
    }
    let client = sync_client()?;
    let _ = require_manifest(project_root)?;
    let (fetched, shorthand) = resolve_add_spec(&client, spec, ui)?;
    let module_key = fetched.dependency_key();
    AgentpackManifest::append_dependency_key(project_root, &module_key)?;
    let manifest = require_manifest(project_root)?;
    resolve_and_save_lock(project_root, &manifest, &client, ui)?;
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
    let client = sync_client()?;
    resolve_and_save_lock(project_root, &manifest, &client, ui)?;
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

pub fn run_lock(project_root: &Path, ui: &Ui) -> Result<()> {
    paths::ensure_user_agentpack_layout()?;
    let manifest = require_manifest(project_root)?;
    let client = sync_client()?;
    let lock = resolve_and_save_lock(project_root, &manifest, &client, ui)?;
    if !ui.quiet {
        ui.message(format!(
            "Wrote {} ({} package(s)).",
            paths::lock_path(project_root).display(),
            lock.packages.len()
        ));
    }
    Ok(())
}

/// Used by binary `claude` to resolve project root then sync + exec.
pub fn sync_for_launch(project_root: &Path, ui: &Ui) -> Result<()> {
    super::run_sync(project_root, false, false, ui)
}
