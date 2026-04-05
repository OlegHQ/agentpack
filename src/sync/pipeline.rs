use std::path::Path;

use chrono::Utc;
use reqwest::blocking::Client;

use crate::cache::index::{list_keys, upsert_entry, CacheEntryRecord};
use crate::cache::{backfill_plugin_lock_entry, ensure_lock_cached};
use crate::error::Result;
use crate::lockfile::{LockPackage, PackLock, PackageKind};
use crate::manifest::AgentpackManifest;
use crate::paths;
use crate::resolve::{resolve_lock_from_manifest, ResolveLockOpts};
use crate::staging::{self, skill_is_shadowed};
use crate::ui::Ui;

use super::add_fetch::http_client;

pub fn run_sync(
    project_root: &Path,
    dry_run: bool,
    verify_only: bool,
    update_lock: bool,
    ui: &Ui,
) -> Result<()> {
    let mut session = SyncSession::prepare(
        project_root,
        SyncMode {
            dry_run,
            verify_only,
            update_lock,
        },
        ui,
    )?;
    for step in sync_steps() {
        if matches!(step.run(&mut session)?, StepOutcome::Finished) {
            break;
        }
    }
    Ok(())
}

#[derive(Clone, Copy)]
struct SyncMode {
    dry_run: bool,
    verify_only: bool,
    update_lock: bool,
}

struct SyncSession<'a> {
    project_root: &'a Path,
    ui: &'a Ui,
    client: Client,
    mode: SyncMode,
    manifest: Option<AgentpackManifest>,
    lock: PackLock,
    shadowed_skills: usize,
    warnings: Vec<String>,
}

impl<'a> SyncSession<'a> {
    fn prepare(project_root: &'a Path, mode: SyncMode, ui: &'a Ui) -> Result<Self> {
        paths::ensure_user_agentpack_layout()?;
        let client = http_client()?;

        let manifest = AgentpackManifest::load(project_root)?;
        if !mode.dry_run {
            maybe_refresh_lock_from_manifest(
                project_root,
                manifest.as_ref(),
                &client,
                ui,
                mode.update_lock,
            )?;
        }

        let lock = PackLock::load(project_root)?;
        let plugins: Vec<&LockPackage> = lock.plugins().collect();
        let shadowed_skills = lock
            .skills()
            .filter(|skill| skill_is_shadowed(skill, &plugins))
            .count();

        Ok(Self {
            project_root,
            ui,
            client,
            mode,
            manifest,
            lock,
            shadowed_skills,
            warnings: Vec::new(),
        })
    }

    fn record_warning(&mut self, warning: String) {
        self.warnings.push(warning);
    }
}

