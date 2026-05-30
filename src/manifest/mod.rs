//! Project manifest: **`agentpack.toml`** (direct dependencies, modes, and MCP only).
//!
//! This module owns the data types and the read-side (`load`). All `toml_edit`-based mutation
//! lives in the [`edit`] submodule.

mod edit;

use std::collections::BTreeMap;
use std::fs;
use std::path::Path;

use serde::Deserialize;

use crate::error::{AgentpackError, Result};
use crate::mode::{ModeDefinition, DEFAULT_MODE_NAME};
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

#[cfg(test)]
mod tests {
    use std::fs;

    use tempfile::tempdir;

    use super::*;

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
