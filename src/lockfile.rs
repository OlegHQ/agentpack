use std::fs;
use std::path::Path;

use serde::{Deserialize, Serialize};
use toml;

use crate::error::{AgentpackError, Result};
use crate::paths::lock_path;

fn is_default_config(c: &Config) -> bool {
    c.disabled_plugins.is_empty()
}

fn is_zero(v: &u32) -> bool {
    *v == 0
}

#[derive(Debug, Clone, Default, Serialize, Deserialize)]
pub struct PackLock {
    /// `2` = canonical `[[packages]]` lockfile; `0` = legacy `skills` / `plugins` only.
    #[serde(default, skip_serializing_if = "is_zero")]
    pub lockfile_version: u32,
    pub meta: Meta,
    #[serde(default, skip_serializing_if = "is_default_config")]
    pub config: Config,
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub packages: Vec<LockPackage>,
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub skills: Vec<LockSkill>,
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub plugins: Vec<LockPlugin>,
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub agents: Vec<LockAgent>,
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub rules: Vec<LockRule>,
}

/// Single locked package (v2 lockfile). Kind is **`skill`** or **`plugin`**.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct LockPackage {
    pub module: String,
    #[serde(default)]
    pub direct: bool,
    pub kind: String,
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

    pub fn needs_backfill(&self) -> bool {
        self.kind == "plugin"
            && !self.url.is_empty()
            && (self.cache_key.is_empty()
                || self.commit.is_empty()
                || self.owner.is_empty()
                || self.repo.is_empty())
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

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct LockSkill {
    #[serde(default)]
    pub module: String,
    pub url: String,
    pub owner: String,
    pub repo: String,
    #[serde(default)]
    pub path: String,
    pub commit: String,
    pub cache_key: String,
}

/// GitHub-pinned Claude plugin root (directory containing `.claude-plugin/plugin.json`).
/// Legacy rows may only set `name` + `url`; `sync` backfills `owner`/`repo`/…/`cache_key`.
#[derive(Debug, Clone, Default, Serialize, Deserialize)]
pub struct LockPlugin {
    #[serde(default)]
    pub module: String,
    #[serde(default)]
    pub name: String,
    #[serde(default)]
    pub url: String,
    #[serde(default)]
    pub owner: String,
    #[serde(default)]
    pub repo: String,
    #[serde(default)]
    pub path: String,
    #[serde(default)]
    pub commit: String,
    #[serde(default)]
    pub cache_key: String,
}

impl LockPlugin {
    /// True when lockfile row still needs network resolution (e.g. old `[[plugins]]` with only `url`).
    pub fn needs_backfill(&self) -> bool {
        !self.url.is_empty()
            && (self.cache_key.is_empty()
                || self.commit.is_empty()
                || self.owner.is_empty()
                || self.repo.is_empty())
    }
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct LockAgent {
    pub name: String,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct LockRule {
    pub name: String,
}

impl PackLock {
    pub fn load_from_path(p: &Path) -> Result<Self> {
        let raw = fs::read_to_string(p).map_err(|e| AgentpackError::io(p, e))?;
        let mut lock: PackLock =
            toml::from_str(&raw).map_err(|e| AgentpackError::LockfileParse(e.to_string()))?;
        lock.hydrate_slices_from_packages();
        Ok(lock)
    }

    pub fn load(project_root: &Path) -> Result<Self> {
        Self::load_from_path(&lock_path(project_root))
    }

    /// When **`[[packages]]`** is present, it is the source of truth: refill **`skills`** / **`plugins`**.
    ///
    /// If **`packages`** is empty (e.g. v2 **`lockfile-version`** with only legacy **`[[plugins]]`** rows),
    /// keep the vectors produced by serde.
    pub fn hydrate_slices_from_packages(&mut self) {
        if self.packages.is_empty() {
            return;
        }
        self.skills.clear();
        self.plugins.clear();
        let mut pkgs = self.packages.clone();
        pkgs.sort_by(|a, b| a.module.cmp(&b.module));
        for p in pkgs {
            if p.kind == "plugin" {
                self.plugins.push(p.to_lock_plugin());
            } else {
                self.skills.push(p.to_lock_skill());
            }
        }
    }

    /// Build v2 **`packages`** from legacy rows (for `migrate`).
    pub fn packages_from_legacy(&self) -> Vec<LockPackage> {
        let mut out = Vec::new();
        for s in &self.skills {
            let module = if !s.module.is_empty() {
                s.module.clone()
            } else {
                legacy_module_id(&s.owner, &s.repo, &s.path)
            };
            out.push(LockPackage {
                module,
                direct: true,
                kind: "skill".into(),
                url: s.url.clone(),
                owner: s.owner.clone(),
                repo: s.repo.clone(),
                path: s.path.clone(),
                commit: s.commit.clone(),
                cache_key: s.cache_key.clone(),
                name: String::new(),
            });
        }
        for p in &self.plugins {
            let module = if !p.module.is_empty() {
                p.module.clone()
            } else {
                legacy_module_id(&p.owner, &p.repo, &p.path)
            };
            out.push(LockPackage {
                module,
                direct: true,
                kind: "plugin".into(),
                url: p.url.clone(),
                owner: p.owner.clone(),
                repo: p.repo.clone(),
                path: p.path.clone(),
                commit: p.commit.clone(),
                cache_key: p.cache_key.clone(),
                name: p.name.clone(),
            });
        }
        out.sort_by(|a, b| a.module.cmp(&b.module));
        out
    }

    pub fn save(&self, project_root: &Path) -> Result<()> {
        let p = lock_path(project_root);
        let raw = toml::to_string_pretty(self)
            .map_err(|e| AgentpackError::LockfileParse(e.to_string()))?;
        fs::write(&p, raw).map_err(|e| AgentpackError::io(&p, e))?;
        Ok(())
    }
}

fn legacy_module_id(owner: &str, repo: &str, path: &str) -> String {
    let path = path.trim_matches('/');
    if path.is_empty() {
        format!(
            "github.com/{}/{}",
            owner.to_lowercase(),
            repo.to_lowercase()
        )
    } else {
        format!(
            "github.com/{}/{}/{}",
            owner.to_lowercase(),
            repo.to_lowercase(),
            path
        )
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
        assert!(!raw.contains("agents = []"));
        assert!(!raw.contains("rules = []"));
        assert!(!raw.contains("[config]"));
        let q: PackLock = toml::from_str(&raw).unwrap();
        assert!(q.skills.is_empty());
        assert!(q.plugins.is_empty());
        assert!(q.config.disabled_plugins.is_empty());
    }

    #[test]
    fn v2_lock_with_empty_packages_keeps_legacy_plugins_from_toml() {
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
        let lock = PackLock::load(root).unwrap();
        assert!(lock.packages.is_empty());
        assert_eq!(lock.plugins.len(), 1);
        assert_eq!(lock.skills.len(), 0);
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
    }
}
