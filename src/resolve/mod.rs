//! Resolve **`agentpack.toml`** into a **`pack.lock`** (v2) via nested-manifest BFS, constraint merges, and GitHub materialization.

mod constraints;
pub(crate) mod module_id;

use std::collections::{BTreeMap, HashSet, VecDeque};

use reqwest::blocking::Client;

use std::path::Path;

use url::Url;

use crate::cache::{
    cache_entry_dir, classify_materialized, copy_package_dir_to_cache, materialize_github_tree,
};
use crate::error::{AgentpackError, Result};
use crate::github::{canonical_github_tree_url, github_source_from_segments};
use crate::lockfile::{LockPackage, Meta, PackLock};
use crate::manifest::{AgentpackManifest, DepSpecToml};
use crate::ui::Ui;

use module_id::{split_module_at_ref, ModuleId};

use constraints::{from_dep, ModuleConstraints};

/// Only **exact commit** requirements from merged constraints can invalidate an already-pinned
/// transitive package. Floating (HEAD / branch / tag / semver) constraints keep the existing
/// lock commit until **`--update`** / **`--update-lock`** forces a refresh.
fn check_transitive_pin_conflict(
    child: &ModuleId,
    pkg: &LockPackage,
    merged: &BTreeMap<ModuleId, ModuleConstraints>,
) -> Result<()> {
    let cmc = merged.get(child).unwrap();
    if let Some(want) = &cmc.exact {
        if want != &pkg.commit {
            return Err(AgentpackError::Cache(format!(
                "transitive dependency `{}` must be at commit {want}, but is already pinned at {}",
                child.as_str(),
                pkg.commit
            )));
        }
    }
    Ok(())
}

/// Options for [`resolve_lock_from_manifest`].
#[derive(Debug, Clone, Copy)]
pub struct ResolveLockOpts<'a> {
    /// Existing lock (same project) used to **reuse commits** for non-exact manifest constraints.
    pub previous: Option<&'a PackLock>,
    /// When **`true`**, ignore [`ResolveLockOpts::previous`] commits and re-resolve floating pins
    /// against the remote (`HEAD`, branch, tag name, semver, etc.).
    pub refresh_floating: bool,
}

fn pick_effective_git_ref(
    mc: &ModuleConstraints,
    client: &Client,
    owner: &str,
    repo: &str,
    mid: &ModuleId,
    opts: &ResolveLockOpts<'_>,
) -> Result<String> {
    if let Some(c) = &mc.exact {
        return Ok(c.clone());
    }
    if !opts.refresh_floating {
        if let Some(prev) = opts.previous {
            if let Some(pkg) = prev.packages.iter().find(|p| p.module == mid.as_str()) {
                return Ok(pkg.commit.clone());
            }
        }
    }
    mc.pick_git_ref(client, owner, repo, opts.refresh_floating)
}

