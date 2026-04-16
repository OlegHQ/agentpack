//! MCP merge pipeline: collect MCP server configs from plugins, manifest, and
//! `.agents/mcp.json`, merge them, and write per-harness `mcp.json`.

use std::collections::BTreeMap;
use std::path::Path;

use serde::{Deserialize, Serialize};

use crate::cache::cache_entry_dir;
use crate::error::{AgentpackError, Result};
use crate::lockfile::PackLock;
use crate::manifest::AgentpackManifest;
use crate::paths::project_dot_agents_dir;

use super::pack_overlay::{disabled_in_config, PackHarnessRoots};

/// Single MCP server entry — used for both TOML manifest and JSON `mcp.json`.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct McpServerEntry {
    pub command: String,
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub args: Vec<String>,
    #[serde(default, skip_serializing_if = "BTreeMap::is_empty")]
    pub env: BTreeMap<String, String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub disabled: Option<bool>,
}

/// Top-level JSON: `{ "mcpServers": { ... } }`
#[derive(Debug, Clone, Default, Serialize, Deserialize)]
pub struct McpConfig {
    #[serde(rename = "mcpServers", default)]
    pub mcp_servers: BTreeMap<String, McpServerEntry>,
}

/// Parse an `mcp.json` (or JSONC) file into [`McpConfig`].
pub(crate) fn load_mcp_json(path: &Path) -> Result<McpConfig> {
    let raw = std::fs::read_to_string(path).map_err(|e| AgentpackError::io(path, e))?;
    crate::fs_util::parse_jsonc(&raw)
        .map_err(|e| AgentpackError::Staging(format!("{}: {e}", path.display())))
}

fn merge_mcp_file(
    path: &Path,
    source: McpSource,
    merged: &mut BTreeMap<String, (McpServerEntry, McpSource)>,
) {
    match load_mcp_json(path) {
        Ok(cfg) => {
            for (name, entry) in cfg.mcp_servers {
                merged.insert(name, (entry, source));
            }
        }
        Err(e) => tracing::warn!(path = %path.display(), error = %e, "skipping mcp.json"),
    }
}

/// Provenance tag for `mcp list` display.
#[derive(Debug, Clone, Copy)]
pub(crate) enum McpSource {
    Plugin,
    Manifest,
    DotAgents,
}

impl std::fmt::Display for McpSource {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.write_str(match self {
            McpSource::Plugin => "plugin",
            McpSource::Manifest => "manifest",
            McpSource::DotAgents => ".agents",
        })
    }
}

/// Collect MCP configs from all sources and merge (later wins on same server name).
/// Merge order: plugin mcp.json files → manifest [mcp.servers] → .agents/mcp.json
pub(crate) fn collect_merged_mcp(
    project_root: &Path,
    lock: &PackLock,
    manifest: Option<&AgentpackManifest>,
) -> Result<BTreeMap<String, (McpServerEntry, McpSource)>> {
    let mut merged: BTreeMap<String, (McpServerEntry, McpSource)> = BTreeMap::new();

    // 1. Plugin mcp.json files (sorted by cache_key)
    let mut plug_list: Vec<_> = lock.plugins().collect();
    plug_list.sort_by(|a, b| a.cache_key.cmp(&b.cache_key));
    for plugin in plug_list {
        if plugin.cache_key.is_empty() || disabled_in_config(lock, plugin) {
            continue;
        }
        let disabled = manifest
            .map(|m| m.disable_paths_for_module(&plugin.module))
            .unwrap_or(&[]);
        if crate::staging::rel_is_disabled(std::path::Path::new("mcp.json"), disabled) {
            continue;
        }
        let Ok(cache_path) = cache_entry_dir(&plugin.cache_key) else {
            continue;
        };
        let mcp_file = cache_path.join("mcp.json");
        if mcp_file.is_file() {
            merge_mcp_file(&mcp_file, McpSource::Plugin, &mut merged);
        }
    }

    // 2. Manifest [mcp.servers]
    if let Some(m) = manifest {
        for (name, entry) in &m.mcp.servers {
            merged.insert(name.clone(), (entry.clone(), McpSource::Manifest));
        }
    }

    // 3. .agents/mcp.json
    let dot_mcp = project_dot_agents_dir(project_root).join("mcp.json");
    if dot_mcp.is_file() {
        merge_mcp_file(&dot_mcp, McpSource::DotAgents, &mut merged);
    }

    Ok(merged)
}

/// Collect, merge, and write `mcp.json` to every harness staging root.
pub(super) fn stage_merged_mcp(
    project_root: &Path,
    lock: &PackLock,
    manifest: Option<&AgentpackManifest>,
    dests: &PackHarnessRoots<'_>,
) -> Result<()> {
    let merged = collect_merged_mcp(project_root, lock, manifest)?;
    if merged.is_empty() {
        return Ok(());
    }
    let config = McpConfig {
        mcp_servers: merged.into_iter().map(|(k, (v, _))| (k, v)).collect(),
    };
    let json = serde_json::to_string_pretty(&config)
        .map_err(|e| AgentpackError::Staging(format!("mcp.json serialization: {e}")))?;
    for dest in [dests.claude_bundle, dests.opencode, dests.codex, dests.cursor_pack] {
        crate::fs_util::write_text_file(&dest.join("mcp.json"), &json)?;
    }
    Ok(())
}
