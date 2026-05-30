//! Project manifest: **`agentpack.toml`** (direct dependencies, modes, and MCP only).
//!
//! Two halves in one module: the data types + read-side (`load`), then the `toml_edit`-based
//! mutation methods (every `pub fn` taking `project_root` loads the document, edits it, and
//! persists — preserving formatting and comments).

use std::collections::BTreeMap;
use std::fs;
use std::path::Path;

use serde::Deserialize;

use crate::error::{AgentpackError, Result};
use crate::mode::{self, ModeBase, ModeDefinition, DEFAULT_MODE_NAME};
use crate::paths::manifest_path;

#[derive(Debug, Clone, Deserialize)]
#[serde(untagged)]
pub enum DepSpecToml {
    Short(String),
    Table(DepTable),
}

#[derive(Debug, Clone, Default, Deserialize)]
pub struct DepTable {
    /// Local filesystem path (relative to project root).
    #[serde(default)]
    pub path: Option<String>,
    #[serde(default)]
    pub commit: Option<String>,
    #[serde(default)]
    pub tag: Option<String>,
    #[serde(default)]
    pub branch: Option<String>,
    /// Semver requirement (e.g. `^1.2.0`) for tag resolution.
    #[serde(default)]
    pub version: Option<String>,
}

impl DepSpecToml {
    /// Returns the `path` value if this is a filesystem path dependency.
    pub fn path_value(&self) -> Option<&str> {
        match self {
            DepSpecToml::Table(t) => t.path.as_deref(),
            _ => None,
        }
    }
}

/// `[mcp]` section in **`agentpack.toml`** — project-level MCP server definitions.
///
/// Uses [`crate::staging::mcp::McpServerEntry`] — the same type serves both TOML manifest
/// and JSON `mcp.json` (Serialize + Deserialize with serde defaults).
#[derive(Debug, Clone, Default, Deserialize)]
pub struct McpSection {
    #[serde(default)]
    pub servers: BTreeMap<String, crate::staging::mcp::McpServerEntry>,
}

/// Nested **`agentpack.toml`** in a package (dependencies only).
#[derive(Debug, Clone, Deserialize)]
pub struct NestedManifest {
    #[serde(default)]
    pub dependencies: BTreeMap<String, DepSpecToml>,
}

#[derive(Debug, Clone, Deserialize)]
pub struct ManifestFile {
    #[serde(default)]
    pub name: String,
    #[serde(default)]
    pub version: String,
    /// Package blurb for synthesized `.claude-plugin` / `.cursor-plugin` when those dirs are absent.
    #[serde(default)]
    pub description: String,
    #[serde(default)]
    pub dependencies: BTreeMap<String, DepSpecToml>,
    #[serde(default)]
    pub mcp: McpSection,
    #[serde(default)]
    pub modes: BTreeMap<String, ModeDefinition>,
}

#[derive(Debug, Clone)]
pub struct AgentpackManifest {
    pub name: String,
    pub version: String,
    pub description: String,
    pub dependencies: BTreeMap<String, DepSpecToml>,
    pub mcp: McpSection,
    pub modes: BTreeMap<String, ModeDefinition>,
}

impl AgentpackManifest {
    pub fn explicit_modes(&self) -> &BTreeMap<String, ModeDefinition> {
        &self.modes
    }

    pub fn mode_definition(&self, name: &str) -> Option<ModeDefinition> {
        self.modes
            .get(name)
            .cloned()
            .or_else(|| (name == DEFAULT_MODE_NAME).then(ModeDefinition::implicit_default))
    }

    pub fn list_mode_names(&self) -> Vec<String> {
        let mut names: Vec<_> = self.modes.keys().cloned().collect();
        if !names.iter().any(|name| name == DEFAULT_MODE_NAME) {
            names.push(DEFAULT_MODE_NAME.into());
        }
        names.sort();
        names
    }

