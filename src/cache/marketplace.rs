//! Normalize a single-plugin marketplace repository into the package-root layout used by the
//! resolver and staging pipeline.
//!
//! Some publishers keep one logical plugin in per-harness directories and expose those directories
//! through marketplace manifests at the repository root. Agentpack locks the repository root as one
//! package, so this module resolves the local marketplace sources once and merges them into a
//! canonical cache root before ordinary plugin classification runs.

use std::collections::{BTreeMap, BTreeSet, HashSet};
use std::fs;
use std::path::{Component, Path, PathBuf};
use std::sync::atomic::{AtomicU64, Ordering};

use serde::Deserialize;
use serde_json::Value;

use crate::error::{AgentpackError, Result};
use crate::fs_util::{copy_merge_tree, read_json_value};

const CLAUDE_MARKETPLACE: &str = ".claude-plugin/marketplace.json";
const CURSOR_MARKETPLACE: &str = ".cursor-plugin/marketplace.json";
const CODEX_MARKETPLACE: &str = ".agents/plugins/marketplace.json";

pub(super) const MARKETPLACE_MANIFESTS: [&str; 3] =
    [CLAUDE_MARKETPLACE, CURSOR_MARKETPLACE, CODEX_MARKETPLACE];

const PLUGIN_MANIFESTS: [&str; 3] = [
    ".claude-plugin/plugin.json",
    ".cursor-plugin/plugin.json",
    ".codex-plugin/plugin.json",
];

#[derive(Clone, Copy)]
struct MarketplaceSpec {
    relative_path: &'static str,
    // Sources are merged from least to most preferred. Claude is the portable baseline agentpack
    // historically consumed; Cursor and Codex sources supplement it with target-specific files.
    priority: u8,
}

const MARKETPLACE_SPECS: [MarketplaceSpec; 3] = [
    MarketplaceSpec {
        relative_path: CODEX_MARKETPLACE,
        priority: 0,
    },
    MarketplaceSpec {
        relative_path: CURSOR_MARKETPLACE,
        priority: 1,
    },
    MarketplaceSpec {
        relative_path: CLAUDE_MARKETPLACE,
        priority: 2,
    },
];

#[derive(Default, Deserialize)]
struct MarketplaceDocument {
    #[serde(default)]
    metadata: MarketplaceMetadata,
    #[serde(default)]
    plugins: Vec<MarketplacePlugin>,
}

#[derive(Default, Deserialize)]
struct MarketplaceMetadata {
    #[serde(default, rename = "pluginRoot")]
    plugin_root: Option<String>,
}

#[derive(Deserialize)]
struct MarketplacePlugin {
    name: String,
    source: Value,
}

struct LocalPluginSource {
    path: PathBuf,
    priority: u8,
}

pub(super) fn cache_has_marketplace_manifest(root: &Path) -> bool {
    MARKETPLACE_MANIFESTS
        .iter()
        .any(|relative| root.join(relative).is_file())
}

pub(super) fn repo_dir_has_marketplace_manifest(paths: &HashSet<String>, dir: &str) -> bool {
    let dir = dir.trim_matches('/');
    MARKETPLACE_MANIFESTS.iter().any(|relative| {
        let path = if dir.is_empty() {
            (*relative).to_string()
        } else {
            format!("{dir}/{relative}")
        };
        paths.contains(&path)
    })
}

