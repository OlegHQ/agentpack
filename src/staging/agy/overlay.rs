//! Antigravity workspace overlay: manage `.agents/plugins/agentpack-bundle` symlink.

use std::fs;
use std::io::ErrorKind;
use std::path::{Path, PathBuf};

use crate::error::{AgentpackError, Result};
use crate::fs_util::remove_path_any;
use crate::paths::{agy_overlay_manifest_path, staging_agy_bundle_dir_for_mode};

use super::super::constants::AGY_WORKSPACE_PLUGIN_OVERLAY;
#[cfg(not(unix))]
use super::super::tree::copy_merge_tree;

pub(in crate::staging) fn read_agy_overlay_manifest(project_root: &Path) -> Result<Vec<PathBuf>> {
    let manifest = agy_overlay_manifest_path(project_root)?;
    match fs::read_to_string(&manifest) {
        Ok(contents) => Ok(contents
            .lines()
            .map(str::trim)
            .filter(|line| !line.is_empty())
            .map(PathBuf::from)
            .collect()),
        Err(err) if err.kind() == ErrorKind::NotFound => Ok(Vec::new()),
        Err(err) => Err(AgentpackError::io(&manifest, err)),
    }
}

fn write_agy_overlay_manifest(project_root: &Path, entries: &[PathBuf]) -> Result<()> {
    let manifest = agy_overlay_manifest_path(project_root)?;
    if entries.is_empty() {
        remove_path_any(&manifest)?;
        return Ok(());
    }
    if let Some(parent) = manifest.parent() {
        fs::create_dir_all(parent).map_err(|err| AgentpackError::io(parent, err))?;
    }
    let mut normalized: Vec<String> = entries
        .iter()
        .map(|entry| entry.to_string_lossy().into_owned())
        .collect();
    normalized.sort();
    normalized.dedup();
    fs::write(&manifest, format!("{}\n", normalized.join("\n")))
        .map_err(|err| AgentpackError::io(&manifest, err))?;
    Ok(())
}

fn remove_agy_overlay_path_safe(path: &Path) -> Result<()> {
    let meta = match fs::symlink_metadata(path) {
        Ok(m) => m,
        Err(e) if e.kind() == ErrorKind::NotFound => return Ok(()),
        Err(e) => return Err(AgentpackError::io(path, e)),
    };
    if meta.file_type().is_symlink() || meta.is_file() {
        fs::remove_file(path).map_err(|e| AgentpackError::io(path, e))?;
    } else if meta.is_dir() {
        tracing::warn!(
            path = %path.display(),
            "agy overlay cleanup: manifest entry is a directory; leaving in place"
        );
    }
    Ok(())
}

pub(in crate::staging) fn cleanup_agy_overlay(project_root: &Path) -> Result<()> {
    for rel in read_agy_overlay_manifest(project_root)? {
        remove_agy_overlay_path_safe(&project_root.join(rel))?;
    }
    write_agy_overlay_manifest(project_root, &[])?;
    Ok(())
}

fn symlink_or_copy_dir(src: &Path, dst: &Path) -> Result<()> {
    if fs::symlink_metadata(dst).is_ok() {
        remove_path_any(dst)?;
    }
    if let Some(parent) = dst.parent() {
        fs::create_dir_all(parent).map_err(|e| AgentpackError::io(parent, e))?;
    }
    let target = src.canonicalize().map_err(|e| AgentpackError::io(src, e))?;
    #[cfg(unix)]
    {
        std::os::unix::fs::symlink(&target, dst).map_err(|e| AgentpackError::io(dst, e))?;
        Ok(())
    }
    #[cfg(windows)]
    {
        std::os::windows::fs::symlink_dir(&target, dst).or_else(|_| copy_merge_tree(src, dst))
    }
    #[cfg(not(any(unix, windows)))]
    {
        copy_merge_tree(src, dst)
    }
}

pub(in crate::staging) fn materialize_workspace_agy_plugin_symlink(
    project_root: &Path,
    mode_name: &str,
) -> Result<Vec<PathBuf>> {
    let bundle = staging_agy_bundle_dir_for_mode(project_root, mode_name)?;
    if !bundle.join("plugin.json").is_file() {
        return Ok(Vec::new());
    }
    let overlay_rel = PathBuf::from(AGY_WORKSPACE_PLUGIN_OVERLAY);
    let overlay_path = project_root.join(&overlay_rel);

    match fs::symlink_metadata(&overlay_path) {
        Ok(meta) if meta.is_dir() && !meta.file_type().is_symlink() => {
            tracing::warn!(
                path = %overlay_path.display(),
                "agentpack: .agents/plugins/agentpack-bundle exists as a directory; not replacing with pack symlink"
            );
            return Ok(Vec::new());
        }
        Ok(meta) if meta.is_file() => {
            tracing::warn!(
                path = %overlay_path.display(),
                "agentpack: .agents/plugins/agentpack-bundle exists as a file; not replacing with pack symlink"
            );
            return Ok(Vec::new());
        }
        Ok(_) => {
            remove_agy_overlay_path_safe(&overlay_path)?;
        }
        Err(e) if e.kind() == ErrorKind::NotFound => {}
        Err(e) => return Err(AgentpackError::io(&overlay_path, e)),
    }

    symlink_or_copy_dir(&bundle, &overlay_path)?;
    Ok(vec![overlay_rel])
}

pub(in crate::staging) fn write_overlay_manifest(
    project_root: &Path,
    entries: &[PathBuf],
) -> Result<()> {
    write_agy_overlay_manifest(project_root, entries)
}
