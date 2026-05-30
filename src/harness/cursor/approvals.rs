//! Pre-seed Cursor's `mcp-approvals.json` for the current workspace so MCP servers staged by
//! agentpack don't silently land in `not loaded (needs approval)` state.
//!
//! Cursor stores per-workspace MCP approvals at
//! `<CURSOR_DATA_DIR or ~/.cursor>/projects/<slugified_workspace>/mcp-approvals.json`
//! as a JSON array of `<server-name>-<sha256({path: workspace, server: <entry>})[:16]>` IDs.
//!
//! We don't redirect `CURSOR_DATA_DIR` (the launcher keeps workspace-trust state on the real
//! profile), so this file lives under the user's real `~/.cursor`. We **only add** missing
//! entries — never remove — so user-approved servers from interactive `agent mcp enable`
//! sessions stick around.
//!
//! Algorithm reproduced from `cursor-agent` v2026.03.30-a5d3e17 (`mcp-agent-exec/dist/index.js`):
//!
//! ```js
//! function l(name, server, cwd) {
//!   const s = { path: cwd, server };
//!   return `${name}-${sha256(JSON.stringify(s)).slice(0, 16)}`;
//! }
//! ```
//!
//! The `path` field equals the `--workspace` argument we pass when launching `agent`, which is
//! the canonical project root.

use std::env;
use std::fs;
use std::path::{Path, PathBuf};

use serde_json::Value;
use sha2::{Digest, Sha256};

use crate::error::{AgentpackError, Result};
use crate::fs_util::{read_json_value_opt, write_json_value};
use crate::staging::mcp::{McpServerEntry, StagedMcpEntries};

/// Build approval IDs for every staged server and merge them into the workspace approvals
/// file. Idempotent: existing entries are preserved.
pub(super) fn seed_workspace_mcp_approvals(
    project_root: &Path,
    merged: &StagedMcpEntries,
) -> Result<()> {
    if merged.is_empty() {
        return Ok(());
    }
    let Some(approvals_path) = workspace_approvals_path(project_root) else {
        tracing::debug!("skip cursor mcp-approvals: no HOME / cursor data dir resolvable");
        return Ok(());
    };

    let workspace_str = canonical_workspace_string(project_root);
    let mut to_add: Vec<String> = Vec::with_capacity(merged.len());
    for (name, (entry, _)) in merged {
        match approval_id(name, entry, &workspace_str) {
            Ok(id) => to_add.push(id),
            Err(e) => {
                tracing::warn!(server = %name, error = %e, "skip cursor approval seed for server");
            }
        }
    }
    if to_add.is_empty() {
        return Ok(());
    }

    let existing =
        read_json_value_opt(&approvals_path)?.unwrap_or_else(|| Value::Array(Vec::new()));
    let mut arr = match existing {
        Value::Array(a) => a,
        _ => Vec::new(),
    };
    let already: std::collections::HashSet<String> = arr
        .iter()
        .filter_map(|v| v.as_str().map(str::to_owned))
        .collect();
    let mut added = 0usize;
    for id in to_add {
        if !already.contains(&id) {
            arr.push(Value::String(id));
            added += 1;
        }
    }
    if added == 0 {
        return Ok(());
    }
    if let Some(parent) = approvals_path.parent() {
        fs::create_dir_all(parent).map_err(|e| AgentpackError::io(parent, e))?;
    }
    write_json_value(&approvals_path, &Value::Array(arr))?;
    tracing::debug!(
        path = %approvals_path.display(),
        added,
        "seeded cursor mcp-approvals.json for staged servers"
    );
    Ok(())
}

fn cursor_data_dir() -> Option<PathBuf> {
    if let Some(v) = env::var_os("CURSOR_DATA_DIR") {
        if !v.is_empty() {
            return Some(PathBuf::from(v));
        }
    }
    dirs::home_dir().map(|h| h.join(".cursor"))
}

fn workspace_approvals_path(project_root: &Path) -> Option<PathBuf> {
    let data_dir = cursor_data_dir()?;
    let slug = slugify_path(&canonical_workspace_string(project_root));
    Some(
        data_dir
            .join("projects")
            .join(slug)
            .join("mcp-approvals.json"),
    )
}

fn canonical_workspace_string(project_root: &Path) -> String {
    project_root
        .canonicalize()
        .unwrap_or_else(|_| project_root.to_path_buf())
        .to_string_lossy()
        .into_owned()
}

/// Match `cursor-agent` `slugifyPath`: replace non-alphanumeric with `-`, collapse runs of
/// `-`, then trim leading/trailing `-`.
fn slugify_path(s: &str) -> String {
    let replaced: String = s
        .chars()
        .map(|c| if c.is_ascii_alphanumeric() { c } else { '-' })
        .collect();
    let mut collapsed = String::with_capacity(replaced.len());
    let mut prev_dash = false;
    for c in replaced.chars() {
        if c == '-' {
            if !prev_dash {
                collapsed.push(c);
            }
            prev_dash = true;
        } else {
            collapsed.push(c);
            prev_dash = false;
        }
    }
    collapsed.trim_matches('-').to_owned()
}

fn approval_id(name: &str, entry: &McpServerEntry, workspace: &str) -> Result<String> {
    let server_json = serde_json::to_string(entry)
        .map_err(|e| AgentpackError::Staging(format!("serialize mcp entry {name}: {e}")))?;
    let path_json = serde_json::to_string(workspace)
        .map_err(|e| AgentpackError::Staging(format!("serialize workspace path: {e}")))?;
    // Build the JSON exactly as `JSON.stringify({path, server})` does: keys in object-literal
    // insertion order (`path`, then `server`), no whitespace.
    let payload = format!("{{\"path\":{path_json},\"server\":{server_json}}}");
    let mut h = Sha256::new();
    h.update(payload.as_bytes());
    let hex = hex::encode(h.finalize());
    Ok(format!("{name}-{}", &hex[..16]))
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::collections::BTreeMap;

    fn remote(url: &str) -> McpServerEntry {
        McpServerEntry {
            kind: None,
            command: None,
            args: Vec::new(),
            env: BTreeMap::new(),
            url: Some(url.into()),
            disabled: None,
        }
    }

    #[test]
    fn slugify_matches_cursor_rules() {
        assert_eq!(
            slugify_path("/Users/snowbear/WORK/GIT/agentpack"),
            "Users-snowbear-WORK-GIT-agentpack"
        );
        assert_eq!(slugify_path("/a//b/c"), "a-b-c");
        assert_eq!(slugify_path("---x---"), "x");
    }

    #[test]
    fn approval_id_matches_cursor_agent() {
        // Generated independently with `agent mcp enable linear` in the same workspace
        // (cursor-agent v2026.03.30-a5d3e17).
        let id = approval_id(
            "linear",
            &remote("https://mcp.linear.app/mcp"),
            "/Users/snowbear/WORK/GIT/agentpack",
        )
        .unwrap();
        assert_eq!(id, "linear-91c8fb12af7abc53");
    }
}