/// Materialize a marketplace root when it represents exactly one logical plugin.
///
/// Returns `false` when no marketplace exists. A marketplace containing multiple plugin names is
/// intentionally rejected: one `LockPackage` cannot truthfully represent an arbitrary catalog, and
/// silently flattening such a catalog would make artifact collisions order-dependent.
pub(super) fn materialize_single_plugin_marketplace(root: &Path) -> Result<bool> {
    if !cache_has_marketplace_manifest(root) {
        return Ok(false);
    }

    let canonical_root = fs::canonicalize(root).map_err(|err| AgentpackError::io(root, err))?;
    let mut plugin_names = BTreeSet::new();
    let mut local_sources: BTreeMap<PathBuf, u8> = BTreeMap::new();

    for spec in MARKETPLACE_SPECS {
        let manifest_path = root.join(spec.relative_path);
        if !manifest_path.is_file() {
            continue;
        }
        let document: MarketplaceDocument =
            serde_json::from_value(read_json_value(&manifest_path)?).map_err(|err| {
                AgentpackError::Cache(format!(
                    "invalid marketplace manifest {}: {err}",
                    manifest_path.display()
                ))
            })?;
        let mut names_in_document = HashSet::with_capacity(document.plugins.len());

        for plugin in document.plugins {
            let name = plugin.name.trim();
            if name.is_empty() {
                return Err(AgentpackError::Cache(format!(
                    "marketplace manifest {} contains a plugin with an empty name",
                    manifest_path.display()
                )));
            }
            if !names_in_document.insert(name.to_string()) {
                return Err(AgentpackError::Cache(format!(
                    "marketplace manifest {} contains duplicate plugin name {name:?}",
                    manifest_path.display()
                )));
            }
            plugin_names.insert(name.to_string());

            let Some(relative_source) = local_source_path(
                &plugin.source,
                document.metadata.plugin_root.as_deref(),
                &manifest_path,
                name,
            )?
            else {
                continue;
            };
            let source =
                canonical_local_source(&canonical_root, &relative_source, &manifest_path, name)?;
            validate_plugin_source(&source, name, &manifest_path)?;
            local_sources
                .entry(source)
                .and_modify(|priority| *priority = (*priority).max(spec.priority))
                .or_insert(spec.priority);
        }
    }

    if plugin_names.is_empty() {
        return Err(AgentpackError::Cache(format!(
            "marketplace at {} contains no plugins",
            root.display()
        )));
    }
    if plugin_names.len() != 1 {
        let names = plugin_names.into_iter().collect::<Vec<_>>().join(", ");
        return Err(AgentpackError::Cache(format!(
            "marketplace at {} contains multiple plugins ({names}); add a specific plugin source directory instead",
            root.display()
        )));
    }
    if local_sources.is_empty() {
        let name = plugin_names.into_iter().next().unwrap_or_default();
        return Err(AgentpackError::Cache(format!(
            "marketplace plugin {name:?} at {} has no local source; add its Git or package source directly",
            root.display()
        )));
    }

    let mut sources: Vec<LocalPluginSource> = local_sources
        .into_iter()
        .map(|(path, priority)| LocalPluginSource { path, priority })
        .collect();
    sources.sort_by(|a, b| {
        a.priority
            .cmp(&b.priority)
            .then_with(|| a.path.cmp(&b.path))
    });

    let normalized = unique_sibling(root, "marketplace-normalized")?;
    fs::create_dir_all(&normalized).map_err(|err| AgentpackError::io(&normalized, err))?;
    let merge_result = sources
        .iter()
        .try_for_each(|source| copy_merge_tree(&source.path, &normalized));
    if let Err(err) = merge_result {
        let _ = fs::remove_dir_all(&normalized);
        return Err(err);
    }

    if !PLUGIN_MANIFESTS
        .iter()
        .any(|relative| normalized.join(relative).is_file())
    {
        let _ = fs::remove_dir_all(&normalized);
        return Err(AgentpackError::Cache(format!(
            "marketplace at {} did not resolve to a plugin manifest",
            root.display()
        )));
    }

    replace_directory(root, &normalized)?;
    Ok(true)
}

fn local_source_path(
    source: &Value,
    plugin_root: Option<&str>,
    manifest_path: &Path,
    plugin_name: &str,
) -> Result<Option<PathBuf>> {
    let source_text = match source {
        Value::String(path) => Some(path.as_str()),
        Value::Object(object) => match object.get("source").and_then(Value::as_str) {
            Some("local") => Some(object.get("path").and_then(Value::as_str).ok_or_else(|| {
                AgentpackError::Cache(format!(
                    "local marketplace source for {plugin_name:?} in {} has no string path",
                    manifest_path.display()
                ))
            })?),
            _ => None,
        },
        _ => {
            return Err(AgentpackError::Cache(format!(
                "marketplace source for {plugin_name:?} in {} must be a string or object",
                manifest_path.display()
            )))
        }
    };
    let Some(source_text) = source_text else {
        return Ok(None);
    };

    // Official marketplace formats require `./` for an ordinary local source. Claude also permits
    // a short leaf such as `formatter` when `metadata.pluginRoot` supplies the base directory.
    if plugin_root.is_none() && !source_text.starts_with("./") {
        return Err(AgentpackError::Cache(format!(
            "local marketplace source {source_text:?} for {plugin_name:?} in {} must start with ./",
            manifest_path.display()
        )));
    }

    let mut relative = PathBuf::new();
    if let Some(base) = plugin_root {
        relative.push(safe_relative_path(base, manifest_path, plugin_name)?);
    }
    relative.push(safe_relative_path(source_text, manifest_path, plugin_name)?);
    if relative.as_os_str().is_empty() {
        return Err(AgentpackError::Cache(format!(
            "local marketplace source for {plugin_name:?} in {} resolves to the marketplace root",
            manifest_path.display()
        )));
    }
    Ok(Some(relative))
}

