use std::fs;
use std::path::{Path, PathBuf};

use crate::error::{AgentpackError, Result};
use crate::hooks::stage::stage_hooks_all_harnesses;
use crate::lockfile::PackLock;
use crate::manifest::AgentpackManifest;
use crate::mode::filter::EffectiveMode;
use crate::paths::{
    staging_codex_home_dir_for_mode, staging_cursor_pack_plugin_dir_for_mode,
    staging_grok_bundle_dir_for_mode, staging_grok_home_dir_for_mode,
    staging_opencode_dir_for_mode, staging_plugins_dir_for_mode, STAGED_AGENTPACK_BUNDLE_NAME,
};

use super::agy::finalize_agy_staging;
use super::claude_home::set_claude_settings_mcp_allowlist;
use super::cursor::{
    finalize_cursor_staging_common, finalize_cursor_workspace_overlay,
    write_cursor_pack_plugin_readme,
};
use super::dot_agents::stage_dot_agents_overlay;
use super::keep_attribution;
use super::pack_overlay::{
    stage_pack_plugins_all_harnesses, stage_pack_skills_all_harnesses, PackHarnessRoots,
};
use super::HarnessTarget;

pub(super) struct StagingPipeline<'a> {
    project_root: &'a Path,
    lock: &'a PackLock,
    manifest: Option<&'a AgentpackManifest>,
    mode: &'a EffectiveMode,
    target: Option<HarnessTarget>,
}

impl<'a> StagingPipeline<'a> {
    pub(super) fn new(
        project_root: &'a Path,
        lock: &'a PackLock,
        manifest: Option<&'a AgentpackManifest>,
        mode: &'a EffectiveMode,
        target: Option<HarnessTarget>,
    ) -> Self {
        Self {
            project_root,
            lock,
            manifest,
            mode,
            target,
        }
    }

    fn claude_bundle_dir(&self) -> Result<PathBuf> {
        Ok(
            staging_plugins_dir_for_mode(self.project_root, self.mode.name())?
                .join(STAGED_AGENTPACK_BUNDLE_NAME),
        )
    }

    pub(super) fn opencode_root(&self) -> Result<PathBuf> {
        staging_opencode_dir_for_mode(self.project_root, self.mode.name())
    }

    pub(super) fn codex_home(&self) -> Result<PathBuf> {
        staging_codex_home_dir_for_mode(self.project_root, self.mode.name())
    }

    pub(super) fn cursor_pack_plugin_dir(&self) -> Result<PathBuf> {
        staging_cursor_pack_plugin_dir_for_mode(self.project_root, self.mode.name())
    }

    pub(super) fn grok_home(&self) -> Result<PathBuf> {
        staging_grok_home_dir_for_mode(self.project_root, self.mode.name())
    }

    pub(super) fn grok_bundle_dir(&self) -> Result<PathBuf> {
        staging_grok_bundle_dir_for_mode(self.project_root, self.mode.name())
    }

    pub(super) fn agy_bundle_dir(&self) -> Result<PathBuf> {
        crate::paths::staging_agy_bundle_dir_for_mode(self.project_root, self.mode.name())
    }

    pub(super) fn rebuild(&self) -> Result<Vec<PathBuf>> {
        self.reset_all()?;
        self.prepare_all()?;

        let claude_bundle = self.claude_bundle_dir()?;
        let opencode = self.opencode_root()?;
        let codex = self.codex_home()?;
        let cursor_pack = self.cursor_pack_plugin_dir()?;
        let grok_home = self.grok_home()?;
        let grok_bundle = self.grok_bundle_dir()?;
        let agy_bundle = self.agy_bundle_dir()?;
        let pack_dests = PackHarnessRoots {
            claude_bundle: &claude_bundle,
            opencode: &opencode,
            codex: &codex,
            cursor_pack: &cursor_pack,
            grok_home: &grok_home,
            grok_bundle: &grok_bundle,
            agy_bundle: &agy_bundle,
        };
        stage_pack_plugins_all_harnesses(self.lock, &pack_dests, self.mode)?;
        stage_pack_skills_all_harnesses(self.lock, &pack_dests, self.mode)?;
        stage_hooks_all_harnesses(self.project_root, self.lock, self.mode)?;
        write_cursor_pack_plugin_readme(&cursor_pack)?;
        stage_dot_agents_overlay(self.project_root, self.mode.name(), self.mode)?;
        // Merge once, then let each harness render its own native format.
        let merged_mcp =
            super::mcp::collect_merged_mcp(self.project_root, self.lock, self.manifest, Some(self.mode))?;
        if !merged_mcp.is_empty() {
            let ctx = self.stage_ctx();
            for harness in crate::harness::all() {
                harness.write_mcp(&merged_mcp, &ctx)?;
            }
            if !keep_attribution() {
                let names: Vec<String> = merged_mcp.keys().cloned().collect();
                set_claude_settings_mcp_allowlist(&names)?;
            }
        }
        super::guidance::stage_guidance_all_harnesses(
            self.project_root,
            self.lock,
            self.mode,
            &pack_dests,
        )?;
        finalize_cursor_staging_common(self.project_root, self.mode.name(), &merged_mcp)?;
        if matches!(self.target, Some(HarnessTarget::Cursor)) {
            finalize_cursor_workspace_overlay(self.project_root, self.mode.name())?;
        }
        if matches!(self.target, Some(HarnessTarget::Agy)) {
            finalize_agy_staging(self.project_root, self.mode.name())?;
        }

        Ok(vec![self.claude_bundle_dir()?])
    }

    /// Borrowed context handed to each [`Harness`](crate::harness::Harness) staging method.
    fn stage_ctx(&self) -> crate::harness::StageCtx<'a> {
        crate::harness::StageCtx {
            project_root: self.project_root,
            mode: self.mode,
            launch_target: self.target,
        }
    }

    pub(super) fn verify(&self) -> Result<()> {
        let ctx = self.stage_ctx();
        for harness in crate::harness::all() {
            harness.verify(&ctx)?;
        }
        Ok(())
    }

    fn prepare_all(&self) -> Result<()> {
        let ctx = self.stage_ctx();
        for harness in crate::harness::all() {
            harness.prepare(&ctx)?;
        }
        Ok(())
    }

    fn reset_all(&self) -> Result<()> {
        let mode = self.mode.name();
        let mut paths = Vec::new();
        for harness in crate::harness::all() {
            paths.extend(harness.reset_paths(self.project_root, mode)?);
        }
        paths.sort();
        paths.dedup();
        for path in paths {
            if path.exists() {
                fs::remove_dir_all(&path).map_err(|e| AgentpackError::io(&path, e))?;
            }
        }
        Ok(())
    }
}