    pub fn load(project_root: &Path) -> Result<Option<Self>> {
        let p = manifest_path(project_root);
        if !p.is_file() {
            return Ok(None);
        }
        let raw = fs::read_to_string(&p).map_err(|e| AgentpackError::io(&p, e))?;
        let file: ManifestFile = toml::from_str(&raw)
            .map_err(|e| AgentpackError::LockfileParse(format!("agentpack.toml: {e}")))?;
        let name = if file.name.is_empty() {
            project_root
                .file_name()
                .and_then(|s| s.to_str())
                .unwrap_or("project")
                .to_string()
        } else {
            file.name
        };
        let version = if file.version.is_empty() {
            "0.0.1".to_string()
        } else {
            file.version
        };
        Ok(Some(Self {
            name,
            version,
            description: file.description,
            dependencies: file.dependencies,
            mcp: file.mcp,
            modes: file.modes,
        }))
    }

    /// Read **`agentpack.toml`** from a cached package root (for transitive resolution).
    pub fn load_nested_dependencies(
        cache_root: &std::path::Path,
    ) -> Result<Option<BTreeMap<String, DepSpecToml>>> {
        let p = cache_root.join(crate::paths::MANIFEST_NAME);
        if !p.is_file() {
            return Ok(None);
        }
        let raw = fs::read_to_string(&p).map_err(|e| AgentpackError::io(&p, e))?;
        let nested: NestedManifest = toml::from_str(&raw)
            .map_err(|e| AgentpackError::LockfileParse(format!("{}: {e}", p.display())))?;
        if nested.dependencies.is_empty() {
            Ok(None)
        } else {
            Ok(Some(nested.dependencies))
        }
    }
}

// ---- write-side: `toml_edit` mutation (load → edit → persist, preserving formatting) ----

fn with_manifest_document_mut(
    project_root: &Path,
    f: impl FnOnce(&mut toml_edit::DocumentMut) -> Result<()>,
) -> Result<()> {
    let p = manifest_path(project_root);
    let src = fs::read_to_string(&p).map_err(|e| AgentpackError::io(&p, e))?;
    let mut doc: toml_edit::DocumentMut = src
        .parse()
        .map_err(|e| AgentpackError::LockfileParse(format!("agentpack.toml: {e}")))?;
    f(&mut doc)?;
    fs::write(&p, doc.to_string()).map_err(|e| AgentpackError::io(&p, e))?;
    Ok(())
}

fn get_or_insert_table<'d>(
    doc: &'d mut toml_edit::DocumentMut,
    key: &str,
) -> Result<&'d mut toml_edit::Table> {
    doc.entry(key)
        .or_insert(toml_edit::Item::Table(toml_edit::Table::new()))
        .as_table_mut()
        .ok_or_else(|| AgentpackError::LockfileParse(format!("[{key}] must be a table")))
}

impl AgentpackManifest {
    pub fn append_dependency_key(project_root: &Path, module_key: &str) -> Result<()> {
        with_manifest_document_mut(project_root, |doc| {
            let tab = get_or_insert_table(doc, "dependencies")?;
            if tab.get(module_key).is_none() {
                tab.insert(
                    module_key,
                    toml_edit::Item::Value(toml_edit::Value::InlineTable(
                        toml_edit::InlineTable::new(),
                    )),
                );
            }
            Ok(())
        })
    }

    /// Append a **path** dependency: `name = { path = "rel_path" }`.
    pub fn append_path_dependency(project_root: &Path, name: &str, rel_path: &str) -> Result<()> {
        with_manifest_document_mut(project_root, |doc| {
            let tab = get_or_insert_table(doc, "dependencies")?;
            let mut inline = toml_edit::InlineTable::new();
            inline.insert("path", toml_edit::Value::from(rel_path));
            tab.insert(
                name,
                toml_edit::Item::Value(toml_edit::Value::InlineTable(inline)),
            );
            Ok(())
        })
    }

    /// Remove **`module_key`** from **`[dependencies]`** and prune any mode selectors that target it.
    pub fn remove_dependency_entry(project_root: &Path, module_key: &str) -> Result<()> {
        let manifest = Self::load(project_root)?
            .ok_or_else(|| AgentpackError::ManifestMissing(manifest_path(project_root)))?;
        let mut modes = manifest.modes;
        let keep = |raw: &String| !selector_targets_module(raw, module_key);
        for definition in modes.values_mut() {
            definition.enable.retain(keep);
            definition.disable.retain(keep);
        }
        with_manifest_document_mut(project_root, |doc| {
            if let Some(tab) = doc.get_mut("dependencies").and_then(|t| t.as_table_mut()) {
                tab.remove(module_key);
            }
            rewrite_modes_table(doc, &modes)
        })
    }