/// Regenerate **`pack.lock`** from **`agentpack.toml`** (transitive via nested manifests).
///
/// When **`opts.refresh_floating`** is **`false`** (default for **`sync`** / **`lock`**), commits
/// already recorded in **`opts.previous`** for each module id are reused instead of re-resolving
/// **`HEAD`**, branch tips, etc. Use **`refresh_floating: true`** (`agentpack lock --update` or
/// `sync --update-lock`) to advance floating pins.
pub fn resolve_lock_from_manifest(
    manifest: &AgentpackManifest,
    client: &Client,
    ui: &Ui,
    opts: &ResolveLockOpts<'_>,
    project_root: &Path,
) -> Result<PackLock> {
    if manifest.dependencies.is_empty() {
        let lock = PackLock {
            lockfile_version: 2,
            meta: Meta {
                name: manifest.name.clone(),
                version: manifest.version.clone(),
            },
            ..Default::default()
        };

        return Ok(lock);
    }

    // --- Phase 1: resolve path dependencies ---
    let mut path_packages: Vec<LockPackage> = Vec::new();
    let mut github_deps: Vec<(String, DepSpecToml)> = Vec::new();
    // Transitive GitHub deps discovered inside path packages.
    let mut transitive_from_path: Vec<(String, DepSpecToml)> = Vec::new();

    for (key, dep) in &manifest.dependencies {
        if let Some(rel_path) = dep.path_value() {
            let abs_path = project_root.join(rel_path);
            let canon = abs_path.canonicalize().map_err(|_| {
                AgentpackError::Cache(format!(
                    "path dependency '{key}' points to '{rel_path}' which does not exist"
                ))
            })?;
            if !canon.is_dir() {
                return Err(AgentpackError::Cache(format!(
                    "path dependency '{key}' at '{}' is not a directory",
                    canon.display()
                )));
            }
            let identity = format!("path:{}", canon.display());
            let (cache_key, commit, out) = copy_package_dir_to_cache(&canon, &identity)?;
            let file_url = Url::from_file_path(&canon)
                .map(|u| u.to_string())
                .map_err(|_| AgentpackError::Cache("invalid path for file URL".into()))?;
            let source = github_source_from_segments("path", key, "");
            let mut pkg = classify_materialized(&out, &file_url, &source, commit, cache_key)?;
            pkg.module = key.to_string();
            pkg.direct = true;
            path_packages.push(pkg);

            // Load nested deps from path package (transitive).
            if let Some(nested) = AgentpackManifest::load_nested_dependencies(&out)? {
                for (k, ndep) in nested {
                    if ndep.path_value().is_some() {
                        return Err(AgentpackError::Cache(format!(
                            "transitive path dependencies are not supported (found in path dep '{key}')"
                        )));
                    }
                    transitive_from_path.push((k, ndep));
                }
            }
        } else {
            github_deps.push((key.clone(), dep.clone()));
        }
    }

    // --- Phase 2: resolve GitHub dependencies ---
    let mut merged: BTreeMap<ModuleId, ModuleConstraints> = BTreeMap::new();
    let mut queue: VecDeque<ModuleId> = VecDeque::new();
    let mut queued: HashSet<ModuleId> = HashSet::new();
    let mut direct_ids: HashSet<ModuleId> = HashSet::new();

    for (key, dep) in &github_deps {
        let (base, key_ref) = split_module_at_ref(key);
        let mid = ModuleId::parse(base)?;
        let mc = from_dep(dep, key_ref)?;
        merged.entry(mid.clone()).or_default().merge(mc)?;
        if queued.insert(mid.clone()) {
            queue.push_back(mid.clone());
        }
        direct_ids.insert(mid);
    }

    // Seed transitive GitHub deps from path packages.
    for (k, dep) in &transitive_from_path {
        let (base, key_ref) = split_module_at_ref(k);
        let mid = ModuleId::parse(base)?;
        let mc = from_dep(dep, key_ref)?;
        merged.entry(mid.clone()).or_default().merge(mc)?;
        if queued.insert(mid.clone()) {
            queue.push_back(mid);
        }
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
        let git_ref = pick_effective_git_ref(&mc, client, &owner, &repo, &mid, opts)?;
        let source = mid.to_github_source(&git_ref);
        let display = canonical_github_tree_url(&source);
        let mut pkg =
            materialize_github_tree(client, &source, &display, ui, opts.refresh_floating)?;
        pkg.module = mid.as_str().to_string();
        pkg.direct = direct_ids.contains(&mid);
        let pkg_cache_key = pkg.cache_key.clone();
        resolved.insert(mid.clone(), pkg);

        if let Some(deps) =
            AgentpackManifest::load_nested_dependencies(&cache_entry_dir(&pkg_cache_key)?)?
        {
            for (k, dep) in deps {
                let (base, key_ref) = split_module_at_ref(&k);
                let child = ModuleId::parse(base)?;
                let cnew = from_dep(&dep, key_ref)?;
                merged
                    .entry(child.clone())
                    .or_default()
                    .merge(cnew.clone())?;

                if let Some(pkg) = resolved.get(&child) {
                    check_transitive_pin_conflict(&child, pkg, &merged)?;
                    continue;
                }

                if queued.insert(child.clone()) {
                    queue.push_back(child);
                }
            }
        }
    }

    // --- Phase 3: combine ---
    let mut packages: Vec<LockPackage> = resolved.into_values().collect();
    packages.extend(path_packages);
    packages.sort_by(|a, b| a.module.cmp(&b.module));

    let lock = PackLock {
        lockfile_version: 2,
        meta: Meta {
            name: manifest.name.clone(),
            version: manifest.version.clone(),
        },
        packages,
        ..Default::default()
    };
    Ok(lock)
}

