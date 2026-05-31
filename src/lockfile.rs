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

impl PackageKind {
    pub fn as_str(self) -> &'static str {
        match self {
            Self::Skill => "skill",
            Self::Plugin => "plugin",
        }
    }
}

impl fmt::Display for PackageKind {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.write_str(self.as_str())
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
    /// True when a plugin row is only partially populated and still needs network resolution.
    pub fn needs_backfill(&self) -> bool {
        self.kind == PackageKind::Plugin
            && !self.url.is_empty()
            && (self.cache_key.is_empty()
                || self.commit.is_empty()
                || self.owner.is_empty()
                || self.repo.is_empty())
    }

    pub fn kind_label(&self) -> &'static str {
        self.kind.as_str()
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

impl PackLock {
    pub fn load_from_path(p: &Path) -> Result<Self> {
        let raw = fs::read_to_string(p).map_err(|e| AgentpackError::io(p, e))?;
        let lock: PackLock =
            toml::from_str(&raw).map_err(|e| AgentpackError::LockfileParse(e.to_string()))?;
        if lock.lockfile_version != 2 {
            return Err(AgentpackError::LockfileParse(format!(
                "unsupported lockfile-version {} (expected 2); run `agentpack lock` to regenerate {}",
                lock.lockfile_version,
                p.display()
            )));
        }
        Ok(lock)
    }

    pub fn load(project_root: &Path) -> Result<Self> {
        Self::load_from_path(&lock_path(project_root))
    }

    /// Iterate over plugin packages.
    pub fn plugins(&self) -> impl Iterator<Item = &LockPackage> {
        self.packages
            .iter()
            .filter(|p| p.kind == PackageKind::Plugin)
    }

    /// Iterate over skill packages.
    pub fn skills(&self) -> impl Iterator<Item = &LockPackage> {
        self.packages
            .iter()
            .filter(|p| p.kind == PackageKind::Skill)
    }

    /// Mutable iterate over plugin packages.
    pub fn plugins_mut(&mut self) -> impl Iterator<Item = &mut LockPackage> {
        self.packages
            .iter_mut()
            .filter(|p| p.kind == PackageKind::Plugin)
    }

    pub fn plugin_count(&self) -> usize {
        self.plugins().count()
    }

    pub fn skill_count(&self) -> usize {
        self.skills().count()
    }

    pub fn save(&self, project_root: &Path) -> Result<()> {
        let mut snapshot = self.clone();
        snapshot.packages.sort_by(|a, b| a.module.cmp(&b.module));
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
        assert!(!raw.contains("[config]"));
        let q: PackLock = toml::from_str(&raw).unwrap();
        assert_eq!(q.skill_count(), 0);
        assert_eq!(q.plugin_count(), 0);
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
    fn rejects_unsupported_lockfile_version() {
        let dir = tempdir().unwrap();
        let root = dir.path();
        fs::write(
            lock_path(root),
            "lockfile_version = 1\n[meta]\nname = \"x\"\nversion = \"0\"\n",
        )
        .unwrap();
        let error = PackLock::load(root).unwrap_err();
        assert!(
            matches!(error, AgentpackError::LockfileParse(m) if m.contains("lockfile-version"))
        );
    }

    #[test]
    fn roundtrip_skill() {
        let dir = tempdir().unwrap();
        let root = dir.path();
        let mut p = PackLock {
            lockfile_version: 2,
            meta: Meta {
                name: "proj".into(),
                version: "0.1.0".into(),
            },
            ..Default::default()
        };
        p.packages.push(LockPackage {
            module: "".into(),
            direct: true,
            kind: PackageKind::Skill,
            url: "u".into(),
            owner: "o".into(),
            repo: "r".into(),
            path: "p".into(),
            commit: "a".repeat(40),
            cache_key: "ab".repeat(32),
            name: String::new(),
        });
        p.save(root).unwrap();
        let q = PackLock::load(root).unwrap();
        assert_eq!(q.skill_count(), 1);
        let skill = q.skills().next().unwrap();
        assert_eq!(skill.cache_key, p.packages[0].cache_key);
        assert_eq!(q.packages.len(), 1);
    }
}
