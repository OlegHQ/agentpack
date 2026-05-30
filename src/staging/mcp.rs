//! MCP merge pipeline: collect MCP server configs from plugins, manifest, and
//! `.agents/mcp.json`, merge them, then fan out to each harness using its native format.
//!
//! Per-harness formats are **not** identical. A single JSON blob will not do. This module owns the
//! merge and the per-harness render targets:
//!
//! | Harness  | Target file                                 | Shape                                                       |
//! |----------|---------------------------------------------|-------------------------------------------------------------|
//! | Claude   | `<bundle>/.mcp.json` (leading dot)          | `{"mcpServers": { name: { command, args, env, url, ... }}}` |
//! | Cursor   | `<cursor_pack>/mcp.json`                    | `{"mcpServers": { name: { command, args, env, url, ... }}}` |
//! | OpenCode | `<opencode>/opencode.json` `mcp` field      | `{"mcp": { name: { type, command: [..], environment, url }}}` |
//! | Codex    | `<codex_home>/config.toml` `[mcp_servers]`  | TOML tables `[mcp_servers.<name>]`                          |
//! | Grok     | `<grok_home>/config.toml` `[mcp_servers]`   | TOML tables `[mcp_servers.<name>]`                          |
//! | Agy      | `<agy_bundle>/mcp_config.json`              | `{"mcpServers": { name: { command, args, env, serverUrl }}}` |
//!
//! For OpenCode and Codex the render is additive: pack entries never clobber user-seeded entries
//! under the same server name (user wins on conflict). Cursor's fake HOME merge with user
//! `~/.cursor/mcp.json` lives in [`super::cursor::fake_home`] and uses the same rule.

use std::collections::BTreeMap;
use std::path::Path;

use serde::{Deserialize, Serialize};

use crate::cache::cache_entry_dir;
use crate::error::{AgentpackError, Result};
use crate::lockfile::PackLock;
use crate::manifest::AgentpackManifest;
use crate::mode::filter::EffectiveMode;
use crate::paths::project_dot_agents_dir;

use super::pack_overlay::disabled_in_config;

/// A single MCP server entry. Supports both stdio (`command` + `args`) and remote (`url`).
///
/// `type` is preserved from source `mcp.json` when present (`"stdio"`, `"http"`, `"sse"`, …).
/// Claude requires the discriminator on remote entries; OpenCode/Codex/Cursor accept the field
/// but don't require it. The Claude renderer fills in `type: "http"` for url-only entries that
/// lack one.
#[derive(Debug, Clone, Default, Serialize, Deserialize)]
pub struct McpServerEntry {
    #[serde(rename = "type", default, skip_serializing_if = "Option::is_none")]
    pub kind: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub command: Option<String>,
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub args: Vec<String>,
    #[serde(default, skip_serializing_if = "BTreeMap::is_empty")]
    pub env: BTreeMap<String, String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub url: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub disabled: Option<bool>,
}

impl McpServerEntry {
    pub(crate) fn is_remote(&self) -> bool {
        self.url.is_some() && self.command.is_none()
    }
}

/// Top-level JSON: `{ "mcpServers": { ... } }` — used by Claude and Cursor.
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
    mode: Option<&EffectiveMode>,
    merged: &mut BTreeMap<String, (McpServerEntry, McpSource)>,
) {
    match load_mcp_json(path) {
        Ok(cfg) => {
            for (name, entry) in cfg.mcp_servers {
                if mode.is_some_and(|mode| !mode.allows_mcp(&name)) {
                    continue;
                }
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
    mode: Option<&EffectiveMode>,
) -> Result<BTreeMap<String, (McpServerEntry, McpSource)>> {
    let mut merged: BTreeMap<String, (McpServerEntry, McpSource)> = BTreeMap::new();

    // 1. Plugin mcp.json files (sorted by cache_key)
    let mut plug_list: Vec<_> = lock.plugins().collect();
    plug_list.sort_by(|a, b| a.cache_key.cmp(&b.cache_key));
    for plugin in plug_list {
        if plugin.cache_key.is_empty() || disabled_in_config(lock, plugin) {
            continue;
        }
        if mode.is_some_and(|mode| {
            !mode
                .allows_package_path(&plugin.module, std::path::Path::new("mcp.json"))
                .unwrap_or(false)
        }) {
            continue;
        }
        let Ok(cache_path) = cache_entry_dir(&plugin.cache_key) else {
            continue;
        };
        let mcp_file = cache_path.join("mcp.json");
        if mcp_file.is_file() {
            merge_mcp_file(&mcp_file, McpSource::Plugin, mode, &mut merged);
        }
    }

    // 2. Manifest [mcp.servers]
    if let Some(m) = manifest {
        for (name, entry) in &m.mcp.servers {
            if mode.is_some_and(|mode| !mode.allows_mcp(name)) {
                continue;
            }
            merged.insert(name.clone(), (entry.clone(), McpSource::Manifest));
        }
    }

    // 3. .agents/mcp.json
    let dot_mcp = project_dot_agents_dir(project_root).join("mcp.json");
    if dot_mcp.is_file()
        && !mode.is_some_and(|mode| {
            !mode
                .allows_dot_agents_path(std::path::Path::new("mcp.json"))
                .unwrap_or(false)
        })
    {
        merge_mcp_file(&dot_mcp, McpSource::DotAgents, mode, &mut merged);
    }

    Ok(merged)
}

type MergedEntries = BTreeMap<String, (McpServerEntry, McpSource)>;

/// The resolved, merged MCP server set handed to each harness's `write_mcp`.
pub(crate) type StagedMcpEntries = MergedEntries;

/// Strip the provenance tag, leaving just the merged server entries — the shape every harness's
/// native MCP writer serializes.
pub(crate) fn bare_entries(merged: &MergedEntries) -> BTreeMap<String, McpServerEntry> {
    merged
        .iter()
        .map(|(k, (v, _))| (k.clone(), v.clone()))
        .collect()
}

/// Shared entry builders for the per-harness MCP writer tests (which live in the harness modules).
#[cfg(test)]
pub(crate) mod test_support {
    use super::{McpServerEntry, McpSource, MergedEntries};
    use std::collections::BTreeMap;

    pub(crate) fn stdio_entry() -> McpServerEntry {
        McpServerEntry {
            kind: None,
            command: Some("cargo".into()),
            args: vec!["run".into(), "--".into(), "serve".into()],
            env: BTreeMap::from([("RUST_LOG".to_string(), "info".to_string())]),
            url: None,
            disabled: None,
        }
    }

    pub(crate) fn remote_entry() -> McpServerEntry {
        McpServerEntry {
            kind: None,
            command: None,
            args: Vec::new(),
            env: BTreeMap::new(),
            url: Some("https://mcp.example.com/mcp".into()),
            disabled: None,
        }
    }

    pub(crate) fn merged(pairs: &[(&str, McpServerEntry)]) -> MergedEntries {
        pairs
            .iter()
            .map(|(n, e)| ((*n).to_string(), (e.clone(), McpSource::Plugin)))
            .collect()
    }
}
