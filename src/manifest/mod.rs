//! Project manifest: **`agentpack.toml`** (direct dependencies and overrides only).

use std::collections::BTreeMap;
use std::fs;
use std::path::Path;

use serde::Deserialize;

use crate::error::{AgentpackError, Result};
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

#[derive(Debug, Clone, Default, Deserialize)]
pub struct OverrideTable {
    /// Relative paths under the package root to omit from staging (e.g. `commands/review.md`).
    #[serde(default)]
    pub disable: Vec<String>,
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
    pub overrides: BTreeMap<String, OverrideTable>,
}

#[derive(Debug, Clone)]
pub struct AgentpackManifest {
    pub name: String,
    pub version: String,
    pub description: String,
    pub dependencies: BTreeMap<String, DepSpecToml>,
    pub overrides: BTreeMap<String, OverrideTable>,
}

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

impl AgentpackManifest {
    pub fn disable_paths_for_module(&self, module: &str) -> &[String] {
        self.overrides
            .get(module)
            .map(|o| o.disable.as_slice())
            .unwrap_or(&[])
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
            overrides: file.overrides,
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

    pub fn append_dependency_key(project_root: &Path, module_key: &str) -> Result<()> {
        with_manifest_document_mut(project_root, |doc| {
            let deps = doc
                .entry("dependencies")
                .or_insert(toml_edit::Item::Table(toml_edit::Table::new()));
            let tab = deps.as_table_mut().ok_or_else(|| {
                AgentpackError::LockfileParse("[dependencies] must be a table".into())
            })?;
            // Logical key only — `toml_edit` quotes the key in output when needed (dots/slashes).
            // Do not wrap in `\"…\"` here: that would make the key *include* quote characters and
            // break `serde`/resolver (`got "\"github.com/…\""`).
            if tab.get(module_key).is_some() {
                return Ok(());
            }
            tab.insert(
                module_key,
                toml_edit::Item::Value(
                    toml_edit::Value::InlineTable(toml_edit::InlineTable::new()),
                ),
            );
            Ok(())
        })
    }

    /// Append a **path** dependency: `name = { path = "rel_path" }`.
    pub fn append_path_dependency(project_root: &Path, name: &str, rel_path: &str) -> Result<()> {
        with_manifest_document_mut(project_root, |doc| {
            let deps = doc
                .entry("dependencies")
                .or_insert(toml_edit::Item::Table(toml_edit::Table::new()));
            let tab = deps.as_table_mut().ok_or_else(|| {
                AgentpackError::LockfileParse("[dependencies] must be a table".into())
            })?;
            let mut inline = toml_edit::InlineTable::new();
            inline.insert("path", toml_edit::Value::from(rel_path));
            tab.insert(
                name,
                toml_edit::Item::Value(toml_edit::Value::InlineTable(inline)),
            );
            Ok(())
        })
    }

    /// Remove **`module_key`** from **`[dependencies]`** and **`[overrides]`** (if present).
    pub fn remove_dependency_entry(project_root: &Path, module_key: &str) -> Result<()> {
        with_manifest_document_mut(project_root, |doc| {
            if let Some(deps) = doc.get_mut("dependencies") {
                if let Some(tab) = deps.as_table_mut() {
                    tab.remove(module_key);
                }
            }
            if let Some(ov) = doc.get_mut("overrides") {
                if let Some(tab) = ov.as_table_mut() {
                    tab.remove(module_key);
                }
            }
            Ok(())
        })
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
