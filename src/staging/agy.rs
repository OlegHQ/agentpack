//! Antigravity (`agy`) harness staging: plugin bundle plus workspace overlay.

mod overlay;

use std::fs;
use std::path::{Path, PathBuf};

use crate::error::{AgentpackError, Result};
use crate::paths::staging_agy_bundle_dir_for_mode;

pub(super) use overlay::read_agy_overlay_manifest;

pub(crate) fn prepare_agy_staging_without_pack_overlay(
    project_root: &Path,
    mode_name: &str,
) -> Result<()> {
    overlay::cleanup_agy_overlay(project_root)?;
    let bundle = staging_agy_bundle_dir_for_mode(project_root, mode_name)?;
    fs::create_dir_all(&bundle).map_err(|e| AgentpackError::io(&bundle, e))?;
    write_agy_plugin_manifest(&bundle)?;
    Ok(())
}

pub(crate) fn finalize_agy_staging(project_root: &Path, mode_name: &str) -> Result<()> {
    let entries = overlay::materialize_workspace_agy_plugin_symlink(project_root, mode_name)?;
    overlay::write_overlay_manifest(project_root, &entries)
}

pub(super) fn write_agy_plugin_manifest(bundle: &Path) -> Result<()> {
    let manifest = serde_json::json!({
        "name": "agentpack-bundle"
    });
    crate::fs_util::write_json_value(&bundle.join("plugin.json"), &manifest)
}

pub(crate) fn agy_workspace_overlay_paths(project_root: &Path) -> Result<Vec<PathBuf>> {
    let root = project_root.to_path_buf();
    Ok(read_agy_overlay_manifest(project_root)?
        .into_iter()
        .map(|rel| root.join(rel))
        .collect())
}
