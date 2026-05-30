use std::fs;
use std::path::{Path, PathBuf};

use crate::error::{AgentpackError, Result};
use crate::hooks::stage::{stage_hooks_all_harnesses, HookHarnessRoots};
use crate::lockfile::PackLock;
use crate::manifest::AgentpackManifest;
use crate::mode::filter::EffectiveMode;
use crate::paths::{
    staging_codex_home_dir_for_mode, staging_cursor_bundle_dir_for_mode,
    staging_cursor_home_dir_for_mode, staging_cursor_pack_plugin_dir_for_mode,
    staging_grok_bundle_dir_for_mode, staging_grok_dir_for_mode, staging_grok_home_dir_for_mode,
    staging_opencode_dir_for_mode, staging_plugins_dir_for_mode, STAGED_AGENTPACK_BUNDLE_NAME,
};

use super::agy::{finalize_agy_staging, prepare_agy_staging_without_pack_overlay};
use super::attribution::{
    force_agy_attribution_off, force_codex_attribution_off, force_cursor_attribution_off,
    force_grok_attribution_off, force_opencode_attribution_off,
};
use super::claude_home::{materialize_claude_settings_overlay, set_claude_settings_mcp_allowlist};
use super::cursor::{
    finalize_cursor_staging_common, finalize_cursor_workspace_overlay,
    prepare_cursor_staging_without_pack_overlay, write_cursor_pack_plugin_readme,
};
use super::dot_agents::stage_dot_agents_overlay;
use super::pack_overlay::{
    stage_pack_plugins_all_harnesses, stage_pack_skills_all_harnesses, PackHarnessRoots,
};
use super::keep_attribution;
use super::seed::{seed_codex_home, seed_grok_home, seed_opencode_root};
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

    fn cursor_bundle_root(&self) -> Result<PathBuf> {
        staging_cursor_bundle_dir_for_mode(self.project_root, self.mode.name())
    }

    fn cursor_home(&self) -> Result<PathBuf> {
        staging_cursor_home_dir_for_mode(self.project_root, self.mode.name())
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
        stage_hooks_all_harnesses(
            self.project_root,
            self.lock,
            self.mode,
            &HookHarnessRoots {
                claude_bundle: &claude_bundle,
                opencode_root: &opencode,
                codex_home: &codex,
                cursor_pack: &cursor_pack,
                grok_bundle: &grok_bundle,
                agy_bundle: &agy_bundle,
            },
        )?;
        write_cursor_pack_plugin_readme(&cursor_pack)?;
        stage_dot_agents_overlay(self.project_root, self.mode.name(), self.mode)?;
        let merged_mcp = super::mcp::stage_merged_mcp(
            self.project_root,
            self.lock,
            self.manifest,
            self.mode,
            &pack_dests,
        )?;
        if !merged_mcp.is_empty() && !keep_attribution() {
            let names: Vec<String> = merged_mcp.keys().cloned().collect();
            set_claude_settings_mcp_allowlist(&names)?;
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
        // Claude bundle (loaded via `--plugin-dir`; user-settings live in the staged config dir).
        let plugins_base = staging_plugins_dir_for_mode(self.project_root, self.mode.name())?;
        fs::create_dir_all(&plugins_base).map_err(|e| AgentpackError::io(&plugins_base, e))?;
        let bundle = self.claude_bundle_dir()?;
        fs::create_dir_all(&bundle).map_err(|e| AgentpackError::io(&bundle, e))?;
        write_bundle_manifest(&bundle)?;

        // Claude attribution overlay (consumed by the launcher via `claude --settings <path>`).
        materialize_claude_settings_overlay()?;

        // OpenCode
        let root = self.opencode_root()?;
        fs::create_dir_all(&root).map_err(|e| AgentpackError::io(&root, e))?;
        seed_opencode_root(&root)?;
        force_opencode_attribution_off(&root)?;

        // Codex home
        let root = self.codex_home()?;
        fs::create_dir_all(&root).map_err(|e| AgentpackError::io(&root, e))?;
        seed_codex_home(&root)?;
        force_codex_attribution_off(&root)?;

        // Grok
        let grok_dir = staging_grok_dir_for_mode(self.project_root, self.mode.name())?;
        fs::create_dir_all(&grok_dir).map_err(|e| AgentpackError::io(&grok_dir, e))?;
        let grok_bundle = self.grok_bundle_dir()?;
        fs::create_dir_all(&grok_bundle).map_err(|e| AgentpackError::io(&grok_bundle, e))?;
        write_simple_plugin_manifest(&grok_bundle)?;
        let grok_home = self.grok_home()?;
        fs::create_dir_all(&grok_home).map_err(|e| AgentpackError::io(&grok_home, e))?;
        seed_grok_home(&grok_home, &grok_bundle)?;
        force_grok_attribution_off(&grok_home)?;

        // Cursor
        prepare_cursor_staging_without_pack_overlay(self.project_root, self.mode.name())?;
        let cursor_pack = self.cursor_pack_plugin_dir()?;
        let cursor_bundle = self.cursor_bundle_root()?;
        force_cursor_attribution_off(&cursor_bundle)?;
        force_cursor_attribution_off(&cursor_pack)?;

        // Antigravity
        prepare_agy_staging_without_pack_overlay(self.project_root, self.mode.name())?;
        force_agy_attribution_off(&self.agy_bundle_dir()?)?;
        Ok(())
    }

    fn reset_all(&self) -> Result<()> {
        let mut paths = vec![
            staging_plugins_dir_for_mode(self.project_root, self.mode.name())?,
            self.opencode_root()?,
            self.codex_home()?,
            self.grok_home()?,
            staging_grok_dir_for_mode(self.project_root, self.mode.name())?,
            crate::paths::staging_agy_dir_for_mode(self.project_root, self.mode.name())?,
            self.cursor_bundle_root()?,
            self.cursor_home()?,
        ];
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

fn write_bundle_manifest(bundle: &Path) -> Result<()> {
    let plugin_dir = bundle.join(".claude-plugin");
    fs::create_dir_all(&plugin_dir).map_err(|e| AgentpackError::io(&plugin_dir, e))?;
    let manifest = r#"{"name":"agentpack-bundle","version":"1.0.0","description":"Merged pack.lock plugins/skills; optional user settings.json and .claude.json"}"#;
    let plugin_json = plugin_dir.join("plugin.json");
    fs::write(&plugin_json, manifest).map_err(|e| AgentpackError::io(&plugin_json, e))?;
    Ok(())
}

fn write_simple_plugin_manifest(bundle: &Path) -> Result<()> {
    let plugin_json = bundle.join("plugin.json");
    let manifest = r#"{"name":"agentpack-bundle"}"#;
    fs::write(&plugin_json, manifest).map_err(|e| AgentpackError::io(&plugin_json, e))?;
    Ok(())
}
