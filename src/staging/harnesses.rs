use std::fs;
use std::path::{Path, PathBuf};

use crate::artifacts::HarnessTarget;
use crate::error::{AgentpackError, Result};
use crate::lockfile::PackLock;
use crate::manifest::AgentpackManifest;
use crate::paths::{
    cursor_workspace_dir, staging_codex_home_dir, staging_cursor_bundle_dir,
    staging_cursor_home_dir, staging_cursor_pack_plugin_dir, staging_opencode_dir,
    staging_plugins_dir, STAGED_AGENTPACK_BUNDLE_NAME,
};

use super::cursor::{
    finalize_cursor_staging, read_cursor_overlay_manifest, rebuild_cursor_staging_without_finalize,
};
use super::dot_agents::stage_dot_agents_overlay;
use super::pack_overlay::{stage_pack_plugins_for_target, stage_pack_skills_for_target};
use super::seed::{merge_user_settings_files_into_bundle, seed_codex_home, seed_opencode_root};

/// Strategy objects encapsulate per-harness staging so adding another harness is additive.
trait HarnessStager {
    fn reset_paths(&self, ctx: &StagingContext<'_>) -> Result<Vec<PathBuf>>;
    fn stage(&self, ctx: &StagingContext<'_>) -> Result<()>;
    fn finalize(&self, _ctx: &StagingContext<'_>) -> Result<()> {
        Ok(())
    }
    fn verify(&self, ctx: &StagingContext<'_>) -> Result<()>;
}

pub(super) struct StagingContext<'a> {
    project_root: &'a Path,
    lock: &'a PackLock,
    manifest: Option<&'a AgentpackManifest>,
}

impl<'a> StagingContext<'a> {
    pub(super) fn new(
        project_root: &'a Path,
        lock: &'a PackLock,
        manifest: Option<&'a AgentpackManifest>,
    ) -> Self {
        Self {
            project_root,
            lock,
            manifest,
        }
    }

    pub(super) fn claude_bundle_dir(&self) -> Result<PathBuf> {
        Ok(staging_plugins_dir(self.project_root)?.join(STAGED_AGENTPACK_BUNDLE_NAME))
    }

    pub(super) fn opencode_root(&self) -> Result<PathBuf> {
        staging_opencode_dir(self.project_root)
    }

    pub(super) fn codex_home(&self) -> Result<PathBuf> {
        staging_codex_home_dir(self.project_root)
    }

    pub(super) fn cursor_bundle_root(&self) -> Result<PathBuf> {
        staging_cursor_bundle_dir(self.project_root)
    }

    pub(super) fn cursor_pack_plugin_dir(&self) -> Result<PathBuf> {
        staging_cursor_pack_plugin_dir(self.project_root)
    }

    pub(super) fn cursor_home(&self) -> Result<PathBuf> {
        staging_cursor_home_dir(self.project_root)
    }
}

pub(super) struct StagingPipeline<'a> {
    ctx: StagingContext<'a>,
}

impl<'a> StagingPipeline<'a> {
    pub(super) fn new(
        project_root: &'a Path,
        lock: &'a PackLock,
        manifest: Option<&'a AgentpackManifest>,
    ) -> Self {
        Self {
            ctx: StagingContext::new(project_root, lock, manifest),
        }
    }

    pub(super) fn rebuild(&self) -> Result<Vec<PathBuf>> {
        self.reset_all()?;
        for stage in harness_stagers() {
            stage.stage(&self.ctx)?;
        }

        stage_dot_agents_overlay(self.ctx.project_root)?;

        for stage in harness_stagers() {
            stage.finalize(&self.ctx)?;
        }

        Ok(vec![self.ctx.claude_bundle_dir()?])
    }

    pub(super) fn verify(&self) -> Result<()> {
        for stage in harness_stagers() {
            stage.verify(&self.ctx)?;
        }
        Ok(())
    }

    /// Delegate path accessors through to `StagingContext` for callers in `staging/mod.rs`.
    pub(super) fn opencode_root(&self) -> Result<PathBuf> {
        self.ctx.opencode_root()
    }

    pub(super) fn codex_home(&self) -> Result<PathBuf> {
        self.ctx.codex_home()
    }

    pub(super) fn cursor_pack_plugin_dir(&self) -> Result<PathBuf> {
        self.ctx.cursor_pack_plugin_dir()
    }