    /// Insert (or replace) an MCP server entry under `[mcp.servers.<name>]`.
    pub fn add_mcp_server(
        project_root: &Path,
        name: &str,
        entry: &crate::staging::mcp::McpServerEntry,
    ) -> Result<()> {
        let mut inline = toml_edit::InlineTable::new();
        if let Some(cmd) = &entry.command {
            inline.insert("command", toml_edit::Value::from(cmd.as_str()));
        }
        if let Some(url) = &entry.url {
            inline.insert("url", toml_edit::Value::from(url.as_str()));
        }
        if !entry.args.is_empty() {
            let arr: toml_edit::Array = entry.args.iter().map(|a| a.as_str()).collect();
            inline.insert("args", toml_edit::Value::Array(arr));
        }
        if !entry.env.is_empty() {
            let mut env_tbl = toml_edit::InlineTable::new();
            for (k, v) in &entry.env {
                env_tbl.insert(k.as_str(), toml_edit::Value::from(v.as_str()));
            }
            inline.insert("env", toml_edit::Value::InlineTable(env_tbl));
        }
        let value = toml_edit::Item::Value(toml_edit::Value::InlineTable(inline));
        with_manifest_document_mut(project_root, |doc| {
            let mcp_tab = get_or_insert_table(doc, "mcp")?;
            let servers = mcp_tab
                .entry("servers")
                .or_insert(toml_edit::Item::Table(toml_edit::Table::new()))
                .as_table_mut()
                .ok_or_else(|| {
                    AgentpackError::LockfileParse("[mcp.servers] must be a table".into())
                })?;
            servers.insert(name, value);
            Ok(())
        })
    }

    /// Remove an MCP server entry from `[mcp.servers]`.
    pub fn remove_mcp_server(project_root: &Path, name: &str) -> Result<()> {
        with_manifest_document_mut(project_root, |doc| {
            if let Some(servers) = doc
                .get_mut("mcp")
                .and_then(|m| m.as_table_mut())
                .and_then(|t| t.get_mut("servers"))
                .and_then(|s| s.as_table_mut())
            {
                servers.remove(name);
            }
            Ok(())
        })
    }

    /// Load the manifest, hand a mutable modes map to `f`, and persist the result.
    fn with_modes_mut(
        project_root: &Path,
        f: impl FnOnce(&mut BTreeMap<String, ModeDefinition>) -> Result<()>,
    ) -> Result<()> {
        let manifest = Self::load(project_root)?
            .ok_or_else(|| AgentpackError::ManifestMissing(manifest_path(project_root)))?;
        let mut modes = manifest.modes;
        f(&mut modes)?;
        Self::replace_modes(project_root, &modes)
    }

    pub fn create_mode(project_root: &Path, name: &str) -> Result<()> {
        let name = mode::validate_mode_name(name)?.to_string();
        Self::with_modes_mut(project_root, |modes| {
            if modes.contains_key(&name) {
                return Err(AgentpackError::Mode(format!("mode already exists: {name}")));
            }
            modes.insert(
                name,
                ModeDefinition {
                    base: ModeBase::All,
                    ..Default::default()
                },
            );
            Ok(())
        })
    }

    pub fn delete_mode(project_root: &Path, name: &str) -> Result<()> {
        if mode::is_reserved_mode(name) {
            return Err(AgentpackError::Mode(format!(
                "{DEFAULT_MODE_NAME} is reserved and cannot be deleted"
            )));
        }
        Self::with_modes_mut(project_root, |modes| {
            if modes.remove(name).is_none() {
                return Err(AgentpackError::Mode(format!("unknown mode: {name}")));
            }
            Ok(())
        })
    }