fn safe_relative_path(value: &str, manifest_path: &Path, plugin_name: &str) -> Result<PathBuf> {
    let path = Path::new(value);
    let mut clean = PathBuf::new();
    for component in path.components() {
        match component {
            Component::CurDir => {}
            Component::Normal(segment) => clean.push(segment),
            Component::ParentDir | Component::RootDir | Component::Prefix(_) => {
                return Err(AgentpackError::Cache(format!(
                    "unsafe marketplace source {value:?} for {plugin_name:?} in {}: paths must stay inside the marketplace root",
                    manifest_path.display()
                )))
            }
        }
    }
    Ok(clean)
}

fn canonical_local_source(
    canonical_root: &Path,
    relative_source: &Path,
    manifest_path: &Path,
    plugin_name: &str,
) -> Result<PathBuf> {
    let candidate = canonical_root.join(relative_source);
    let canonical = fs::canonicalize(&candidate).map_err(|err| {
        AgentpackError::Cache(format!(
            "cannot resolve marketplace source for {plugin_name:?} in {} at {}: {err}",
            manifest_path.display(),
            candidate.display()
        ))
    })?;
    if !canonical.starts_with(canonical_root) {
        return Err(AgentpackError::Cache(format!(
            "marketplace source for {plugin_name:?} in {} escapes the marketplace root: {}",
            manifest_path.display(),
            candidate.display()
        )));
    }
    if !canonical.is_dir() {
        return Err(AgentpackError::Cache(format!(
            "marketplace source for {plugin_name:?} in {} is not a directory: {}",
            manifest_path.display(),
            candidate.display()
        )));
    }
    Ok(canonical)
}

fn validate_plugin_source(source: &Path, expected_name: &str, marketplace: &Path) -> Result<()> {
    let mut found_manifest = false;
    for relative in PLUGIN_MANIFESTS {
        let manifest = source.join(relative);
        if !manifest.is_file() {
            continue;
        }
        found_manifest = true;
        let value = read_json_value(&manifest)?;
        let actual_name = value.get("name").and_then(Value::as_str).ok_or_else(|| {
            AgentpackError::Cache(format!(
                "plugin manifest {} referenced by {} has no string name",
                manifest.display(),
                marketplace.display()
            ))
        })?;
        if actual_name != expected_name {
            return Err(AgentpackError::Cache(format!(
                "marketplace plugin name {expected_name:?} in {} does not match manifest name {actual_name:?} at {}",
                marketplace.display(),
                manifest.display()
            )));
        }
    }
    if !found_manifest {
        return Err(AgentpackError::Cache(format!(
            "marketplace plugin {expected_name:?} in {} points to {}, which has no .claude-plugin, .cursor-plugin, or .codex-plugin manifest",
            marketplace.display(),
            source.display()
        )));
    }
    Ok(())
}

static UNIQUE_PATH_COUNTER: AtomicU64 = AtomicU64::new(0);

fn unique_sibling(path: &Path, label: &str) -> Result<PathBuf> {
    let parent = path.parent().ok_or_else(|| {
        AgentpackError::Cache(format!("cache root has no parent: {}", path.display()))
    })?;
    let name = path
        .file_name()
        .and_then(|value| value.to_str())
        .unwrap_or("package");
    loop {
        let serial = UNIQUE_PATH_COUNTER.fetch_add(1, Ordering::Relaxed);
        let candidate = parent.join(format!(
            ".agentpack-{label}-{name}-{}-{serial}",
            std::process::id()
        ));
        if !candidate.exists() {
            return Ok(candidate);
        }
    }
}

fn replace_directory(current: &Path, replacement: &Path) -> Result<()> {
    let backup = unique_sibling(current, "marketplace-backup")?;
    fs::rename(current, &backup).map_err(|err| AgentpackError::io(current, err))?;
    if let Err(err) = fs::rename(replacement, current) {
        let rollback = fs::rename(&backup, current);
        let _ = fs::remove_dir_all(replacement);
        if let Err(rollback_err) = rollback {
            return Err(AgentpackError::Cache(format!(
                "failed to install normalized marketplace cache ({err}) and rollback {} ({rollback_err})",
                current.display()
            )));
        }
        return Err(AgentpackError::io(current, err));
    }
    fs::remove_dir_all(&backup).map_err(|err| AgentpackError::io(&backup, err))
}

#[cfg(test)]
mod tests {
    use super::*;

    fn write_json(path: &Path, value: Value) {
        if let Some(parent) = path.parent() {
            fs::create_dir_all(parent).unwrap();
        }
        fs::write(path, serde_json::to_vec_pretty(&value).unwrap()).unwrap();
    }

    fn write_plugin(root: &Path, harness: &str, name: &str) {
        let manifest_dir = match harness {
            "claude" => ".claude-plugin",
            "cursor" => ".cursor-plugin",
            "codex" => ".codex-plugin",
            _ => unreachable!(),
        };
        write_json(
            &root.join(manifest_dir).join("plugin.json"),
            serde_json::json!({"name": name, "version": "1.0.0"}),
        );
    }

