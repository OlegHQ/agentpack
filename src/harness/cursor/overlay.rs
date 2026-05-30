//! Cursor workspace overlay: manage `.cursor/agents` symlink and `cursor-overlay.manifest`.

use std::fs;
use std::io::ErrorKind;
use std::path::{Path, PathBuf};

use crate::error::{AgentpackError, Result};
use crate::fs_util::remove_path_any;
use crate::paths::{
    cursor_overlay_manifest_path, cursor_workspace_dir, staging_cursor_pack_plugin_dir_for_mode,
};

use super::fake_home::symlink_or_copy_into_fake_home;
use super::CURSOR_WORKSPACE_AGENTS_OVERLAY;

pub(super) fn read_cursor_overlay_manifest(project_root: &Path) -> Result<Vec<PathBuf>> {
    let manifest = cursor_overlay_manifest_path(project_root)?;
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

fn write_cursor_overlay_manifest(project_root: &Path, entries: &[PathBuf]) -> Result<()> {
    let manifest = cursor_overlay_manifest_path(project_root)?;
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

fn dir_has_cursor_agent_markdown(dir: &Path) -> bool {
    let Ok(rd) = fs::read_dir(dir) else {
        return false;
    };
    rd.filter_map(|e| e.ok()).any(|e| {
        e.path()
            .extension()
            .is_some_and(|x| x.eq_ignore_ascii_case("md") || x.eq_ignore_ascii_case("mdc"))
    })
}

/// Drop a tracked workspace path from a prior **`sync`**. Only removes **symlinks** or **files** —
/// never **`remove_dir_all`** on a directory.
fn remove_cursor_overlay_path_safe(path: &Path) -> Result<()> {
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
            "cursor overlay cleanup: manifest entry is a directory; leaving in place (not removing)"
        );
    }
    Ok(())
}

/// Removes workspace **`.cursor/`** paths listed in **`cursor-overlay.manifest`**. Entries are
/// absolute (the overlay follows the CWD workspace, which may differ from `project_root`).
pub(super) fn cleanup_cursor_overlay(project_root: &Path) -> Result<()> {
    for tracked in read_cursor_overlay_manifest(project_root)? {
        remove_cursor_overlay_path_safe(&tracked)?;
    }
    write_cursor_overlay_manifest(project_root, &[])?;
    Ok(())
}

/// **`<workspace>/.cursor/agents`** → **`$STAGING/cursor/agentpack-bundle/agents`** so Cursor
/// **`agent`** finds subagents under **`--workspace`**. `workspace_root` is the CWD Cursor will run
/// in (see `cursor_workspace_root`), not the pack root. Returns the absolute symlink path it created.
pub(super) fn materialize_workspace_cursor_agents_symlink(
    project_root: &Path,
    workspace_root: &Path,
    mode_name: &str,
) -> Result<Vec<PathBuf>> {
    let pack_agents =
        staging_cursor_pack_plugin_dir_for_mode(project_root, mode_name)?.join("agents");
    if !pack_agents.is_dir() || !dir_has_cursor_agent_markdown(&pack_agents) {
        return Ok(Vec::new());
    }
    let source = pack_agents
        .canonicalize()
        .map_err(|e| AgentpackError::io(&pack_agents, e))?;

    let cursor_root = cursor_workspace_dir(workspace_root);
    let agents_link = cursor_root.join(CURSOR_WORKSPACE_AGENTS_OVERLAY);

    match fs::symlink_metadata(&agents_link) {
        Ok(meta) if meta.is_dir() => {
            tracing::warn!(
                path = %agents_link.display(),
                "agentpack: ./.cursor/agents exists as a directory; not replacing with pack symlink"
            );
            return Ok(Vec::new());
        }
        Ok(meta) if meta.is_file() => {
            tracing::warn!(
                path = %agents_link.display(),
                "agentpack: ./.cursor/agents exists as a file; not replacing with pack symlink"
            );
            return Ok(Vec::new());
        }
        Ok(_) => {
            remove_cursor_overlay_path_safe(&agents_link)?;
        }
        Err(e) if e.kind() == ErrorKind::NotFound => {}
        Err(e) => return Err(AgentpackError::io(&agents_link, e)),
    }

    fs::create_dir_all(&cursor_root).map_err(|e| AgentpackError::io(&cursor_root, e))?;
    symlink_or_copy_into_fake_home(&source, &agents_link, true)?;
    Ok(vec![agents_link])
}

/// Write overlay manifest after finalization.
pub(super) fn write_overlay_manifest(project_root: &Path, entries: &[PathBuf]) -> Result<()> {
    write_cursor_overlay_manifest(project_root, entries)
}