    pub fn rename_mode(project_root: &Path, old: &str, new: &str) -> Result<()> {
        if mode::is_reserved_mode(old) {
            return Err(AgentpackError::Mode(format!(
                "{DEFAULT_MODE_NAME} is reserved and cannot be renamed"
            )));
        }
        let new = mode::validate_mode_name(new)?.to_string();
        Self::with_modes_mut(project_root, |modes| {
            if modes.contains_key(&new) {
                return Err(AgentpackError::Mode(format!("mode already exists: {new}")));
            }
            let definition = modes
                .remove(old)
                .ok_or_else(|| AgentpackError::Mode(format!("unknown mode: {old}")))?;
            modes.insert(new, definition);
            Ok(())
        })
    }

    pub fn set_mode_base(project_root: &Path, name: &str, base: ModeBase) -> Result<()> {
        let name = mode::validate_mode_name(name)?.to_string();
        ensure_mode_editable(&name)?;
        Self::with_modes_mut(project_root, |modes| {
            modes
                .entry(name)
                .or_insert_with(ModeDefinition::implicit_default)
                .base = base;
            Ok(())
        })
    }

    pub fn add_mode_selectors(
        project_root: &Path,
        name: &str,
        enabled: bool,
        selectors: &[String],
    ) -> Result<()> {
        let name = mode::validate_mode_name(name)?.to_string();
        ensure_mode_editable(&name)?;
        let normalized = canonicalize_selectors(selectors)?;
        Self::with_modes_mut(project_root, |modes| {
            let definition = modes
                .entry(name)
                .or_insert_with(ModeDefinition::implicit_default);
            for canonical in normalized {
                if enabled {
                    definition.disable.retain(|entry| entry != &canonical);
                    definition.enable.push(canonical);
                } else {
                    definition.enable.retain(|entry| entry != &canonical);
                    definition.disable.push(canonical);
                }
            }
            definition.sort_and_dedup();
            Ok(())
        })
    }

    pub fn remove_mode_selectors(
        project_root: &Path,
        name: &str,
        selectors: &[String],
    ) -> Result<()> {
        ensure_mode_editable(name)?;
        let normalized = canonicalize_selectors(selectors)?;
        Self::with_modes_mut(project_root, |modes| {
            let definition = modes
                .get_mut(name)
                .ok_or_else(|| AgentpackError::Mode(format!("unknown mode: {name}")))?;
            definition
                .enable
                .retain(|entry| !normalized.iter().any(|selector| selector == entry));
            definition
                .disable
                .retain(|entry| !normalized.iter().any(|selector| selector == entry));
            Ok(())
        })
    }

    pub fn replace_modes(
        project_root: &Path,
        modes: &BTreeMap<String, ModeDefinition>,
    ) -> Result<()> {
        with_manifest_document_mut(project_root, |doc| rewrite_modes_table(doc, modes))
    }

    pub fn write_stub(project_root: &Path, name: &str, version: &str) -> Result<()> {
        let p = manifest_path(project_root);
        if p.exists() {
            return Err(AgentpackError::ManifestExists(p));
        }
        let body = format!(
            r#"# Agentpack project manifest — direct dependencies only. Run `agentpack lock` to refresh pack.lock.

name = "{name}"
version = "{version}"

[dependencies]
# "github.com/owner/repo/path" = {{}}
"#
        );
        fs::write(&p, body).map_err(|e| AgentpackError::io(&p, e))?;
        Ok(())
    }
}

fn rewrite_modes_table(
    doc: &mut toml_edit::DocumentMut,
    modes: &BTreeMap<String, ModeDefinition>,
) -> Result<()> {
    if modes.is_empty() {
        doc.remove("modes");
        return Ok(());
    }

    let mut table = toml_edit::Table::new();
    for (name, definition) in modes {
        let mut mode_table = toml_edit::Table::new();
        mode_table.insert(
            "base",
            toml_edit::Item::Value(toml_edit::Value::from(definition.base.as_str())),
        );
        if !definition.enable.is_empty() {
            let array: toml_edit::Array = definition
                .enable
                .iter()
                .map(|value| value.as_str())
                .collect();
            mode_table.insert(
                "enable",
                toml_edit::Item::Value(toml_edit::Value::Array(array)),
            );
        }
        if !definition.disable.is_empty() {
            let array: toml_edit::Array = definition
                .disable
                .iter()
                .map(|value| value.as_str())
                .collect();
            mode_table.insert(
                "disable",
                toml_edit::Item::Value(toml_edit::Value::Array(array)),
            );
        }
        table.insert(name, toml_edit::Item::Table(mode_table));
    }
    doc.insert("modes", toml_edit::Item::Table(table));
    Ok(())
}

