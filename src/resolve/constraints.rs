//! Constraint types and parsing for dependency resolution.

use reqwest::blocking::Client;
use semver::VersionReq;

use crate::error::{AgentpackError, Result};
use crate::github::{list_tags, DEFAULT_GIT_REF};
use crate::manifest::{DepSpecToml, DepTable};

#[derive(Debug, Clone, Default)]
pub(super) struct ModuleConstraints {
    pub exact: Option<String>,
    pub branch: Option<String>,
    pub tag: Option<String>,
    pub semver_reqs: Vec<VersionReq>,
    /// Floating default branch
    pub latest: bool,
}

impl ModuleConstraints {
    pub fn merge(&mut self, other: ModuleConstraints) -> Result<()> {
        if let Some(e2) = other.exact {
            match &self.exact {
                Some(e1) if e1 != &e2 => {
                    return Err(AgentpackError::Cache(format!(
                        "conflicting commit pins for the same module: {e1} vs {e2}"
                    )));
                }
                None => self.exact = Some(e2),
                _ => {}
            }
        }
        if let Some(t2) = other.tag {
            match &self.tag {
                Some(t1) if t1 != &t2 => {
                    return Err(AgentpackError::Cache(format!(
                        "conflicting tags for the same module: {t1} vs {t2}"
                    )));
                }
                None => self.tag = Some(t2),
                _ => {}
            }
        }
        if let Some(b2) = other.branch {
            match &self.branch {
                Some(b1) if b1 != &b2 => {
                    return Err(AgentpackError::Cache(format!(
                        "conflicting branches for the same module: {b1} vs {b2}"
                    )));
                }
                None => self.branch = Some(b2),
                _ => {}
            }
        }
        self.semver_reqs.extend(other.semver_reqs);
        if other.latest {
            self.latest = true;
        }
        Ok(())
    }

    pub fn pick_git_ref(
        &self,
        client: &Client,
        owner: &str,
        repo: &str,
        force_refresh: bool,
    ) -> Result<String> {
        if let Some(c) = &self.exact {
            return Ok(c.clone());
        }
        if !self.semver_reqs.is_empty() {
            let tags = list_tags(client, owner, repo, force_refresh)?;
            let mut candidates: Vec<(semver::Version, String)> = Vec::new();
            for (name, _sha) in tags {
                let vpart = name.strip_prefix('v').unwrap_or(&name);
                if let Ok(v) = semver::Version::parse(vpart) {
                    candidates.push((v, name));
                }
            }
            candidates.sort_by(|a, b| b.0.cmp(&a.0));
            for (v, name) in candidates {
                if self.semver_reqs.iter().all(|r| r.matches(&v)) {
                    return Ok(name);
                }
            }
            return Err(AgentpackError::Cache(format!(
                "no tag matching semver constraints {:?} for {owner}/{repo}",
                self.semver_reqs
            )));
        }
        if let Some(t) = &self.tag {
            return Ok(t.clone());
        }
        if let Some(b) = &self.branch {
            return Ok(b.clone());
        }
        if self.latest {
            return Ok(DEFAULT_GIT_REF.into());
        }
        Ok(DEFAULT_GIT_REF.into())
    }
}

pub(super) fn from_ref_str(r: Option<&str>) -> Result<ModuleConstraints> {
    let Some(s) = r.map(str::trim).filter(|s| !s.is_empty()) else {
        return Ok(ModuleConstraints {
            latest: true,
            ..Default::default()
        });
    };
    if s.len() == 40 && s.chars().all(|c| c.is_ascii_hexdigit()) {
        return Ok(ModuleConstraints {
            exact: Some(s.to_lowercase()),
            ..Default::default()
        });
    }
    if let Ok(req) = VersionReq::parse(s) {
        return Ok(ModuleConstraints {
            semver_reqs: vec![req],
            ..Default::default()
        });
    }
    Ok(ModuleConstraints {
        tag: Some(s.to_string()),
        ..Default::default()
    })
}

pub(super) fn from_table(t: &DepTable, key_ref: Option<&str>) -> Result<ModuleConstraints> {
    if t.path.is_some() {
        return Err(AgentpackError::Cache(
            "path dependencies should be resolved before constraint parsing".into(),
        ));
    }
    let mut c = ModuleConstraints::default();
    let mut n = 0u8;
    if t.commit.is_some() {
        n += 1;
    }
    if t.tag.is_some() {
        n += 1;
    }
    if t.branch.is_some() {
        n += 1;
    }
    if t.version.is_some() {
        n += 1;
    }
    if n > 1 {
        return Err(AgentpackError::Cache(
            "dependency table may only specify one of commit, tag, branch, version".into(),
        ));
    }
    if let Some(commit) = &t.commit {
        c.exact = Some(commit.to_lowercase());
    } else if let Some(tag) = &t.tag {
        c.tag = Some(tag.clone());
    } else if let Some(branch) = &t.branch {
        c.branch = Some(branch.clone());
    } else if let Some(ver) = &t.version {
        c.semver_reqs.push(
            VersionReq::parse(ver).map_err(|e| AgentpackError::Cache(format!("semver: {e}")))?,
        );
    } else if let Some(r) = key_ref {
        c = from_ref_str(Some(r))?;
    } else {
        c.latest = true;
    }
    Ok(c)
}

pub(super) fn from_dep(dep: &DepSpecToml, key_ref: Option<&str>) -> Result<ModuleConstraints> {
    match dep {
        DepSpecToml::Short(s) => {
            let s = s.trim();
            if s.is_empty() {
                from_table(&DepTable::default(), key_ref)
            } else {
                from_ref_str(Some(s))
            }
        }
        DepSpecToml::Table(t) => from_table(t, key_ref),
    }
}