fn maybe_refresh_lock_from_manifest(
    project_root: &Path,
    manifest: Option<&AgentpackManifest>,
    client: &Client,
    ui: &Ui,
    refresh_floating: bool,
) -> Result<()> {
    let Some(manifest) = manifest else {
        return Ok(());
    };
    // Empty `[dependencies]` means the lock is authoritative (e.g. tests or hand-edited v2).
    if manifest.dependencies.is_empty() {
        return Ok(());
    }
    let previous = PackLock::load(project_root).ok();
    let opts = ResolveLockOpts {
        previous: previous.as_ref(),
        refresh_floating,
    };
    let resolved = resolve_lock_from_manifest(manifest, client, ui, &opts, project_root)?;
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

fn missing_cache_warning(pkg: &LockPackage) -> String {
    format!(
        "{} {} ({}): cache missing and source unavailable — omitted from staging",
        pkg.kind_label(),
        crate::fs_util::truncate_str(&pkg.cache_key, 12),
        pkg.url
    )
}

enum StepOutcome {
    Continue,
    Finished,
}

// Chain of Responsibility: each sync phase is isolated so new phases are additive instead of
// expanding `run_sync` into a larger coordinator.
trait SyncStep {
    fn run(&self, session: &mut SyncSession<'_>) -> Result<StepOutcome>;
}

struct DryRunStep;
struct PluginBackfillStep;
struct CacheAndIndexStep;
struct ReportNotesStep;
struct StageOrVerifyStep;
struct SummaryStep;

static DRY_RUN_STEP: DryRunStep = DryRunStep;
static PLUGIN_BACKFILL_STEP: PluginBackfillStep = PluginBackfillStep;
static CACHE_AND_INDEX_STEP: CacheAndIndexStep = CacheAndIndexStep;
static REPORT_NOTES_STEP: ReportNotesStep = ReportNotesStep;
static STAGE_OR_VERIFY_STEP: StageOrVerifyStep = StageOrVerifyStep;
static SUMMARY_STEP: SummaryStep = SummaryStep;

fn sync_steps() -> [&'static dyn SyncStep; 6] {
    [
        &DRY_RUN_STEP,
        &PLUGIN_BACKFILL_STEP,
        &CACHE_AND_INDEX_STEP,
        &REPORT_NOTES_STEP,
        &STAGE_OR_VERIFY_STEP,
        &SUMMARY_STEP,
    ]
}

impl SyncStep for DryRunStep {
    fn run(&self, session: &mut SyncSession<'_>) -> Result<StepOutcome> {
        if !session.mode.dry_run {
            return Ok(StepOutcome::Continue);
        }

        session.ui.message(format!(
            "Dry-run: would sync {} skill(s), {} plugin(s); {} skill(s) shadowed by plugins (omitted from staging); no changes made.",
            session.lock.skill_count(),
            session.lock.plugin_count(),
            session.shadowed_skills
        ));
        tracing::info!(
            skills = session.lock.skill_count(),
            plugins = session.lock.plugin_count(),
            shadowed_skills = session.shadowed_skills,
            "dry-run: would ensure cache and rebuild staging"
        );
        Ok(StepOutcome::Finished)
    }
}

impl SyncStep for PluginBackfillStep {
    fn run(&self, session: &mut SyncSession<'_>) -> Result<StepOutcome> {
        let mut lock_dirty = false;
        for plugin in session.lock.plugins_mut() {
            if plugin.url.is_empty() {
                tracing::warn!("skipping plugin row with empty url");
                continue;
            }
            if plugin.needs_backfill() {
                backfill_plugin_lock_entry(&session.client, plugin, session.ui)?;
                lock_dirty = true;
            }
        }
        if lock_dirty {
            session.lock.save(session.project_root)?;
        }
        Ok(StepOutcome::Continue)
    }
}

impl SyncStep for CacheAndIndexStep {
    fn run(&self, session: &mut SyncSession<'_>) -> Result<StepOutcome> {
        let fetched_at_unix = Utc::now().timestamp();
        let mut warnings = Vec::new();

        for pkg in &session.lock.packages {
            if pkg.cache_key.is_empty() {
                if pkg.kind == PackageKind::Plugin {
                    tracing::warn!("skipping plugin sync: empty cache_key");
                }
                continue;
            }
            if !ensure_lock_cached(&session.client, pkg, session.ui)? {
                warnings.push(missing_cache_warning(pkg));
            }
            upsert_entry(&pkg.cache_key, &index_record(pkg, fetched_at_unix), &[])?;
        }

        for warning in warnings {
            session.record_warning(warning);
        }

        Ok(StepOutcome::Continue)
    }
}

impl SyncStep for ReportNotesStep {
    fn run(&self, session: &mut SyncSession<'_>) -> Result<StepOutcome> {
        for warning in &session.warnings {
            if !session.ui.quiet {
                session.ui.message(format!("Warning: {warning}"));
            }
            tracing::warn!(message = %warning, "sync cache miss");
        }

        if session.shadowed_skills > 0 && !session.ui.quiet {
            session.ui.message(format!(
                "Note: {} skill(s) are shadowed by full plugin(s) and will not get separate staging hubs.",
                session.shadowed_skills
            ));
        }

        Ok(StepOutcome::Continue)
    }
}

impl SyncStep for StageOrVerifyStep {
    fn run(&self, session: &mut SyncSession<'_>) -> Result<StepOutcome> {
        if session.mode.verify_only {
            let spinner = session.ui.spinner("Verify staging layout…");
            staging::verify_staging(session.project_root, &session.lock)?;
            Ui::finish_spinner(spinner.as_ref(), "Staging checks passed");
            return Ok(StepOutcome::Finished);
        }

        let spinner = session.ui.spinner("Rebuild plugin staging (symlinks)…");
        staging::rebuild_staging(
            session.project_root,
            &session.lock,
            session.manifest.as_ref(),
        )?;
        staging::verify_staging(session.project_root, &session.lock)?;
        Ui::finish_spinner(spinner.as_ref(), "Staging ready");
        Ok(StepOutcome::Continue)
    }
}

impl SyncStep for SummaryStep {
    fn run(&self, session: &mut SyncSession<'_>) -> Result<StepOutcome> {
        let index_key_count = list_keys()?.len();
        tracing::debug!(
            index_keys = index_key_count,
            skills = session.lock.skill_count(),
            plugins = session.lock.plugin_count(),
            "sync complete"
        );
        if !session.ui.quiet {
            session.ui.message(format!(
                "Sync finished — {} skill(s), {} plugin(s), {} cache index entr(ies). One merged bundle: agentpack-bundle.",
                session.lock.skill_count(),
                session.lock.plugin_count(),
                index_key_count
            ));
        }
        Ok(StepOutcome::Continue)
    }
}
