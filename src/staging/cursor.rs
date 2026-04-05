//! Cursor harness staging: pack tree, fake HOME, and workspace overlay.

mod fake_home;
mod manifests;
mod overlay;

use std::fs;
use std::path::Path;

use crate::artifacts::HarnessTarget;
use crate::error::{AgentpackError, Result};
use crate::lockfile::PackLock;
use crate::manifest::AgentpackManifest;
use crate::paths::{staging_cursor_bundle_dir, staging_cursor_pack_plugin_dir};

use super::pack_overlay::{stage_pack_plugins_for_target, stage_pack_skills_for_target};
use super::seed::seed_cursor_root;

pub(super) use overlay::read_cursor_overlay_manifest;

/// Cursor pack tree and marketplace layout only. **`rebuild_staging`** calls
/// **`stage_dot_agents_overlay`** next, then **`finalize_cursor_staging`** (fake **`HOME`** +
/// workspace **`agents`** symlink), so dot-agents content is included before symlinks are written.
pub(super) fn rebuild_cursor_staging_without_finalize(
    project_root: &Path,
    lock: &PackLock,
    manifest: Option<&AgentpackManifest>,
) -> Result<()> {
    overlay::cleanup_cursor_overlay(project_root)?;
    let root = staging_cursor_bundle_dir(project_root)?;
    fs::create_dir_all(&root).map_err(|e| AgentpackError::io(&root, e))?;
    seed_cursor_root(&root)?;
    let pack_plugin = staging_cursor_pack_plugin_dir(project_root)?;
    fs::create_dir_all(&pack_plugin).map_err(|e| AgentpackError::io(&pack_plugin, e))?;
    manifests::write_cursor_pack_plugin_manifests(&root)?;
    stage_pack_plugins_for_target(lock, &pack_plugin, HarnessTarget::Cursor, manifest)?;
    stage_pack_skills_for_target(lock, &pack_plugin, HarnessTarget::Cursor, manifest)?;
    manifests::write_cursor_pack_plugin_readme(&pack_plugin)?;
    Ok(())
}

/// Runs after pack **and** dot-agents overlay so staged **`agents/`** reflects merged content.
pub(super) fn finalize_cursor_staging(project_root: &Path) -> Result<()> {
    fake_home::materialize_cursor_fake_home(project_root)?;
    let cursor_overlay = overlay::materialize_workspace_cursor_agents_symlink(project_root)?;
    overlay::write_overlay_manifest(project_root, &cursor_overlay)?;
    Ok(())
}
