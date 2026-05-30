use std::path::Path;

use chrono::Utc;

use crate::cache::index::{list_keys, upsert_entry, CacheEntryRecord};
use crate::cache::{backfill_plugin_lock_entry, ensure_lock_cached};
use crate::error::Result;
use crate::lockfile::{LockPackage, PackLock, PackageKind};
use crate::manifest::AgentpackManifest;
use crate::paths;
use crate::resolve::{resolve_lock_from_manifest, ResolveLockOpts};
use crate::staging::{self, skill_is_shadowed, HarnessTarget};
use crate::ui::Ui;

use super::add_fetch::http_client;

pub fn run_sync(
    project_root: &Path,
    dry_run: bool,
    verify_only: bool,
    update_lock: bool,
    selected_mode: Option<&str>,
    target: Option<HarnessTarget>,
    ui: &Ui,
) -> Result<()> {
    paths::ensure_user_agentpack_layout()?;
    let client = http_client()?;

    let manifest = AgentpackManifest::load(project_root)?;
    if !dry_run {
        maybe_refresh_lock_from_manifest(
            project_root,
            manifest.as_ref(),
            &client,
            ui,
            update_lock,
        )?;
    }

    let lock = PackLock::load(project_root)?;
    let mode =
        super::run::resolve_effective_mode(project_root, manifest.as_ref(), &lock, selected_mode)?;
    let plugins: Vec<&LockPackage> = lock.plugins().collect();
    let shadowed_skills = lock
        .skills()
        .filter(|s| skill_is_shadowed(s, &plugins))
        .count();

    if dry_run {
        ui.message(format!(
            "Dry-run: would sync {} skill(s), {} plugin(s); {} skill(s) shadowed by plugins (omitted from staging); no changes made.",
            lock.skill_count(), lock.plugin_count(), shadowed_skills
        ));
        tracing::info!(
            skills = lock.skill_count(),
            plugins = lock.plugin_count(),
            shadowed_skills,
            "dry-run"
        );
        return Ok(());
    }

    // Backfill plugin cache entries that are missing metadata.
    let mut lock = lock;
    let mut lock_dirty = false;
    for plugin in lock.plugins_mut() {
        if plugin.url.is_empty() {
            tracing::warn!("skipping plugin row with empty url");
            continue;
        }
        if plugin.needs_backfill() {
            backfill_plugin_lock_entry(&client, plugin, ui)?;
            lock_dirty = true;
        }
    }
    if lock_dirty {
        lock.save(project_root)?;
    }

    // Ensure cache and index.
    let fetched_at_unix = Utc::now().timestamp();
    let mut warnings = Vec::new();
    for pkg in &lock.packages {
        if pkg.cache_key.is_empty() {
            if pkg.kind == PackageKind::Plugin {
                tracing::warn!("skipping plugin sync: empty cache_key");
            }
            continue;
        }
        if !ensure_lock_cached(&client, pkg, ui)? {
            warnings.push(format!(
                "{} {} ({}): cache missing and source unavailable — omitted from staging",
                pkg.kind_label(),
                crate::fs_util::truncate_str(&pkg.cache_key, 12),
                pkg.url
            ));
        }
        upsert_entry(&pkg.cache_key, &index_record(pkg, fetched_at_unix), &[])?;
    }

    for warning in &warnings {
        if !ui.quiet {
            ui.message(format!("Warning: {warning}"));
        }
        tracing::warn!(message = %warning, "sync cache miss");
    }
    if shadowed_skills > 0 && !ui.quiet {
        ui.message(format!(
            "Note: {} skill(s) are shadowed by full plugin(s) and will not get separate staging hubs.",
            shadowed_skills
        ));
    }

    if verify_only {
        let spinner = ui.spinner("Verify staging layout…");
        staging::verify_staging(project_root, &lock, &mode, target)?;
        Ui::finish_spinner(spinner.as_ref(), "Staging checks passed");
        return Ok(());
    }

    let spinner = ui.spinner("Rebuild plugin staging…");
    staging::rebuild_staging(project_root, &lock, manifest.as_ref(), &mode, target)?;
    staging::verify_staging(project_root, &lock, &mode, target)?;
    Ui::finish_spinner(spinner.as_ref(), "Staging ready");

    let index_key_count = list_keys()?.len();
    tracing::debug!(
        index_keys = index_key_count,
        skills = lock.skill_count(),
        plugins = lock.plugin_count(),
        "sync complete"
    );
    if !ui.quiet {
        ui.message(format!(
            "Sync finished — {} skill(s), {} plugin(s), {} cache index entr(ies). One merged bundle: agentpack-bundle.",
            lock.skill_count(), lock.plugin_count(), index_key_count
        ));
    }
    Ok(())
}

fn maybe_refresh_lock_from_manifest(
    project_root: &Path,
    manifest: Option<&AgentpackManifest>,
    client: &reqwest::blocking::Client,
    ui: &Ui,
    refresh_floating: bool,
) -> Result<()> {
    let Some(manifest) = manifest else {
        return Ok(());
    };
    if manifest.dependencies.is_empty() {
        return Ok(());
    }
    let previous = PackLock::load(project_root).ok();
    let resolved = resolve_lock_from_manifest(
        manifest,
        client,
        ui,
        &ResolveLockOpts {
            previous: previous.as_ref(),
            refresh_floating,
        },
        project_root,
    )?;
    resolved.save(project_root)
}

fn index_record(pkg: &LockPackage, fetched_at_unix: i64) -> CacheEntryRecord {
    CacheEntryRecord {
        kind: pkg.kind,
        source_url: pkg.url.to_owned(),
        owner: pkg.owner.to_owned(),
        repo: pkg.repo.to_owned(),
        path: pkg.path.to_owned(),
        commit: pkg.commit.to_owned(),
        fetched_at_unix,
    }
}