#[cfg(test)]
mod effective_ref_tests {
    use reqwest::blocking::Client;

    use crate::lockfile::PackageKind;

    use super::*;

    #[test]
    fn reuses_previous_commit_when_not_refreshing() {
        let mid = ModuleId::parse("github.com/foo/bar/sub").unwrap();
        let sha = "0123456789abcdef0123456789abcdef01234567";
        let mut prev = PackLock::default();
        prev.packages.push(LockPackage {
            module: mid.as_str().to_string(),
            direct: true,
            kind: PackageKind::Plugin,
            url: "https://github.com/foo/bar/tree/x/sub".into(),
            owner: "foo".into(),
            repo: "bar".into(),
            path: "sub".into(),
            commit: sha.into(),
            cache_key: "k".repeat(64),
            name: "".into(),
        });
        let mc = ModuleConstraints {
            latest: true,
            ..Default::default()
        };
        let opts = ResolveLockOpts {
            previous: Some(&prev),
            refresh_floating: false,
        };
        let c = Client::new();
        let got = pick_effective_git_ref(&mc, &c, "foo", "bar", &mid, &opts).expect("reuse");
        assert_eq!(got, sha);
    }

    #[test]
    fn manifest_exact_commit_overrides_previous_lock() {
        let mid = ModuleId::parse("github.com/foo/bar/sub").unwrap();
        let old_sha = "0123456789abcdef0123456789abcdef01234567";
        let new_sha = "ffffffffffffffffffffffffffffffffffffffff";
        let mut prev = PackLock::default();
        prev.packages.push(LockPackage {
            module: mid.as_str().to_string(),
            direct: true,
            kind: PackageKind::Plugin,
            url: "u".into(),
            owner: "foo".into(),
            repo: "bar".into(),
            path: "sub".into(),
            commit: old_sha.into(),
            cache_key: "k".repeat(64),
            name: "".into(),
        });
        let mc = ModuleConstraints {
            exact: Some(new_sha.into()),
            ..Default::default()
        };
        let opts = ResolveLockOpts {
            previous: Some(&prev),
            refresh_floating: false,
        };
        let c = Client::new();
        let got = pick_effective_git_ref(&mc, &c, "foo", "bar", &mid, &opts).expect("exact");
        assert_eq!(got, new_sha);
    }

    #[test]
    fn refresh_floating_skips_previous_lock() {
        let mid = ModuleId::parse("github.com/foo/bar/sub").unwrap();
        let mut prev = PackLock::default();
        prev.packages.push(LockPackage {
            module: mid.as_str().to_string(),
            direct: true,
            kind: PackageKind::Plugin,
            url: "u".into(),
            owner: "foo".into(),
            repo: "bar".into(),
            path: "sub".into(),
            commit: "0123456789abcdef0123456789abcdef01234567".into(),
            cache_key: "k".repeat(64),
            name: "".into(),
        });
        let mc = ModuleConstraints {
            latest: true,
            ..Default::default()
        };
        let opts = ResolveLockOpts {
            previous: Some(&prev),
            refresh_floating: true,
        };
        let c = Client::new();
        let got = pick_effective_git_ref(&mc, &c, "foo", "bar", &mid, &opts).unwrap();
        assert_eq!(got, "HEAD");
    }
}
