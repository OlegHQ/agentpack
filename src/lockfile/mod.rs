use std::collections::HashMap;
use std::fmt;
use std::fs;
use std::path::Path;

use serde::{Deserialize, Serialize};

use crate::error::{AgentpackError, Result};
use crate::paths::lock_path;

/// Whether a locked entry is a bare skill or a full plugin directory.
#[derive(Clone, Copy, Debug, Default, Eq, Hash, PartialEq, Serialize, Deserialize)]
#[serde(rename_all = "lowercase")]
pub enum PackageKind {
    #[default]
    Skill,
    Plugin,
}

impl fmt::Display for PackageKind {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            Self::Skill => f.write_str("skill"),
            Self::Plugin => f.write_str("plugin"),
        }
    }
}

fn is_default_config(c: &Config) -> bool {
    c.disabled_plugins.is_empty()
}

fn is_zero(v: &u32) -> bool {
    *v == 0
}

#[derive(Debug, Clone, Default, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct PackLock {
    /// Canonical lockfile version. Pre-release but `2` is the only supported on-disk schema.
    #[serde(default, skip_serializing_if = "is_zero")]
    pub lockfile_version: u32,
    pub meta: Meta,
    #[serde(default, skip_serializing_if = "is_default_config")]
    pub config: Config,
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub packages: Vec<LockPackage>,
    /// Derived read-models for command code; never serialized or parsed from disk.
    #[serde(skip)]
    pub skills: Vec<LockSkill>,
    /// Derived read-models for command code; never serialized or parsed from disk.
    #[serde(skip)]
    pub plugins: Vec<LockPlugin>,
}

/// Single locked package (v2 lockfile). Kind is **`skill`** or **`plugin`**.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct LockPackage {
    pub module: String,
    #[serde(default)]
    pub direct: bool,
    pub kind: PackageKind,
    pub url: String,
    pub owner: String,
    pub repo: String,
    #[serde(default)]
    pub path: String,
    pub commit: String,
    pub cache_key: String,
    #[serde(default)]
    pub name: String,
}

impl LockPackage {
    pub fn to_lock_skill(&self) -> LockSkill {
        LockSkill {
            module: self.module.clone(),
            url: self.url.clone(),
            owner: self.owner.clone(),
            repo: self.repo.clone(),
            path: self.path.clone(),
            commit: self.commit.clone(),
            cache_key: self.cache_key.clone(),
        }
    }

    pub fn to_lock_plugin(&self) -> LockPlugin {
        LockPlugin {
            module: self.module.clone(),
            name: self.name.clone(),
            url: self.url.clone(),
            owner: self.owner.clone(),
            repo: self.repo.clone(),
            path: self.path.clone(),
            commit: self.commit.clone(),
            cache_key: self.cache_key.clone(),
        }
    }
}

#[derive(Debug, Clone, Default, Serialize, Deserialize)]
pub struct Meta {
    #[serde(default)]
    pub name: String,
    #[serde(default)]
    pub version: String,
}

#[derive(Debug, Clone, Default, Serialize, Deserialize)]
pub struct Config {
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub disabled_plugins: Vec<String>,
}

#[derive(Debug, Clone)]
pub struct LockSkill {
    pub module: String,
    pub url: String,
    pub owner: String,
    pub repo: String,
    pub path: String,
    pub commit: String,
    pub cache_key: String,
}

/// GitHub-pinned plugin read-model derived from canonical packages or network fetches.
#[derive(Debug, Clone)]
pub struct LockPlugin {
    pub module: String,
    pub name: String,
    pub url: String,
    pub owner: String,
    pub repo: String,
    pub path: String,
    pub commit: String,
    pub cache_key: String,
}

impl LockPlugin {
    /// True when a plugin row is only partially populated and still needs network resolution.
    pub fn needs_backfill(&self) -> bool {
        !self.url.is_empty()
            && (self.cache_key.is_empty()
                || self.commit.is_empty()
                || self.owner.is_empty()
                || self.repo.is_empty())
    }
}

impl PackLock {
    pub fn load_from_path(p: &Path) -> Result<Self> {
        let raw = fs::read_to_string(p).map_err(|e| AgentpackError::io(p, e))?;
        let mut lock: PackLock =
            toml::from_str(&raw).map_err(|e| AgentpackError::LockfileParse(e.to_string()))?;
        lock.sync_views_from_packages();
        Ok(lock)
    }

    pub fn load(project_root: &Path) -> Result<Self> {
        Self::load_from_path(&lock_path(project_root))
    }

    /// Facade read-models: `packages` is canonical, `skills/plugins` are derived for the rest of the codebase.
    pub fn sync_views_from_packages(&mut self) {
        self.skills.clear();
        self.plugins.clear();
        let mut pkgs = self.packages.clone();
        pkgs.sort_by(|a, b| a.module.cmp(&b.module));
        for p in pkgs {
            match p.kind {
                PackageKind::Plugin => self.plugins.push(p.to_lock_plugin()),
                PackageKind::Skill => self.skills.push(p.to_lock_skill()),
            }
        }
    }