fn canonicalize_selectors(raw: &[String]) -> Result<Vec<String>> {
    raw.iter()
        .map(|entry| {
            crate::mode::selectors::Selector::parse(entry)
                .map(|selector| selector.canonical_string())
        })
        .collect()
}

fn ensure_mode_editable(name: &str) -> Result<()> {
    if mode::is_reserved_mode(name) {
        return Err(AgentpackError::Mode(format!(
            "{DEFAULT_MODE_NAME} is read-only"
        )));
    }
    Ok(())
}

fn selector_targets_module(raw: &str, module_key: &str) -> bool {
    crate::mode::selectors::Selector::parse(raw)
        .ok()
        .and_then(|selector| selector.module().map(|module| module == module_key))
        .unwrap_or(false)
}

#[cfg(test)]
mod tests {
    use std::fs;

    use tempfile::tempdir;

    use super::*;
    use crate::mode::ModeBase;

    fn write_manifest(root: &Path, extra: &str) {
        fs::write(
            manifest_path(root),
            format!(
                "name = \"proj\"\nversion = \"0.1.0\"\n\n[dependencies]\n\"github.com/acme/repo\" = {{}}\n\n{extra}"
            ),
        )
        .unwrap();
    }

    #[test]
    fn implicit_default_mode_is_available_on_read() {
        let dir = tempdir().unwrap();
        write_manifest(dir.path(), "");
        let manifest = AgentpackManifest::load(dir.path()).unwrap().unwrap();
        let default = manifest.mode_definition("default").unwrap();
        assert_eq!(default, ModeDefinition::implicit_default());
        assert!(manifest.list_mode_names().contains(&"default".to_string()));
    }

    #[test]
    fn mode_crud_helpers_roundtrip_manifest() {
        let dir = tempdir().unwrap();
        write_manifest(dir.path(), "");

        AgentpackManifest::create_mode(dir.path(), "design").unwrap();
        AgentpackManifest::set_mode_base(dir.path(), "design", ModeBase::None).unwrap();
        AgentpackManifest::add_mode_selectors(
            dir.path(),
            "design",
            true,
            &[
                "package:github.com/acme/repo".into(),
                ".agents:rules/backend.mdc".into(),
            ],
        )
        .unwrap();
        AgentpackManifest::rename_mode(dir.path(), "design", "review").unwrap();

        let manifest = AgentpackManifest::load(dir.path()).unwrap().unwrap();
        let review = manifest.mode_definition("review").unwrap();
        assert_eq!(review.base, ModeBase::None);
        assert_eq!(
            review.enable,
            vec![
                ".agents:rules/backend.mdc".to_string(),
                "package:github.com/acme/repo".to_string()
            ]
        );

        AgentpackManifest::delete_mode(dir.path(), "review").unwrap();
        let manifest = AgentpackManifest::load(dir.path()).unwrap().unwrap();
        assert!(manifest.mode_definition("review").is_none());
    }

    #[test]
    fn removing_dependency_prunes_mode_selectors_for_that_module() {
        let dir = tempdir().unwrap();
        write_manifest(
            dir.path(),
            r#"[modes.default]
base = "all"
disable = ["package:github.com/acme/repo", "package-path:github.com/acme/repo:hooks"]
"#,
        );

        AgentpackManifest::remove_dependency_entry(dir.path(), "github.com/acme/repo").unwrap();
        let manifest = AgentpackManifest::load(dir.path()).unwrap().unwrap();
        let default = manifest.mode_definition("default").unwrap();
        assert!(default.disable.is_empty());
    }
}
