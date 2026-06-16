use std::path::{Path, PathBuf};

use crate::error::Result;
use crate::fs_util::remove_rebuild_path;
use crate::harness::HarnessTarget;
use crate::hooks::stage::stage_hooks_all_harnesses;
use crate::lockfile::PackLock;
use crate::manifest::AgentpackManifest;
use crate::mode::filter::EffectiveMode;
use crate::paths::{staging_plugins_dir_for_mode, STAGED_AGENTPACK_BUNDLE_NAME};

use super::dot_agents::stage_dot_agents_overlay;
use super::pack_overlay::{stage_pack_plugins_all_harnesses, stage_pack_skills_all_harnesses};

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

    pub(super) fn rebuild(&self) -> Result<Vec<PathBuf>> {
        self.reset_all()?;
        self.prepare_all()?;

        // Each harness's pack-content root, derived from the registry — the single-walk pack/skill
        // staging fans out to these without any harness-specific knowledge in `staging`.
        let mode = self.mode.name();
        let owned_roots: Vec<(HarnessTarget, PathBuf)> = crate::harness::all()
            .iter()
            .map(|h| Ok((h.id(), h.staged_root(self.project_root, mode)?)))
            .collect::<Result<_>>()?;
        let pack_dests: Vec<(HarnessTarget, &Path)> =
            owned_roots.iter().map(|(t, p)| (*t, p.as_path())).collect();
        stage_pack_plugins_all_harnesses(self.lock, &pack_dests, self.mode)?;
        stage_pack_skills_all_harnesses(self.lock, &pack_dests, self.mode)?;
        stage_hooks_all_harnesses(self.project_root, self.lock, self.mode)?;
        stage_dot_agents_overlay(self.project_root, self.mode.name(), self.mode)?;
        // Merge once, then let each harness render its own native format and run any post-staging
        // finalize (Claude MCP allowlist; Cursor fake-home + approvals).
        let merged_mcp = super::mcp::collect_merged_mcp(
            self.project_root,
            self.lock,
            self.manifest,
            Some(self.mode),
        )?;
        let ctx = self.stage_ctx();
        if !merged_mcp.is_empty() {
            for harness in crate::harness::all() {
                harness.write_mcp(&merged_mcp, &ctx)?;
            }
        }
        // Collect always-apply guidance once, then let each harness inject it natively.
        if let Some(blob) =
            super::guidance::collect_guidance_blob(self.project_root, self.lock, self.mode)?
        {
            for harness in crate::harness::all() {
                harness.inject_guidance(&blob, &ctx)?;
            }
        }
        for harness in crate::harness::all() {
            harness.finalize(&merged_mcp, &ctx)?;
        }
        // Workspace overlays (Cursor `.cursor/agents`, Agy `.agents/plugins/...`) are only created
        // for the harness being launched; each impl knows its own overlay (default: none).
        if let Some(target) = self.target {
            target.harness().finalize_workspace_overlay(&ctx)?;
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
            remove_rebuild_path(&path)?;
        }
        Ok(())
    }
}