    fn sync_packages_from_views(&mut self) {
        let existing_direct: HashMap<(PackageKind, String, String), bool> = self
            .packages
            .iter()
            .map(|package| {
                (
                    (
                        package.kind,
                        package.module.clone(),
                        package.cache_key.clone(),
                    ),
                    package.direct,
                )
            })
            .collect();

        let mut packages = Vec::with_capacity(self.skills.len() + self.plugins.len());
        for skill in &self.skills {
            let key = (
                PackageKind::Skill,
                skill.module.clone(),
                skill.cache_key.clone(),
            );
            packages.push(LockPackage {
                module: skill.module.clone(),
                direct: existing_direct.get(&key).copied().unwrap_or(true),
                kind: PackageKind::Skill,
                url: skill.url.clone(),
                owner: skill.owner.clone(),
                repo: skill.repo.clone(),
                path: skill.path.clone(),
                commit: skill.commit.clone(),
                cache_key: skill.cache_key.clone(),
                name: String::new(),
            });
        }
        for plugin in &self.plugins {
            let key = (
                PackageKind::Plugin,
                plugin.module.clone(),
                plugin.cache_key.clone(),
            );
            packages.push(LockPackage {
                module: plugin.module.clone(),
                direct: existing_direct.get(&key).copied().unwrap_or(true),
                kind: PackageKind::Plugin,
                url: plugin.url.clone(),
                owner: plugin.owner.clone(),
                repo: plugin.repo.clone(),
                path: plugin.path.clone(),
                commit: plugin.commit.clone(),
                cache_key: plugin.cache_key.clone(),
                name: plugin.name.clone(),
            });
        }
        packages.sort_by(|a, b| a.module.cmp(&b.module));
        self.packages = packages;
    }

    pub fn save(&self, project_root: &Path) -> Result<()> {
        let mut snapshot = self.clone();
        snapshot.sync_packages_from_views();
        snapshot.sync_views_from_packages();
        let p = lock_path(project_root);
        let raw = toml::to_string_pretty(&snapshot)
            .map_err(|e| AgentpackError::LockfileParse(e.to_string()))?;
        fs::write(&p, raw).map_err(|e| AgentpackError::io(&p, e))?;
        Ok(())
    }
}

pub fn init_lockfile(
    project_root: &Path,
    name: Option<String>,
    version: Option<String>,
) -> Result<()> {
    let p = lock_path(project_root);
    if p.exists() {
        return Err(AgentpackError::PackLockExists(p));
    }
    let dirname = project_root
        .file_name()
        .and_then(|n| n.to_str())
        .unwrap_or("project");
    let lock = PackLock {
        lockfile_version: 2,
        meta: Meta {
            name: name.unwrap_or_else(|| dirname.to_string()),
            version: version.unwrap_or_else(|| "0.0.1".to_string()),
        },
        ..Default::default()
    };
    crate::paths::ensure_user_agentpack_layout()?;
    lock.save(project_root)?;
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;
    use tempfile::tempdir;

    #[test]
    fn fresh_lock_omits_empty_sections() {
        let p = PackLock {
            meta: Meta {
                name: "agentpack".into(),
                version: "0.0.1".into(),
            },
            ..Default::default()
        };
        let raw = toml::to_string_pretty(&p).unwrap();
        assert!(!raw.contains("skills = []"));
        assert!(!raw.contains("plugins = []"));
        assert!(!raw.contains("[config]"));
        let q: PackLock = toml::from_str(&raw).unwrap();
        assert!(q.skills.is_empty());
        assert!(q.plugins.is_empty());
        assert!(q.config.disabled_plugins.is_empty());
    }

    #[test]
    fn legacy_lock_sections_are_rejected() {
        let dir = tempdir().unwrap();
        let root = dir.path();
        let raw = r#"lockfile-version = 2
[meta]
name = "p"
version = "0.1.0"

[[plugins]]
module = ""
url = "https://example.com"
owner = "o"
repo = "r"
path = ""
commit = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
cache_key = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
        "#;
        fs::write(lock_path(root), raw).unwrap();
        let error = PackLock::load(root).unwrap_err();
        assert!(matches!(error, AgentpackError::LockfileParse(_)));
    }

    #[test]
    fn roundtrip_skill() {
        let dir = tempdir().unwrap();
        let root = dir.path();
        let mut p = PackLock {
            meta: Meta {
                name: "proj".into(),
                version: "0.1.0".into(),
            },
            ..Default::default()
        };
        p.skills.push(LockSkill {
            module: "".into(),
            url: "u".into(),
            owner: "o".into(),
            repo: "r".into(),
            path: "p".into(),
            commit: "a".repeat(40),
            cache_key: "ab".repeat(32),
        });
        p.save(root).unwrap();
        let q = PackLock::load(root).unwrap();
        assert_eq!(q.skills.len(), 1);
        assert_eq!(q.skills[0].cache_key, p.skills[0].cache_key);
        assert_eq!(q.packages.len(), 1);
    }
}
