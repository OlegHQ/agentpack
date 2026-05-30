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
use std::fs;
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
    fn is_remote(&self) -> bool {
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

pub(crate) fn bare_entries(merged: &MergedEntries) -> BTreeMap<String, McpServerEntry> {
    merged
        .iter()
        .map(|(k, (v, _))| (k.clone(), v.clone()))
        .collect()
}

/// Write `{"mcpServers":{...}}` JSON (Cursor `mcp.json`). Cursor accepts entries without a
/// `type` discriminator, so we serialize as-is.
pub(crate) fn write_mcp_servers_json(dest: &Path, merged: &MergedEntries) -> Result<()> {
    let cfg = McpConfig {
        mcp_servers: bare_entries(merged),
    };
    let json = serde_json::to_string_pretty(&cfg)
        .map_err(|e| AgentpackError::Staging(format!("{}: {e}", dest.display())))?;
    crate::fs_util::write_text_file(dest, &json)
}

/// Write Claude's plugin `.mcp.json`. Claude rejects remote entries without a `type`
/// discriminator — its zod schema is a `discriminatedUnion("type", ...)` over
/// `stdio`/`sse`/`http`/`sse-ide`/`ws-ide`/`sdk`. We default to `"http"` (Streamable HTTP, the
/// modern remote transport) for url-only entries that don't already specify one.
pub(crate) fn write_claude_mcp_servers_json(dest: &Path, merged: &MergedEntries) -> Result<()> {
    let mut entries = bare_entries(merged);
    for entry in entries.values_mut() {
        if entry.kind.is_none() && entry.is_remote() {
            entry.kind = Some("http".into());
        }
    }
    let cfg = McpConfig {
        mcp_servers: entries,
    };
    let json = serde_json::to_string_pretty(&cfg)
        .map_err(|e| AgentpackError::Staging(format!("{}: {e}", dest.display())))?;
    crate::fs_util::write_text_file(dest, &json)
}

fn opencode_entry_value(entry: &McpServerEntry) -> serde_json::Value {
    use serde_json::{json, Value};
    let mut obj = serde_json::Map::new();
    if entry.is_remote() {
        obj.insert("type".into(), json!("remote"));
        if let Some(url) = &entry.url {
            obj.insert("url".into(), json!(url));
        }
    } else {
        obj.insert("type".into(), json!("local"));
        let mut cmd: Vec<Value> = Vec::with_capacity(1 + entry.args.len());
        if let Some(c) = &entry.command {
            cmd.push(json!(c));
        }
        cmd.extend(entry.args.iter().map(|a| json!(a)));
        obj.insert("command".into(), Value::Array(cmd));
        if !entry.env.is_empty() {
            let env_obj: serde_json::Map<String, Value> = entry
                .env
                .iter()
                .map(|(k, v)| (k.clone(), json!(v)))
                .collect();
            obj.insert("environment".into(), Value::Object(env_obj));
        }
    }
    if entry.disabled != Some(true) {
        obj.insert("enabled".into(), json!(true));
    } else {
        obj.insert("enabled".into(), json!(false));
    }
    Value::Object(obj)
}

/// Merge MCP entries into `opencode.json` under the top-level `mcp` object.
/// User-seeded entries win: we only insert pack entries whose names are absent.
pub(crate) fn merge_into_opencode_config(config_path: &Path, merged: &MergedEntries) -> Result<()> {
    use serde_json::Value;

    let mut root: Value = if config_path.is_file() {
        let raw =
            fs::read_to_string(config_path).map_err(|e| AgentpackError::io(config_path, e))?;
        crate::fs_util::parse_jsonc(&raw)
            .map_err(|e| AgentpackError::Staging(format!("{}: {e}", config_path.display())))?
    } else {
        serde_json::json!({ "$schema": "https://opencode.ai/config.json" })
    };

    let obj = root.as_object_mut().ok_or_else(|| {
        AgentpackError::Staging(format!(
            "{}: top-level must be a JSON object",
            config_path.display()
        ))
    })?;
    let mcp = obj
        .entry("mcp".to_string())
        .or_insert_with(|| Value::Object(serde_json::Map::new()));
    let mcp_obj = mcp.as_object_mut().ok_or_else(|| {
        AgentpackError::Staging(format!(
            "{}: `mcp` must be a JSON object",
            config_path.display()
        ))
    })?;
    for (name, (entry, _)) in merged {
        if mcp_obj.contains_key(name) {
            continue;
        }
        mcp_obj.insert(name.clone(), opencode_entry_value(entry));
    }

    let out = serde_json::to_string_pretty(&root)
        .map_err(|e| AgentpackError::Staging(format!("{}: {e}", config_path.display())))?;
    crate::fs_util::write_text_file(config_path, &out)
}

pub(crate) fn toml_mcp_entry_table(entry: &McpServerEntry) -> toml::value::Table {
    let mut t = toml::value::Table::new();
    if entry.is_remote() {
        if let Some(url) = &entry.url {
            t.insert("url".into(), toml::Value::String(url.clone()));
        }
    } else {
        if let Some(c) = &entry.command {
            t.insert("command".into(), toml::Value::String(c.clone()));
        }
        if !entry.args.is_empty() {
            let arr: Vec<toml::Value> = entry
                .args
                .iter()
                .map(|a| toml::Value::String(a.clone()))
                .collect();
            t.insert("args".into(), toml::Value::Array(arr));
        }
        if !entry.env.is_empty() {
            let mut env_tbl = toml::value::Table::new();
            for (k, v) in &entry.env {
                env_tbl.insert(k.clone(), toml::Value::String(v.clone()));
            }
            t.insert("env".into(), toml::Value::Table(env_tbl));
        }
    }
    if entry.disabled == Some(true) {
        t.insert("enabled".into(), toml::Value::Boolean(false));
    }
    t
}

/// Merge MCP entries into a TOML `config.toml` under `[mcp_servers.<name>]` tables. Shared by
/// Codex and Grok, which use the identical native format. User-seeded entries win: we only insert
/// pack entries whose names are absent.
pub(crate) fn merge_into_toml_mcp_config(config_path: &Path, merged: &MergedEntries) -> Result<()> {
    let mut doc = crate::fs_util::read_toml_value_or_default(config_path)?;

    let root = doc.as_table_mut().ok_or_else(|| {
        AgentpackError::Staging(format!(
            "{}: top-level must be a TOML table",
            config_path.display()
        ))
    })?;
    let servers = root
        .entry("mcp_servers".to_string())
        .or_insert_with(|| toml::Value::Table(toml::value::Table::new()))
        .as_table_mut()
        .ok_or_else(|| {
            AgentpackError::Staging(format!(
                "{}: `mcp_servers` must be a table",
                config_path.display()
            ))
        })?;
    for (name, (entry, _)) in merged {
        if servers.contains_key(name) {
            continue;
        }
        servers.insert(
            name.clone(),
            toml::Value::Table(toml_mcp_entry_table(entry)),
        );
    }

    let out = toml::to_string(&doc)
        .map_err(|e| AgentpackError::Staging(format!("{}: {e}", config_path.display())))?;
    crate::fs_util::write_text_file(config_path, &out)
}

fn agy_entry_value(entry: &McpServerEntry) -> serde_json::Value {
    use serde_json::{json, Value};
    let mut obj = serde_json::Map::new();
    if entry.is_remote() {
        if let Some(url) = &entry.url {
            obj.insert("serverUrl".into(), json!(url));
        }
    } else {
        if let Some(command) = &entry.command {
            obj.insert("command".into(), json!(command));
        }
        if !entry.args.is_empty() {
            obj.insert("args".into(), json!(entry.args));
        }
        if !entry.env.is_empty() {
            let env_obj: serde_json::Map<String, Value> = entry
                .env
                .iter()
                .map(|(k, v)| (k.clone(), json!(v)))
                .collect();
            obj.insert("env".into(), Value::Object(env_obj));
        }
    }
    if let Some(disabled) = entry.disabled {
        obj.insert("disabled".into(), json!(disabled));
    }
    Value::Object(obj)
}

pub(crate) fn write_agy_mcp_config_json(dest: &Path, merged: &MergedEntries) -> Result<()> {
    use serde_json::Value;
    let entries: serde_json::Map<String, Value> = merged
        .iter()
        .map(|(name, (entry, _))| (name.clone(), agy_entry_value(entry)))
        .collect();
    let cfg = serde_json::json!({ "mcpServers": entries });
    let json = serde_json::to_string_pretty(&cfg)
        .map_err(|e| AgentpackError::Staging(format!("{}: {e}", dest.display())))?;
    crate::fs_util::write_text_file(dest, &json)
}

#[cfg(test)]
mod tests {
    use super::*;
    use tempfile::tempdir;

    fn stdio_entry() -> McpServerEntry {
        McpServerEntry {
            kind: None,
            command: Some("cargo".into()),
            args: vec!["run".into(), "--".into(), "serve".into()],
            env: BTreeMap::from([("RUST_LOG".to_string(), "info".to_string())]),
            url: None,
            disabled: None,
        }
    }

    fn remote_entry() -> McpServerEntry {
        McpServerEntry {
            kind: None,
            command: None,
            args: Vec::new(),
            env: BTreeMap::new(),
            url: Some("https://mcp.example.com/mcp".into()),
            disabled: None,
        }
    }

    fn merged(pairs: &[(&str, McpServerEntry)]) -> MergedEntries {
        pairs
            .iter()
            .map(|(n, e)| ((*n).to_string(), (e.clone(), McpSource::Plugin)))
            .collect()
    }

    #[test]
    fn claude_file_uses_dot_prefix_and_mcpservers_key() {
        let dir = tempdir().unwrap();
        let dest = dir.path().join(".mcp.json");
        write_mcp_servers_json(&dest, &merged(&[("codesight", stdio_entry())])).unwrap();
        let text = fs::read_to_string(&dest).unwrap();
        assert!(text.contains("\"mcpServers\""));
        assert!(text.contains("\"command\": \"cargo\""));
        assert!(text.contains("\"RUST_LOG\": \"info\""));
    }

    #[test]
    fn opencode_merge_converts_command_to_array_and_env_to_environment() {
        let dir = tempdir().unwrap();
        let cfg = dir.path().join("opencode.json");
        fs::write(&cfg, "{\"$schema\":\"https://opencode.ai/config.json\"}").unwrap();
        merge_into_opencode_config(&cfg, &merged(&[("codesight", stdio_entry())])).unwrap();
        let v: serde_json::Value =
            serde_json::from_str(&fs::read_to_string(&cfg).unwrap()).unwrap();
        let entry = &v["mcp"]["codesight"];
        assert_eq!(entry["type"], "local");
        assert_eq!(entry["command"][0], "cargo");
        assert_eq!(entry["command"][1], "run");
        assert_eq!(entry["environment"]["RUST_LOG"], "info");
        assert!(entry.get("env").is_none());
        assert!(entry.get("args").is_none());
    }

    #[test]
    fn opencode_merge_remote_entry_uses_type_remote() {
        let dir = tempdir().unwrap();
        let cfg = dir.path().join("opencode.json");
        merge_into_opencode_config(&cfg, &merged(&[("linear", remote_entry())])).unwrap();
        let v: serde_json::Value =
            serde_json::from_str(&fs::read_to_string(&cfg).unwrap()).unwrap();
        let entry = &v["mcp"]["linear"];
        assert_eq!(entry["type"], "remote");
        assert_eq!(entry["url"], "https://mcp.example.com/mcp");
    }

    #[test]
    fn opencode_merge_user_wins_on_conflict() {
        let dir = tempdir().unwrap();
        let cfg = dir.path().join("opencode.json");
        fs::write(
            &cfg,
            r#"{"mcp":{"linear":{"type":"remote","url":"https://user.example/mcp"}}}"#,
        )
        .unwrap();
        merge_into_opencode_config(&cfg, &merged(&[("linear", remote_entry())])).unwrap();
        let v: serde_json::Value =
            serde_json::from_str(&fs::read_to_string(&cfg).unwrap()).unwrap();
        assert_eq!(v["mcp"]["linear"]["url"], "https://user.example/mcp");
    }

    #[test]
    fn codex_merge_writes_mcp_servers_tables() {
        let dir = tempdir().unwrap();
        let cfg = dir.path().join("config.toml");
        fs::write(&cfg, "model = \"gpt-5\"\n").unwrap();
        merge_into_toml_mcp_config(&cfg, &merged(&[("codesight", stdio_entry())])).unwrap();
        let text = fs::read_to_string(&cfg).unwrap();
        assert!(text.contains("[mcp_servers.codesight]"));
        assert!(text.contains("command = \"cargo\""));
        assert!(text.contains("[mcp_servers.codesight.env]"));
        assert!(text.contains("model = \"gpt-5\""));
    }

    #[test]
    fn codex_merge_user_wins_on_conflict() {
        let dir = tempdir().unwrap();
        let cfg = dir.path().join("config.toml");
        fs::write(&cfg, "[mcp_servers.codesight]\ncommand = \"user-cmd\"\n").unwrap();
        merge_into_toml_mcp_config(&cfg, &merged(&[("codesight", stdio_entry())])).unwrap();
        let text = fs::read_to_string(&cfg).unwrap();
        assert!(text.contains("command = \"user-cmd\""));
        assert!(!text.contains("\"cargo\""));
    }

    #[test]
    fn codex_merge_remote_entry_uses_url_field() {
        let dir = tempdir().unwrap();
        let cfg = dir.path().join("config.toml");
        merge_into_toml_mcp_config(&cfg, &merged(&[("linear", remote_entry())])).unwrap();
        let text = fs::read_to_string(&cfg).unwrap();
        assert!(text.contains("[mcp_servers.linear]"));
        assert!(text.contains("url = \"https://mcp.example.com/mcp\""));
    }

    #[test]
    fn grok_merge_writes_native_mcp_servers_tables() {
        let dir = tempdir().unwrap();
        let cfg = dir.path().join("config.toml");
        merge_into_toml_mcp_config(&cfg, &merged(&[("codesight", stdio_entry())])).unwrap();
        let text = fs::read_to_string(&cfg).unwrap();
        assert!(text.contains("[mcp_servers.codesight]"));
        assert!(text.contains("command = \"cargo\""));
        assert!(text.contains("[mcp_servers.codesight.env]"));
    }

    #[test]
    fn grok_merge_remote_entry_uses_url_field() {
        let dir = tempdir().unwrap();
        let cfg = dir.path().join("config.toml");
        merge_into_toml_mcp_config(&cfg, &merged(&[("linear", remote_entry())])).unwrap();
        let text = fs::read_to_string(&cfg).unwrap();
        assert!(text.contains("[mcp_servers.linear]"));
        assert!(text.contains("url = \"https://mcp.example.com/mcp\""));
    }

    #[test]
    fn agy_mcp_remote_uses_server_url() {
        let dir = tempdir().unwrap();
        let cfg = dir.path().join("mcp_config.json");
        write_agy_mcp_config_json(&cfg, &merged(&[("linear", remote_entry())])).unwrap();
        let v: serde_json::Value =
            serde_json::from_str(&fs::read_to_string(&cfg).unwrap()).unwrap();
        let entry = &v["mcpServers"]["linear"];
        assert_eq!(entry["serverUrl"], "https://mcp.example.com/mcp");
        assert!(entry.get("url").is_none());
        assert!(entry.get("httpUrl").is_none());
    }

    #[test]
    fn agy_mcp_local_uses_command_args_env() {
        let dir = tempdir().unwrap();
        let cfg = dir.path().join("mcp_config.json");
        write_agy_mcp_config_json(&cfg, &merged(&[("codesight", stdio_entry())])).unwrap();
        let v: serde_json::Value =
            serde_json::from_str(&fs::read_to_string(&cfg).unwrap()).unwrap();
        let entry = &v["mcpServers"]["codesight"];
        assert_eq!(entry["command"], "cargo");
        assert_eq!(entry["args"][0], "run");
        assert_eq!(entry["env"]["RUST_LOG"], "info");
    }
}
