use std::path::Path;

use reqwest::blocking::Client;

use crate::cache::verify_lock_cache_integrity;
use crate::error::{AgentpackError, Result};
use crate::harness::HarnessTarget;
use crate::lockfile::{LockPackage, PackLock};
use crate::manifest::AgentpackManifest;
use crate::mode::catalog::CapabilityCatalog;
use crate::mode::filter::EffectiveMode;
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

fn ensure_project_files_for_add(project_root: &Path, ui: &Ui) -> Result<()> {
    let manifest_path = paths::manifest_path(project_root);
    let lock_path = paths::lock_path(project_root);
    if manifest_path.is_file() {
        return Ok(());
    }

    ui.message(format!(
        "Warning: no agentpack.toml found; initializing agentpack.toml and pack.lock in {}.",
        project_root.display()
    ));
    let dirname = project_root
        .file_name()
        .and_then(|n| n.to_str())
        .unwrap_or("project");
    AgentpackManifest::write_stub(project_root, dirname, "0.0.1")?;
    if !lock_path.is_file() {
        crate::lockfile::init_lockfile(
            project_root,
            Some(dirname.to_string()),
            Some("0.0.1".into()),
        )?;
    }
    Ok(())
}

fn resolve_and_save_lock(
    project_root: &Path,
    manifest: &AgentpackManifest,
    client: &Client,
    ui: &Ui,
    refresh_floating: bool,
    primed: &[LockPackage],
) -> Result<PackLock> {
    let mut previous = PackLock::load(project_root).ok();
    // Splice freshly-fetched packages into the previous lock so
    // `pick_effective_git_ref` reuses their commits instead of re-resolving
    // (and re-downloading) the same module within a single `add` call.
    // Later entries win on duplicate module id.
    if !primed.is_empty() {
        let prev = previous.get_or_insert_with(PackLock::default);
        for pkg in primed {
            prev.packages.retain(|p| p.module != pkg.module);
            prev.packages.push(pkg.clone());
        }
    }
    let opts = ResolveLockOpts {
        previous: previous.as_ref(),
        refresh_floating,
    };
    let lock = resolve_lock_from_manifest(manifest, client, ui, &opts, project_root)?;
    lock.save(project_root)?;
    Ok(lock)
}

fn sync_unless_skipped(
    project_root: &Path,
    selected_mode: Option<&str>,
    no_sync: bool,
    ui: &Ui,
) -> Result<()> {
    if no_sync {
        ui.message("Skipping sync (--no-sync).");
        return Ok(());
    }
    // `add` / `remove` / `mcp` flows don't launch a harness — no workspace overlay should appear.
    super::run_sync(project_root, false, false, false, selected_mode, None, ui)
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
        ensure_project_files_for_add(project_root, ui)?;
        AgentpackManifest::append_path_dependency(project_root, basename, rel_str)?;
        let manifest = require_manifest(project_root)?;
        let client = http_client()?;
        resolve_and_save_lock(project_root, &manifest, &client, ui, false, &[])?;
        ui.message(format!(
            "Recorded {basename} = {{ path = \"{rel_str}\" }} in agentpack.toml and refreshed pack.lock."
        ));
        return sync_unless_skipped(project_root, None, no_sync, ui);
    }

    let client = http_client()?;
    let resolved = resolve_add_spec(&client, spec, ui)?;
    let fetched = resolved.package;
    let module_key = crate::cache::asset::dependency_key_for_entry(
        &fetched.module,
        &fetched.owner,
        &fetched.repo,
        &fetched.path,
    );
    ensure_project_files_for_add(project_root, ui)?;
    // Persist an explicit `@ref` so `lock`/`sync` re-resolve the same pin (otherwise it floats).
    AgentpackManifest::append_dependency_pin(
        project_root,
        &module_key,
        resolved.git_ref.as_deref(),
    )?;
    // Update the cache alias index before resolving so transitive lookups via
    // shorthand can reuse the just-fetched cache_key.
    upsert_fetched_index(&fetched, resolved.shorthand.as_deref())?;
    let manifest = require_manifest(project_root)?;
    let primed = [fetched];
    resolve_and_save_lock(project_root, &manifest, &client, ui, false, &primed)?;
    ui.message(format!(
        "Recorded {module_key} in agentpack.toml and refreshed pack.lock."
    ));

    sync_unless_skipped(project_root, None, no_sync, ui)
}

