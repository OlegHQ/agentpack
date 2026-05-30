//! Cursor harness staging: pack tree, fake HOME, and workspace overlay.

mod approvals;
mod fake_home;
mod manifests;
mod overlay;

use std::fs;
use std::path::Path;

use crate::error::{AgentpackError, Result};
use crate::paths::{staging_cursor_bundle_dir_for_mode, staging_cursor_pack_plugin_dir_for_mode};

use super::mcp::StagedMcpEntries;
use super::seed::seed_cursor_root;

pub(crate) use manifests::write_cursor_pack_plugin_readme;
pub(crate) use overlay::read_cursor_overlay_manifest;

/// Cursor marketplace layout and optional user **`~/.cursor`** seeds only. Pack trees are merged in
/// one pass for all harnesses in **`staging::pack_overlay`**.
pub(crate) fn prepare_cursor_staging_without_pack_overlay(
    project_root: &Path,
    mode_name: &str,
) -> Result<()> {
    overlay::cleanup_cursor_overlay(project_root)?;
    let root = staging_cursor_bundle_dir_for_mode(project_root, mode_name)?;
    fs::create_dir_all(&root).map_err(|e| AgentpackError::io(&root, e))?;
    seed_cursor_root(&root)?;
    let pack_plugin = staging_cursor_pack_plugin_dir_for_mode(project_root, mode_name)?;
    fs::create_dir_all(&pack_plugin).map_err(|e| AgentpackError::io(&pack_plugin, e))?;
    manifests::write_cursor_pack_plugin_manifests(&root)?;
    Ok(())
}

/// Workspace-agnostic Cursor finalization: builds the staged fake-`HOME` and seeds the user's
/// real `~/.cursor/projects/<slug>/mcp-approvals.json` so MCP servers are pre-approved. Safe to
/// run for every harness target — does **not** touch the project's `./.cursor/` tree.
pub(crate) fn finalize_cursor_staging_common(
    project_root: &Path,
    mode_name: &str,
    merged_mcp: &StagedMcpEntries,
) -> Result<()> {
    fake_home::materialize_cursor_fake_home(project_root, mode_name)?;
    approvals::seed_workspace_mcp_approvals(project_root, merged_mcp)?;
    Ok(())
}

/// Materialize the project-side `./.cursor/agents` symlink required by `cursor agent`'s
/// workspace-scoped subagent discovery. Caller is responsible for only invoking this when the
/// Cursor harness is being launched.
pub(crate) fn finalize_cursor_workspace_overlay(
    project_root: &Path,
    mode_name: &str,
) -> Result<()> {
    let cursor_overlay =
        overlay::materialize_workspace_cursor_agents_symlink(project_root, mode_name)?;
    overlay::write_overlay_manifest(project_root, &cursor_overlay)?;
    Ok(())
}
