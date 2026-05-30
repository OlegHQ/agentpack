//! Shared MCP config writer for the harnesses whose native format is `[mcp_servers.<name>]` TOML.
//! Codex and Grok have identical shapes, so the format adapter lives here and both call it.

use std::path::Path;

use crate::error::{AgentpackError, Result};
use crate::fs_util::write_text_file;
use crate::staging::mcp::{McpServerEntry, StagedMcpEntries};

/// Merge MCP entries into a TOML `config.toml` under `[mcp_servers.<name>]` tables. User-seeded
/// entries win: we only insert pack entries whose names are absent.
pub(super) fn merge_into_toml_mcp_config(
    config_path: &Path,
    merged: &StagedMcpEntries,
) -> Result<()> {
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
    write_text_file(config_path, &out)
}

fn toml_mcp_entry_table(entry: &McpServerEntry) -> toml::value::Table {
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

#[cfg(test)]
mod tests {
    use super::*;
    use crate::staging::mcp::test_support::{merged, remote_entry, stdio_entry};
    use std::fs;
    use tempfile::tempdir;

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
}
