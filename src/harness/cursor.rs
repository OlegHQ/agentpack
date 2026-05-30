use std::path::{Path, PathBuf};

use serde_norway::Mapping;

use super::{require, Harness, HarnessTarget, StageCtx};
use crate::artifacts::yaml::insert_string;
use crate::artifacts::ArtifactKind;
use crate::error::{AgentpackError, Result};
use crate::paths::{
    cursor_workspace_dir, staging_cursor_bundle_dir_for_mode, staging_cursor_home_dir_for_mode,
    staging_cursor_pack_plugin_dir_for_mode,
};
use crate::staging::read_cursor_overlay_manifest;

/// Cursor: pack plugin tree plus a fake `HOME` and an optional workspace `.cursor/agents` overlay.
pub(super) struct Cursor;

impl Harness for Cursor {
    fn id(&self) -> HarnessTarget {
        HarnessTarget::Cursor
    }

    fn staged_root(&self, project_root: &Path, mode: &str) -> Result<PathBuf> {
        staging_cursor_pack_plugin_dir_for_mode(project_root, mode)
    }

    fn reset_paths(&self, project_root: &Path, mode: &str) -> Result<Vec<PathBuf>> {
        // The pack plugin lives under the bundle root; also wipe the fake HOME.
        Ok(vec![
            staging_cursor_bundle_dir_for_mode(project_root, mode)?,
            staging_cursor_home_dir_for_mode(project_root, mode)?,
        ])
    }

    fn raw_plugin_subdirs(&self) -> &'static [&'static str] {
        // Cursor plugins often ship `skills/<slug>/…` plus `commands` / `agents` / `rules` at the
        // repo root. Copy these subtrees verbatim first so non-`.md` assets (eval JSON, reference
        // snippets, etc.) survive; the markdown pass then overlays rendered artifacts.
        &[
            "hooks", "assets", "scripts", "commands", "agents", "rules", "skills",
        ]
    }

    fn seed_command_frontmatter(&self, m: &mut Mapping, name: &str, description: &str) {
        insert_string(m, "name", name);
        insert_string(m, "description", description);
    }

    fn command_allowed_extra_frontmatter_keys(&self) -> &'static [&'static str] {
        &[
            "agent",
            "allowed-tools",
            "context",
            "disable-model-invocation",
            "model",
            "permission",
            "subtask",
        ]
    }

    fn rendered_artifact_kind(&self, source: ArtifactKind) -> ArtifactKind {
        match source {
            // Cursor has native rule files, so rules stay rules.
            ArtifactKind::Rule => ArtifactKind::Rule,
            other => other,
        }
    }

    fn verify(&self, ctx: &StageCtx) -> Result<()> {
        let mode = ctx.mode.name();
        let bundle_root = staging_cursor_bundle_dir_for_mode(ctx.project_root, mode)?;
        let pack_plugin = staging_cursor_pack_plugin_dir_for_mode(ctx.project_root, mode)?;
        let home = staging_cursor_home_dir_for_mode(ctx.project_root, mode)?;
        require(bundle_root.is_dir(), || {
            format!("cursor staging missing {}", bundle_root.display())
        })?;
        require(
            pack_plugin.join(".cursor-plugin/plugin.json").is_file(),
            || {
                format!(
                    "cursor pack plugin missing {}",
                    pack_plugin.join(".cursor-plugin/plugin.json").display()
                )
            },
        )?;
        require(
            bundle_root
                .join(".cursor-plugin/marketplace.json")
                .is_file(),
            || {
                format!(
                    "cursor staging missing {}",
                    bundle_root.join(".cursor-plugin/marketplace.json").display()
                )
            },
        )?;
        require(home.join(".cursor").is_dir(), || {
            format!("cursor fake home missing .cursor/ under {}", home.display())
        })?;
        if ctx.launch_target == Some(HarnessTarget::Cursor) {
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
        }
        Ok(())
    }
}
