//! Resolve **`agentpack.toml`** into a **`pack.lock`** (v2) using PubGrub-ready constraint merges and GitHub materialization.

use std::collections::{BTreeMap, HashSet, VecDeque};

use reqwest::blocking::Client;
use semver::VersionReq;

use crate::cache::{cache_entry_dir, materialize_github_tree, FetchedGithubAsset};
use crate::error::{AgentpackError, Result};
use crate::github::{canonical_github_tree_url, list_tags, resolve_ref_to_sha};
use crate::lockfile::{LockPackage, Meta, PackLock};
use crate::manifest::{AgentpackManifest, DepSpecToml, DepTable};
use crate::module_id::{split_module_at_ref, ModuleId};
use crate::ui::Ui;

#[derive(Debug, Clone, Default)]
struct ModuleConstraints {
    exact: Option<String>,
    branch: Option<String>,
    tag: Option<String>,
    semver_reqs: Vec<VersionReq>,
    /// Floating default branch
    latest: bool,
}

impl ModuleConstraints {
    fn merge(&mut self, other: ModuleConstraints) -> Result<()> {
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

    fn pick_git_ref(&self, client: &Client, owner: &str, repo: &str) -> Result<String> {
        if let Some(c) = &self.exact {
            return Ok(c.clone());
        }
        if !self.semver_reqs.is_empty() {
            let tags = list_tags(client, owner, repo)?;
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
            return Ok("HEAD".into());
        }
        Ok("HEAD".into())
    }
}

fn constraints_from_ref_str(r: Option<&str>) -> Result<ModuleConstraints> {
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

fn constraints_from_table(t: &DepTable, key_ref: Option<&str>) -> Result<ModuleConstraints> {
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
        c = constraints_from_ref_str(Some(r))?;
    } else {
        c.latest = true;
    }
    Ok(c)
}

fn constraints_from_dep(dep: &DepSpecToml, key_ref: Option<&str>) -> Result<ModuleConstraints> {
    match dep {
        DepSpecToml::Short(s) => {
            let s = s.trim();
            if s.is_empty() {
                constraints_from_table(&DepTable::default(), key_ref)
            } else {
                constraints_from_ref_str(Some(s))
            }
        }
        DepSpecToml::Table(t) => constraints_from_table(t, key_ref),
    }
}

fn package_from_skill(s: &crate::lockfile::LockSkill, module: &str, direct: bool) -> LockPackage {
    LockPackage {
        module: module.to_string(),
        direct,
        kind: "skill".into(),
        url: s.url.clone(),
        owner: s.owner.clone(),
        repo: s.repo.clone(),
        path: s.path.clone(),
        commit: s.commit.clone(),
        cache_key: s.cache_key.clone(),
        name: String::new(),
    }
}

fn package_from_plugin(p: &crate::lockfile::LockPlugin, module: &str, direct: bool) -> LockPackage {
    LockPackage {
        module: module.to_string(),
        direct,
        kind: "plugin".into(),
        url: p.url.clone(),
        owner: p.owner.clone(),
        repo: p.repo.clone(),
        path: p.path.clone(),
        commit: p.commit.clone(),
        cache_key: p.cache_key.clone(),
        name: p.name.clone(),
    }
}

/// Regenerate **`pack.lock`** from **`agentpack.toml`** (transitive via nested manifests).
pub fn resolve_lock_from_manifest(
    manifest: &AgentpackManifest,
    client: &Client,
    ui: &Ui,
) -> Result<PackLock> {
    if manifest.dependencies.is_empty() {
        let mut lock = PackLock {
            lockfile_version: 2,
            meta: Meta {
                name: manifest.name.clone(),
                version: manifest.version.clone(),
            },
            ..Default::default()
        };
        lock.hydrate_slices_from_packages();
        return Ok(lock);
    }

    let mut merged: BTreeMap<ModuleId, ModuleConstraints> = BTreeMap::new();
    let mut queue: VecDeque<ModuleId> = VecDeque::new();
    let mut queued: HashSet<ModuleId> = HashSet::new();
    let mut direct_ids: HashSet<ModuleId> = HashSet::new();

    for (key, dep) in &manifest.dependencies {
        let (base, key_ref) = split_module_at_ref(key);
        let mid = ModuleId::parse(base)?;
        let mc = constraints_from_dep(dep, key_ref)?;
        merged.entry(mid.clone()).or_default().merge(mc)?;
        if queued.insert(mid.clone()) {
            queue.push_back(mid.clone());
        }
        direct_ids.insert(mid);
    }

    let mut resolved: BTreeMap<ModuleId, LockPackage> = BTreeMap::new();

    while let Some(mid) = queue.pop_front() {
        if resolved.contains_key(&mid) {
            continue;
        }
        let mc = merged
            .get(&mid)
            .ok_or_else(|| AgentpackError::Cache("internal: missing constraints".into()))?
            .clone();
        let (owner, repo, _in_path) = mid.owner_repo_path_parts();
        let git_ref = mc.pick_git_ref(client, &owner, &repo)?;
        let source = mid.to_github_source(&git_ref);
        let display = canonical_github_tree_url(&source);
        let fetched = materialize_github_tree(client, &source, &display, ui)?;
        let pkg = match &fetched {
            FetchedGithubAsset::Skill(s) => {
                package_from_skill(s, mid.as_str(), direct_ids.contains(&mid))
            }
            FetchedGithubAsset::Plugin(p) => {
                package_from_plugin(p, mid.as_str(), direct_ids.contains(&mid))
            }
        };
        resolved.insert(mid.clone(), pkg);

        if let Some(deps) =
            AgentpackManifest::load_nested_dependencies(&cache_entry_dir(match &fetched {
                FetchedGithubAsset::Skill(s) => &s.cache_key,
                FetchedGithubAsset::Plugin(p) => &p.cache_key,
            })?)?
        {
            for (k, dep) in deps {
                let (base, key_ref) = split_module_at_ref(&k);
                let child = ModuleId::parse(base)?;
                let cnew = constraints_from_dep(&dep, key_ref)?;
                merged
                    .entry(child.clone())
                    .or_default()
                    .merge(cnew.clone())?;

                if let Some(pkg) = resolved.get(&child) {
                    let (co, cr, _) = child.owner_repo_path_parts();
                    let cmc = merged.get(&child).unwrap().clone();
                    let want_ref = cmc.pick_git_ref(client, &co, &cr)?;
                    let want_sha = resolve_ref_to_sha(client, &co, &cr, &want_ref)?;
                    if want_sha != pkg.commit {
                        return Err(AgentpackError::Cache(format!(
                            "transitive dependency `{}` was already pinned at {}; merged requirements resolve to {} (ref {})",
                            child.as_str(),
                            pkg.commit,
                            want_sha,
                            want_ref
                        )));
                    }
                    continue;
                }

                if queued.insert(child.clone()) {
                    queue.push_back(child);
                }
            }
        }
    }

    let mut packages: Vec<LockPackage> = resolved.into_values().collect();
    packages.sort_by(|a, b| a.module.cmp(&b.module));

    let mut lock = PackLock {
        lockfile_version: 2,
        meta: Meta {
            name: manifest.name.clone(),
            version: manifest.version.clone(),
        },
        packages,
        ..Default::default()
    };
    lock.hydrate_slices_from_packages();
    Ok(lock)
}

#[cfg(test)]
mod pubgrub_smoke {
    use pubgrub::{resolve, OfflineDependencyProvider, Ranges};

    #[test]
    fn diamond_conflict_is_unsat() {
        type VS = Ranges<u32>;
        let mut p = OfflineDependencyProvider::<&str, VS>::new();
        p.add_dependencies("root", 1u32, [("a", Ranges::full()), ("b", Ranges::full())]);
        p.add_dependencies("a", 1u32, [("c", Ranges::singleton(1u32))]);
        p.add_dependencies("b", 1u32, [("c", Ranges::singleton(2u32))]);
        assert!(resolve(&p, "root", 1u32).is_err());
    }
}
