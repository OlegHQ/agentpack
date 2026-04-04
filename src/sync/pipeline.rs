use std::path::Path;

use chrono::Utc;
use reqwest::blocking::Client;

use crate::cache::{
    backfill_plugin_lock_entry, ensure_lock_plugin_cached, ensure_lock_skill_cached,
};
use crate::error::Result;
use crate::index::{list_keys, upsert_entry, CacheEntryRecord};
use crate::lockfile::{LockPlugin, LockSkill, PackLock, PackageKind};
use crate::manifest::AgentpackManifest;
use crate::paths;
use crate::resolve::resolve_lock_from_manifest;
use crate::staging::{self, skill_is_shadowed};
use crate::ui::Ui;

use super::add_fetch::http_client;

pub fn run_sync(project_root: &Path, dry_run: bool, verify_only: bool, ui: &Ui) -> Result<()> {
    let mut session = SyncSession::prepare(
        project_root,
        SyncMode {
            dry_run,
            verify_only,
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
            maybe_refresh_lock_from_manifest(project_root, manifest.as_ref(), &client, ui)?;
        }

        let lock = PackLock::load(project_root)?;
        let shadowed_skills = lock
            .skills
            .iter()
            .filter(|skill| skill_is_shadowed(skill, &lock.plugins))
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
) -> Result<()> {
    let Some(manifest) = manifest else {
        return Ok(());
    };
    // Empty `[dependencies]` means the lock is authoritative (e.g. tests or hand-edited v2).
    if manifest.dependencies.is_empty() {
        return Ok(());
    }
    let resolved = resolve_lock_from_manifest(manifest, client, ui)?;
    resolved.save(project_root)
}

enum SyncEntry<'a> {
    Plugin(&'a LockPlugin),
    Skill(&'a LockSkill),
}

impl<'a> SyncEntry<'a> {
    fn ensure_cached(&self, client: &Client, ui: &Ui) -> Result<bool> {
        match self {
            Self::Plugin(plugin) => ensure_lock_plugin_cached(client, plugin, ui),
            Self::Skill(skill) => ensure_lock_skill_cached(client, skill, ui),
        }
    }

    fn index_record(&self, fetched_at_unix: i64) -> CacheEntryRecord {
        match self {
            Self::Plugin(plugin) => CacheEntryRecord {
                kind: PackageKind::Plugin,
                source_url: plugin.url.clone(),
                owner: plugin.owner.clone(),
                repo: plugin.repo.clone(),
                path: plugin.path.clone(),
                commit: plugin.commit.clone(),
                fetched_at_unix,
            },
            Self::Skill(skill) => CacheEntryRecord {
                kind: PackageKind::Skill,
                source_url: skill.url.clone(),
                owner: skill.owner.clone(),
                repo: skill.repo.clone(),
                path: skill.path.clone(),
                commit: skill.commit.clone(),
                fetched_at_unix,
            },
        }
    }

    fn cache_key(&self) -> &str {
        match self {
            Self::Plugin(plugin) => &plugin.cache_key,
            Self::Skill(skill) => &skill.cache_key,
        }
    }

    fn url(&self) -> &str {
        match self {
            Self::Plugin(plugin) => &plugin.url,
            Self::Skill(skill) => &skill.url,
        }
    }

    fn kind_label(&self) -> &'static str {
        match self {
            Self::Plugin(_) => "plugin",
            Self::Skill(_) => "skill",
        }
    }

    fn should_skip(&self) -> bool {
        matches!(self, Self::Plugin(plugin) if plugin.cache_key.is_empty())
    }

    fn log_skip(&self) {
        if matches!(self, Self::Plugin(_)) {
            tracing::warn!("skipping plugin sync: empty cache_key");
        }
    }

    fn missing_cache_warning(&self) -> String {
        format!(
            "{} {} ({}): cache missing and source unavailable — omitted from staging",
            self.kind_label(),
            crate::fs_util::truncate_str(self.cache_key(), 12),
            self.url()
        )
    }
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
            session.lock.skills.len(),
            session.lock.plugins.len(),
            session.shadowed_skills
        ));
        tracing::info!(
            skills = session.lock.skills.len(),
            plugins = session.lock.plugins.len(),
            shadowed_skills = session.shadowed_skills,
            "dry-run: would ensure cache and rebuild staging"
        );
        Ok(StepOutcome::Finished)
    }
}

impl SyncStep for PluginBackfillStep {
    fn run(&self, session: &mut SyncSession<'_>) -> Result<StepOutcome> {
        let mut lock_dirty = false;
        for plugin in &mut session.lock.plugins {
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

        for plugin in &session.lock.plugins {
            let entry = SyncEntry::Plugin(plugin);
            if entry.should_skip() {
                entry.log_skip();
                continue;
            }
            if !entry.ensure_cached(&session.client, session.ui)? {
                warnings.push(entry.missing_cache_warning());
            }
            upsert_entry(entry.cache_key(), &entry.index_record(fetched_at_unix), &[])?;
        }

        for skill in &session.lock.skills {
            let entry = SyncEntry::Skill(skill);
            if !entry.ensure_cached(&session.client, session.ui)? {
                warnings.push(entry.missing_cache_warning());
            }
            upsert_entry(entry.cache_key(), &entry.index_record(fetched_at_unix), &[])?;
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
            skills = session.lock.skills.len(),
            plugins = session.lock.plugins.len(),
            "sync complete"
        );
        if !session.ui.quiet {
            session.ui.message(format!(
                "Sync finished — {} skill(s), {} plugin(s), {} cache index entr(ies). One merged bundle: agentpack-bundle.",
                session.lock.skills.len(),
                session.lock.plugins.len(),
                index_key_count
            ));
        }
        Ok(StepOutcome::Continue)
    }
}