    #[test]
    fn merges_three_marketplaces_for_one_logical_plugin() {
        let temp = tempfile::tempdir().unwrap();
        let root = temp.path().join("marketplace");
        let claude = root.join("providers/claude/plugin");
        let cursor = root.join("providers/cursor/plugin");
        let codex = root.join("providers/codex/plugin");
        write_plugin(&claude, "claude", "paddle");
        write_plugin(&cursor, "cursor", "paddle");
        write_plugin(&codex, "codex", "paddle");
        fs::create_dir_all(claude.join("skills/billing")).unwrap();
        fs::write(claude.join("skills/billing/SKILL.md"), "# Billing\n").unwrap();
        fs::create_dir_all(cursor.join("skills/billing")).unwrap();
        fs::write(cursor.join("skills/billing/SKILL.md"), "# Cursor billing\n").unwrap();
        fs::create_dir_all(codex.join("skills/billing")).unwrap();
        fs::write(codex.join("skills/billing/SKILL.md"), "# Codex billing\n").unwrap();
        fs::create_dir_all(cursor.join("rules")).unwrap();
        fs::write(cursor.join("rules/paddle.mdc"), "# Paddle\n").unwrap();
        fs::create_dir_all(codex.join("assets")).unwrap();
        fs::write(codex.join("assets/logo.svg"), "<svg/>\n").unwrap();

        write_json(
            &root.join(CLAUDE_MARKETPLACE),
            serde_json::json!({"plugins": [{"name": "paddle", "source": "./providers/claude/plugin"}]}),
        );
        write_json(
            &root.join(CURSOR_MARKETPLACE),
            serde_json::json!({"plugins": [{"name": "paddle", "source": "./providers/cursor/plugin"}]}),
        );
        write_json(
            &root.join(CODEX_MARKETPLACE),
            serde_json::json!({"plugins": [{"name": "paddle", "source": {"source": "local", "path": "./providers/codex/plugin"}}]}),
        );

        assert!(materialize_single_plugin_marketplace(&root).unwrap());
        assert!(root.join(".claude-plugin/plugin.json").is_file());
        assert!(root.join(".cursor-plugin/plugin.json").is_file());
        assert!(root.join(".codex-plugin/plugin.json").is_file());
        assert!(root.join("skills/billing/SKILL.md").is_file());
        assert_eq!(
            fs::read_to_string(root.join("skills/billing/SKILL.md")).unwrap(),
            "# Billing\n",
            "Claude is the deterministic portable baseline on cross-provider conflicts"
        );
        assert!(root.join("rules/paddle.mdc").is_file());
        assert!(root.join("assets/logo.svg").is_file());
        assert!(!root.join(CLAUDE_MARKETPLACE).exists());
        assert!(!materialize_single_plugin_marketplace(&root).unwrap());
    }

    #[test]
    fn supports_claude_plugin_root_metadata() {
        let temp = tempfile::tempdir().unwrap();
        let root = temp.path().join("marketplace");
        let plugin = root.join("plugins/paddle");
        write_plugin(&plugin, "claude", "paddle");
        write_json(
            &root.join(CLAUDE_MARKETPLACE),
            serde_json::json!({
                "metadata": {"pluginRoot": "./plugins"},
                "plugins": [{"name": "paddle", "source": "paddle"}]
            }),
        );

        assert!(materialize_single_plugin_marketplace(&root).unwrap());
        assert!(root.join(".claude-plugin/plugin.json").is_file());
    }

    #[test]
    fn rejects_multi_plugin_catalog_instead_of_flattening_it() {
        let temp = tempfile::tempdir().unwrap();
        let root = temp.path().join("marketplace");
        for name in ["one", "two"] {
            write_plugin(&root.join("plugins").join(name), "claude", name);
        }
        write_json(
            &root.join(CLAUDE_MARKETPLACE),
            serde_json::json!({"plugins": [
                {"name": "one", "source": "./plugins/one"},
                {"name": "two", "source": "./plugins/two"}
            ]}),
        );

        let error = materialize_single_plugin_marketplace(&root).unwrap_err();
        assert!(error.to_string().contains("multiple plugins (one, two)"));
        assert!(root.join(CLAUDE_MARKETPLACE).is_file());
    }

    #[test]
    fn rejects_source_path_traversal() {
        let temp = tempfile::tempdir().unwrap();
        let root = temp.path().join("marketplace");
        fs::create_dir_all(&root).unwrap();
        write_json(
            &root.join(CLAUDE_MARKETPLACE),
            serde_json::json!({"plugins": [{"name": "escape", "source": "./../escape"}]}),
        );

        let error = materialize_single_plugin_marketplace(&root).unwrap_err();
        assert!(error.to_string().contains("unsafe marketplace source"));
    }
}