pub fn run_remove(project_root: &Path, spec: &str, no_sync: bool, ui: &Ui) -> Result<()> {
    paths::ensure_user_agentpack_layout()?;
    let manifest = require_manifest(project_root)?;
    let key = resolve_remove_spec_to_key(spec, &manifest)?;
    AgentpackManifest::remove_dependency_entry(project_root, &key)?;
    let manifest = require_manifest(project_root)?;
    let client = http_client()?;
    resolve_and_save_lock(project_root, &manifest, &client, ui, false, &[])?;
    if !ui.quiet {
        ui.message(format!(
            "Removed {} from {} and refreshed {}.",
            key,
            paths::manifest_path(project_root).display(),
            paths::lock_path(project_root).display()
        ));
    }
    sync_unless_skipped(project_root, None, no_sync, ui)
}

pub fn run_lock(project_root: &Path, refresh_floating: bool, ui: &Ui) -> Result<()> {
    paths::ensure_user_agentpack_layout()?;
    let manifest = require_manifest(project_root)?;
    let client = http_client()?;
    let lock = resolve_and_save_lock(project_root, &manifest, &client, ui, refresh_floating, &[])?;
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
/// integrity checks. `target` flows down so workspace overlays (`.cursor/agents`,
/// `.agents/plugins/agentpack-bundle`) are only materialized for the matching harness.
pub fn sync_for_launch(
    project_root: &Path,
    selected_mode: Option<&str>,
    target: HarnessTarget,
    ui: &Ui,
) -> Result<EffectiveMode> {
    paths::ensure_user_agentpack_layout()?;
    let manifest = AgentpackManifest::load(project_root)?;
    let lock = match PackLock::load(project_root) {
        Ok(lock) => lock,
        Err(AgentpackError::Io { path, source }) if path == paths::lock_path(project_root) => {
            if source.kind() == std::io::ErrorKind::NotFound {
                PackLock::empty_for_project(project_root)
            } else {
                return Err(AgentpackError::Io { path, source });
            }
        }
        Err(err) => return Err(err),
    };
    let mode = resolve_effective_mode(project_root, manifest.as_ref(), &lock, selected_mode)?;

    if let Some(stored) = read_stored_launch_digest(project_root, mode.name())? {
        let current = compute_launch_sync_digest(project_root, &mode, Some(target))?;
        if stored == current {
            match verify_lock_cache_integrity(&lock) {
                Ok(()) => match staging::verify_staging(project_root, &lock, &mode, Some(target)) {
                    Ok(()) => {
                        ui.debug_message(
                            "Launch sync skipped — manifest, lock, cache, and staging look unchanged.",
                        );
                        return Ok(mode);
                    }
                    Err(e) => tracing::debug!(%e, "launch fast path: verify_staging failed"),
                },
                Err(e) => tracing::debug!(%e, "launch fast path: cache integrity failed"),
            }
        }
    }

    super::run_sync(
        project_root,
        false,
        false,
        false,
        Some(mode.name()),
        Some(target),
        ui,
    )?;
    let digest = compute_launch_sync_digest(project_root, &mode, Some(target))?;
    write_launch_sync_state(project_root, mode.name(), &digest)?;
    Ok(mode)
}

pub fn resolve_effective_mode(
    project_root: &Path,
    manifest: Option<&AgentpackManifest>,
    lock: &PackLock,
    selected_mode: Option<&str>,
) -> Result<EffectiveMode> {
    let catalog = CapabilityCatalog::build(project_root, Some(lock), manifest)?;
    EffectiveMode::resolve(manifest, selected_mode, Some(&catalog))
}