    fn reset_all(&self) -> Result<()> {
        let mut reset_paths = Vec::new();
        for stage in harness_stagers() {
            reset_paths.extend(stage.reset_paths(&self.ctx)?);
        }

        reset_paths.sort();
        reset_paths.dedup();

        for path in reset_paths {
            if path.exists() {
                fs::remove_dir_all(&path).map_err(|err| AgentpackError::io(&path, err))?;
            }
        }
        Ok(())
    }
}

struct ClaudeBundleStager;
struct OpenCodeStager;
struct CodexHomeStager;
struct CursorStager;

static CLAUDE_BUNDLE_STAGER: ClaudeBundleStager = ClaudeBundleStager;
static OPENCODE_STAGER: OpenCodeStager = OpenCodeStager;
static CODEX_HOME_STAGER: CodexHomeStager = CodexHomeStager;
static CURSOR_STAGER: CursorStager = CursorStager;

fn harness_stagers() -> [&'static dyn HarnessStager; 4] {
    [
        &CLAUDE_BUNDLE_STAGER,
        &OPENCODE_STAGER,
        &CODEX_HOME_STAGER,
        &CURSOR_STAGER,
    ]
}

fn stage_target_tree(ctx: &StagingContext<'_>, root: &Path, target: HarnessTarget) -> Result<()> {
    stage_pack_plugins_for_target(ctx.lock, root, target, ctx.manifest)?;
    stage_pack_skills_for_target(ctx.lock, root, target, ctx.manifest)?;
    Ok(())
}

fn staging_require(cond: bool, message: impl FnOnce() -> String) -> Result<()> {
    if !cond {
        return Err(AgentpackError::Staging(message()));
    }
    Ok(())
}

fn write_bundle_manifest(bundle: &Path) -> Result<()> {
    let plugin_dir = bundle.join(".claude-plugin");
    fs::create_dir_all(&plugin_dir).map_err(|err| AgentpackError::io(&plugin_dir, err))?;
    let manifest = r#"{"name":"agentpack-bundle","version":"1.0.0","description":"Merged pack.lock plugins/skills; optional user settings.json and .claude.json"}"#;
    let plugin_json = plugin_dir.join("plugin.json");
    fs::write(&plugin_json, manifest).map_err(|err| AgentpackError::io(&plugin_json, err))?;
    Ok(())
}

impl HarnessStager for ClaudeBundleStager {
    fn reset_paths(&self, ctx: &StagingContext<'_>) -> Result<Vec<PathBuf>> {
        Ok(vec![staging_plugins_dir(ctx.project_root)?])
    }

    fn stage(&self, ctx: &StagingContext<'_>) -> Result<()> {
        let plugins_base = staging_plugins_dir(ctx.project_root)?;
        fs::create_dir_all(&plugins_base).map_err(|err| AgentpackError::io(&plugins_base, err))?;

        let bundle = ctx.claude_bundle_dir()?;
        fs::create_dir_all(&bundle).map_err(|err| AgentpackError::io(&bundle, err))?;
        write_bundle_manifest(&bundle)?;
        merge_user_settings_files_into_bundle(&bundle)?;
        stage_target_tree(ctx, &bundle, HarnessTarget::Claude)
    }

    fn verify(&self, ctx: &StagingContext<'_>) -> Result<()> {
        let bundle = ctx.claude_bundle_dir()?;
        staging_require(bundle.join(".claude-plugin/plugin.json").is_file(), || {
            format!("bundle missing manifest {}", bundle.display())
        })
    }
}

impl HarnessStager for OpenCodeStager {
    fn reset_paths(&self, ctx: &StagingContext<'_>) -> Result<Vec<PathBuf>> {
        Ok(vec![ctx.opencode_root()?])
    }

    fn stage(&self, ctx: &StagingContext<'_>) -> Result<()> {
        let root = ctx.opencode_root()?;
        fs::create_dir_all(&root).map_err(|err| AgentpackError::io(&root, err))?;
        seed_opencode_root(&root)?;
        stage_target_tree(ctx, &root, HarnessTarget::OpenCode)
    }

    fn verify(&self, ctx: &StagingContext<'_>) -> Result<()> {
        let root = ctx.opencode_root()?;
        staging_require(root.is_dir(), || {
            format!("opencode staging missing {}", root.display())
        })
    }
}

impl HarnessStager for CodexHomeStager {
    fn reset_paths(&self, ctx: &StagingContext<'_>) -> Result<Vec<PathBuf>> {
        Ok(vec![ctx.codex_home()?])
    }

    fn stage(&self, ctx: &StagingContext<'_>) -> Result<()> {
        let root = ctx.codex_home()?;
        fs::create_dir_all(&root).map_err(|err| AgentpackError::io(&root, err))?;
        seed_codex_home(&root)?;
        stage_target_tree(ctx, &root, HarnessTarget::Codex)
    }

    fn verify(&self, ctx: &StagingContext<'_>) -> Result<()> {
        let root = ctx.codex_home()?;
        staging_require(root.is_dir(), || {
            format!("codex home staging missing {}", root.display())
        })
    }
}

impl HarnessStager for CursorStager {
    fn reset_paths(&self, ctx: &StagingContext<'_>) -> Result<Vec<PathBuf>> {
        Ok(vec![ctx.cursor_bundle_root()?, ctx.cursor_home()?])
    }

    fn stage(&self, ctx: &StagingContext<'_>) -> Result<()> {
        rebuild_cursor_staging_without_finalize(ctx.project_root, ctx.lock, ctx.manifest)
    }

    fn finalize(&self, ctx: &StagingContext<'_>) -> Result<()> {
        finalize_cursor_staging(ctx.project_root)
    }

    fn verify(&self, ctx: &StagingContext<'_>) -> Result<()> {
        let bundle_root = ctx.cursor_bundle_root()?;
        let pack_plugin = ctx.cursor_pack_plugin_dir()?;
        let home = ctx.cursor_home()?;

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

        for rel in read_cursor_overlay_manifest(ctx.project_root)? {
            let tracked = cursor_workspace_dir(ctx.project_root).join(&rel);
            if !tracked.exists() {
                return Err(AgentpackError::Staging(format!(
                    "cursor workspace overlay missing at {} (from cursor-overlay.manifest entry {})",
                    tracked.display(),
                    rel.display()
                )));
            }
        }

        Ok(())
    }
}
