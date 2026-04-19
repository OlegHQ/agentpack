use std::fs;
use std::path::{Path, PathBuf};

use crate::error::{AgentpackError, Result};
use crate::hooks::stage::{stage_hooks_all_harnesses, HookHarnessRoots};
use crate::lockfile::PackLock;
use crate::manifest::AgentpackManifest;
use crate::mode::filter::EffectiveMode;
use crate::paths::{
    cursor_workspace_dir, staging_codex_home_dir_for_mode, staging_cursor_bundle_dir_for_mode,
    staging_cursor_home_dir_for_mode, staging_cursor_pack_plugin_dir_for_mode,
    staging_opencode_dir_for_mode, staging_plugins_dir_for_mode, STAGED_AGENTPACK_BUNDLE_NAME,
};

use super::cursor::{
    finalize_cursor_staging, prepare_cursor_staging_without_pack_overlay,
    read_cursor_overlay_manifest, write_cursor_pack_plugin_readme,
};
use super::dot_agents::stage_dot_agents_overlay;
use super::pack_overlay::{
    stage_pack_plugins_all_harnesses, stage_pack_skills_all_harnesses, PackHarnessRoots,
};
use super::seed::{merge_user_settings_files_into_bundle, seed_codex_home, seed_opencode_root};

pub(super) struct StagingPipeline<'a> {
    project_root: &'a Path,
    lock: &'a PackLock,
    manifest: Option<&'a AgentpackManifest>,
    mode: &'a EffectiveMode,
}

impl<'a> StagingPipeline<'a> {
    pub(super) fn new(
        project_root: &'a Path,
        lock: &'a PackLock,
        manifest: Option<&'a AgentpackManifest>,
        mode: &'a EffectiveMode,
    ) -> Self {
        Self {
            project_root,
            lock,
            manifest,
            mode,
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
        let pack_dests = PackHarnessRoots {
            claude_bundle: &claude_bundle,
            opencode: &opencode,
            codex: &codex,
            cursor_pack: &cursor_pack,
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
            },
        )?;
        write_cursor_pack_plugin_readme(&cursor_pack)?;
        stage_dot_agents_overlay(self.project_root, self.mode.name(), self.mode)?;
        super::mcp::stage_merged_mcp(
            self.project_root,
            self.lock,
            self.manifest,
            self.mode,
            &pack_dests,
        )?;
        super::guidance::stage_guidance_all_harnesses(
            self.project_root,
            self.lock,
            self.mode,
            &pack_dests,
        )?;
        finalize_cursor_staging(self.project_root, self.mode.name())?;

        Ok(vec![self.claude_bundle_dir()?])
    }

    pub(super) fn verify(&self) -> Result<()> {
        // Claude bundle
        let bundle = self.claude_bundle_dir()?;
        staging_require(bundle.join(".claude-plugin/plugin.json").is_file(), || {
            format!("bundle missing manifest {}", bundle.display())
        })?;

        // OpenCode
        let root = self.opencode_root()?;
        staging_require(root.is_dir(), || {
            format!("opencode staging missing {}", root.display())
        })?;

        // Codex home
        let root = self.codex_home()?;
        staging_require(root.is_dir(), || {
            format!("codex home staging missing {}", root.display())
        })?;

        // Cursor
        let bundle_root = self.cursor_bundle_root()?;
        let pack_plugin = self.cursor_pack_plugin_dir()?;
        let home = self.cursor_home()?;
        staging_require(bundle_root.is_dir(), || {
            format!("cursor staging missing {}", bundle_root.display())
        })?;
        staging_require(
            pack_plugin.join(".cursor-plugin/plugin.json").is_file(),
            || {
                format!(
                    "cursor pack plugin missing {}",
                    pack_plugin.join(".cursor-plugin/plugin.json").display()
                )
            },
        )?;
        staging_require(
            bundle_root
                .join(".cursor-plugin/marketplace.json")
                .is_file(),
            || {
                format!(
                    "cursor staging missing {}",
                    bundle_root
                        .join(".cursor-plugin/marketplace.json")
                        .display()
                )
            },
        )?;
        staging_require(home.join(".cursor").is_dir(), || {
            format!("cursor fake home missing .cursor/ under {}", home.display())
        })?;
        for rel in read_cursor_overlay_manifest(self.project_root)? {
            let tracked = cursor_workspace_dir(self.project_root).join(&rel);
            if !tracked.exists() {
                return Err(AgentpackError::Staging(format!(
                    "cursor workspace overlay missing at {} (from cursor-overlay.manifest entry {})",
                    tracked.display(), rel.display()
                )));
            }
        }
        Ok(())
    }

    fn prepare_all(&self) -> Result<()> {
        // Claude bundle
        let plugins_base = staging_plugins_dir_for_mode(self.project_root, self.mode.name())?;
        fs::create_dir_all(&plugins_base).map_err(|e| AgentpackError::io(&plugins_base, e))?;
        let bundle = self.claude_bundle_dir()?;
        fs::create_dir_all(&bundle).map_err(|e| AgentpackError::io(&bundle, e))?;
        write_bundle_manifest(&bundle)?;
        merge_user_settings_files_into_bundle(&bundle)?;

        // OpenCode
        let root = self.opencode_root()?;
        fs::create_dir_all(&root).map_err(|e| AgentpackError::io(&root, e))?;
        seed_opencode_root(&root)?;

        // Codex home
        let root = self.codex_home()?;
        fs::create_dir_all(&root).map_err(|e| AgentpackError::io(&root, e))?;
        seed_codex_home(&root)?;

        // Cursor
        prepare_cursor_staging_without_pack_overlay(self.project_root, self.mode.name())?;
        Ok(())
    }

    fn reset_all(&self) -> Result<()> {
        let mut paths = vec![
            staging_plugins_dir_for_mode(self.project_root, self.mode.name())?,
            self.opencode_root()?,
            self.codex_home()?,
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

fn staging_require(cond: bool, message: impl FnOnce() -> String) -> Result<()> {
    if !cond {
        return Err(AgentpackError::Staging(message()));
    }
    Ok(())
}

fn write_bundle_manifest(bundle: &Path) -> Result<()> {
    let plugin_dir = bundle.join(".claude-plugin");
    fs::create_dir_all(&plugin_dir).map_err(|e| AgentpackError::io(&plugin_dir, e))?;
    let manifest = r#"{"name":"agentpack-bundle","version":"1.0.0","description":"Merged pack.lock plugins/skills; optional user settings.json and .claude.json"}"#;
    let plugin_json = plugin_dir.join("plugin.json");
    fs::write(&plugin_json, manifest).map_err(|e| AgentpackError::io(&plugin_json, e))?;
    Ok(())
}
