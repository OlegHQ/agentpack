use std::env;
use std::fs;
use std::path::{Path, PathBuf};

use chrono::Utc;
use reqwest::blocking::Client;
use url::Url;

use crate::cache::{
    backfill_plugin_lock_entry, blob_path_parent_prefixes, cache_entry_dir,
    cache_has_plugin_manifest, classify_materialized, claude_plugin_manifest_path,
    copy_package_dir_to_cache, cursor_plugin_manifest_path, ensure_lock_plugin_cached,
    ensure_lock_skill_cached, fetch_github_asset_from_url, materialize_github_tree,
    FetchedGithubAsset,
};
use crate::error::{AgentpackError, Result};
use crate::github::{
    canonical_github_tree_url, check_rate_limit_hint, github_source_from_segments,
    parse_github_url, path_in_repo_looks_like_file, GitHubSource,
};
use crate::index::{
    aliases_for_github_entry, get_entry, list_keys, lookup_alias, upsert_entry, CacheEntryRecord,
};
use crate::lockfile::{LockPlugin, LockSkill, PackLock};
use crate::manifest::AgentpackManifest;
use crate::module_id::{split_module_at_ref, ModuleId};
use crate::paths::{self};
use crate::resolve::resolve_lock_from_manifest;
use crate::staging::{self, skill_is_shadowed};
use crate::ui::Ui;

fn http_client() -> Result<Client> {
    Client::builder()
        .user_agent("agentpack/0.1 (https://github.com/)")
        .build()
        .map_err(|e| AgentpackError::GitHubApi(e.to_string()))
}

fn read_plugin_package_name(cache_root: &Path) -> Option<String> {
    for p in [
        claude_plugin_manifest_path(cache_root),
        cursor_plugin_manifest_path(cache_root),
    ] {
        if p.is_file() {
            if let Ok(s) = fs::read_to_string(&p) {
                if let Ok(v) = serde_json::from_str::<serde_json::Value>(&s) {
                    if let Some(n) = v.get("name").and_then(|x| x.as_str()) {
                        return Some(n.to_string());
                    }
                }
            }
        }
    }
    None
}

fn skill_alias_name(s: &LockSkill) -> String {
    if s.path.is_empty() {
        s.repo.clone()
    } else {
        Path::new(&s.path)
            .file_name()
            .map(|x| x.to_string_lossy().into_owned())
            .unwrap_or_else(|| s.repo.clone())
    }
}

fn record_and_aliases(fetched: &FetchedGithubAsset) -> Result<(CacheEntryRecord, Vec<String>)> {
    match fetched {
        FetchedGithubAsset::Skill(s) => {
            let rec = CacheEntryRecord {
                kind: "skill".into(),
                source_url: s.url.clone(),
                owner: s.owner.clone(),
                repo: s.repo.clone(),
                path: s.path.clone(),
                commit: s.commit.clone(),
                fetched_at_unix: Utc::now().timestamp(),
            };
            let name = skill_alias_name(s);
            let aliases = aliases_for_github_entry(&s.owner, &s.repo, &s.path, Some(&name));
            Ok((rec, aliases))
        }
        FetchedGithubAsset::Plugin(p) => {
            let cache_root = cache_entry_dir(&p.cache_key)?;
            let pkg = read_plugin_package_name(&cache_root);
            let rec = CacheEntryRecord {
                kind: "plugin".into(),
                source_url: p.url.clone(),
                owner: p.owner.clone(),
                repo: p.repo.clone(),
                path: p.path.clone(),
                commit: p.commit.clone(),
                fetched_at_unix: Utc::now().timestamp(),
            };
            let aliases = aliases_for_github_entry(&p.owner, &p.repo, &p.path, pkg.as_deref());
            Ok((rec, aliases))
        }
    }
}

fn merge_shorthand_alias(aliases: &mut Vec<String>, shorthand: Option<&str>) {
    if let Some(s) = shorthand {
        let t = s.trim().to_lowercase();
        if !t.is_empty() && !aliases.contains(&t) {
            aliases.push(t);
        }
    }
}

fn resolve_existing_path(spec: &str) -> Option<PathBuf> {
    let p = Path::new(spec);
    let candidate = if p.is_absolute() {
        p.to_path_buf()
    } else {
        env::current_dir().ok()?.join(p)
    };
    fs::canonicalize(&candidate).ok().filter(|c| c.is_dir())
}

fn add_from_filesystem(canon: &Path, _ui: &Ui) -> Result<FetchedGithubAsset> {
    let identity = format!("path:{}", canon.display());
    let (cache_key, commit, out) = copy_package_dir_to_cache(canon, &identity)?;
    let file_url = Url::from_file_path(canon)
        .map(|u| u.to_string())
        .map_err(|_| AgentpackError::Cache("invalid absolute path for file URL".into()))?;
    let base = canon.file_name().and_then(|s| s.to_str()).unwrap_or("pack");
    let gh = github_source_from_segments("path", base, "");
    classify_materialized(&out, &file_url, &gh, commit, cache_key)
}

fn add_from_local_mirror(
    spec: &str,
    owner: &str,
    repo: &str,
    in_repo_path: &str,
    _ui: &Ui,
) -> Result<FetchedGithubAsset> {
    let mirror = paths::local_mirror_path_from_shorthand(spec)?;
    if !mirror.is_dir() {
        return Err(AgentpackError::Cache(format!(
            "expected local mirror at {}",
            mirror.display()
        )));
    }
    let identity = format!("local:{spec}");
    let (cache_key, commit, out) = copy_package_dir_to_cache(&mirror, &identity)?;
    let local_url = format!("agentpack-local:{spec}");
    let gh = github_source_from_segments(owner, repo, in_repo_path);
    classify_materialized(&out, &local_url, &gh, commit, cache_key)
}

fn add_two_segment(
    client: &Client,
    owner: &str,
    repo: &str,
    ui: &Ui,
) -> Result<FetchedGithubAsset> {
    let spec = format!("{owner}/{repo}");
    let mirror = paths::local_mirror_path_from_shorthand(&spec)?;
    if mirror.is_dir() {
        return add_from_local_mirror(&spec, owner, repo, "", ui);
    }
    let source = github_source_from_segments(owner, repo, "");
    let display = canonical_github_tree_url(&source);
    materialize_github_tree(client, &source, &display, ui)
}

fn add_multi_segment(client: &Client, parts: &[&str], ui: &Ui) -> Result<FetchedGithubAsset> {
    let owner = parts[0];
    let repo = parts[1];
    let in_path = parts[2..].join("/");
    let spec = parts.join("/");
    let mirror = paths::local_mirror_path_from_shorthand(&spec)?;
    if mirror.is_dir() {
        return add_from_local_mirror(&spec, owner, repo, &in_path, ui);
    }
    let source = github_source_from_segments(owner, repo, &in_path);
    let display = canonical_github_tree_url(&source);
    materialize_github_tree(client, &source, &display, ui)
}

fn add_one_segment(client: &Client, name: &str, ui: &Ui) -> Result<FetchedGithubAsset> {
    let mirror = paths::local_registry_root()?.join(name);
    if mirror.is_dir() {
        let spec = name.to_string();
        return add_from_local_mirror(&spec, "local", name, "", ui);
    }
    if let Some(ck) = lookup_alias(name)? {
        if let Some(rec) = get_entry(&ck)? {
            return recreate_fetched_from_record(client, &rec, &ck, ui);
        }
    }
    Err(AgentpackError::Cache(format!(
        "unknown package {name}: not in local/ ({}) and not in cache index",
        paths::local_registry_root()?.join(name).display()
    )))
}

fn recreate_fetched_from_record(
    client: &Client,
    rec: &CacheEntryRecord,
    cache_key: &str,
    ui: &Ui,
) -> Result<FetchedGithubAsset> {
    let out = cache_entry_dir(cache_key)?;
    let mut has_skill = out.join("SKILL.md").is_file();
    let mut has_plug = cache_has_plugin_manifest(&out);
    if !has_skill && !has_plug {
        if rec.owner == "path"
            || rec.owner == "local"
            || rec.source_url.starts_with("file:")
            || rec.source_url.starts_with("agentpack-local:")
        {
            return Err(AgentpackError::Cache(format!(
                "cache for {cache_key} is empty and local/path sources are unavailable here"
            )));
        }
        if rec.kind == "plugin" {
            let plugin = LockPlugin {
                module: String::new(),
                name: String::new(),
                url: rec.source_url.clone(),
                owner: rec.owner.clone(),
                repo: rec.repo.clone(),
                path: rec.path.clone(),
                commit: rec.commit.clone(),
                cache_key: cache_key.to_string(),
            };
            ensure_lock_plugin_cached(client, &plugin, ui)?;
        } else {
            let skill = LockSkill {
                module: String::new(),
                url: rec.source_url.clone(),
                owner: rec.owner.clone(),
                repo: rec.repo.clone(),
                path: rec.path.clone(),
                commit: rec.commit.clone(),
                cache_key: cache_key.to_string(),
            };
            ensure_lock_skill_cached(client, &skill, ui)?;
        }
        has_skill = out.join("SKILL.md").is_file();
        has_plug = cache_has_plugin_manifest(&out);
        if !has_skill && !has_plug {
            return Err(AgentpackError::Cache(format!(
                "could not repopulate cache for {cache_key}"
            )));
        }
    }
    let src = GitHubSource {
        owner: rec.owner.clone(),
        repo: rec.repo.clone(),
        git_ref: "HEAD".into(),
        path: rec.path.clone(),
    };
    classify_materialized(
        &out,
        &rec.source_url,
        &src,
        rec.commit.clone(),
        cache_key.to_string(),
    )
}

fn resolve_add_spec(
    client: &Client,
    spec: &str,
    ui: &Ui,
) -> Result<(FetchedGithubAsset, Option<String>)> {
    let spec = spec.trim();
    if spec.is_empty() {
        return Err(AgentpackError::Cache("empty add spec".into()));
    }
    if spec.starts_with("http://") || spec.starts_with("https://") {
        if parse_github_url(spec).is_err() {
            return Err(AgentpackError::GitHubUrl(
                "only https://github.com/… URLs are supported".into(),
            ));
        }
        let f = fetch_github_asset_from_url(client, spec, ui)?;
        return Ok((f, None));
    }
    if let Some(canon) = resolve_existing_path(spec) {
        let f = add_from_filesystem(&canon, ui)?;
        return Ok((f, None));
    }
    let parts: Vec<&str> = spec.split('/').filter(|s| !s.is_empty()).collect();
    match parts.len() {
        0 => Err(AgentpackError::Cache("invalid add spec".into())),
        1 => Ok((add_one_segment(client, parts[0], ui)?, None)),
        2 => {
            let sh = spec.to_string();
            let f = add_two_segment(client, parts[0], parts[1], ui)?;
            Ok((f, Some(sh)))
        }
        _ => {
            let sh = spec.to_string();
            let f = add_multi_segment(client, &parts, ui)?;
            Ok((f, Some(sh)))
        }
    }
}

fn upsert_fetched_index(fetched: &FetchedGithubAsset, shorthand_alias: Option<&str>) -> Result<()> {
    let (rec, mut aliases) = record_and_aliases(fetched)?;
    merge_shorthand_alias(&mut aliases, shorthand_alias);
    let ck = match fetched {
        FetchedGithubAsset::Skill(s) => &s.cache_key,
        FetchedGithubAsset::Plugin(p) => &p.cache_key,
    };
    upsert_entry(ck, &rec, &aliases)
}

pub fn run_sync(project_root: &Path, dry_run: bool, verify_only: bool, ui: &Ui) -> Result<()> {
    paths::ensure_user_agentpack_layout()?;
    let client = http_client()?;
    check_rate_limit_hint(&client);

    let manifest = AgentpackManifest::load(project_root)?;
    if !dry_run {
        if let Some(ref m) = manifest {
            // Empty `[dependencies]` means the lock is authoritative (e.g. tests or hand-edited v2).
            if !m.dependencies.is_empty() {
                let resolved = resolve_lock_from_manifest(m, &client, ui)?;
                resolved.save(project_root)?;
            }
        }
    }

    let mut lock = PackLock::load(project_root)?;

    let shadowed_skills = lock
        .skills
        .iter()
        .filter(|s| skill_is_shadowed(s, &lock.plugins))
        .count();

    if dry_run {
        ui.message(format!(
            "Dry-run: would sync {} skill(s), {} plugin(s); {} skill(s) shadowed by plugins (omitted from staging); no changes made.",
            lock.skills.len(),
            lock.plugins.len(),
            shadowed_skills
        ));
        tracing::info!(
            skills = lock.skills.len(),
            plugins = lock.plugins.len(),
            shadowed_skills,
            "dry-run: would ensure cache and rebuild staging"
        );
        return Ok(());
    }

    let mut lock_dirty = false;
    for plugin in &mut lock.plugins {
        if plugin.url.is_empty() {
            tracing::warn!("skipping plugin row with empty url");
            continue;
        }
        if plugin.needs_backfill() {
            backfill_plugin_lock_entry(&client, plugin, ui)?;
            lock_dirty = true;
        }
    }
    if lock_dirty {
        lock.save(project_root)?;
    }

    let mut warns: Vec<String> = Vec::new();
    for plugin in &lock.plugins {
        if plugin.cache_key.is_empty() {
            tracing::warn!("skipping plugin sync: empty cache_key");
            continue;
        }
        if !ensure_lock_plugin_cached(&client, plugin, ui)? {
            warns.push(format!(
                "plugin {} ({}): cache missing and source unavailable — omitted from staging",
                plugin.cache_key.chars().take(12).collect::<String>(),
                plugin.url
            ));
        }
        let rec = CacheEntryRecord {
            kind: "plugin".into(),
            source_url: plugin.url.clone(),
            owner: plugin.owner.clone(),
            repo: plugin.repo.clone(),
            path: plugin.path.clone(),
            commit: plugin.commit.clone(),
            fetched_at_unix: Utc::now().timestamp(),
        };
        upsert_entry(&plugin.cache_key, &rec, &[])?;
    }

    for skill in &lock.skills {
        if !ensure_lock_skill_cached(&client, skill, ui)? {
            warns.push(format!(
                "skill {} ({}): cache missing and source unavailable — omitted from staging",
                skill.cache_key.chars().take(12).collect::<String>(),
                skill.url
            ));
        }
        let rec = CacheEntryRecord {
            kind: "skill".into(),
            source_url: skill.url.clone(),
            owner: skill.owner.clone(),
            repo: skill.repo.clone(),
            path: skill.path.clone(),
            commit: skill.commit.clone(),
            fetched_at_unix: Utc::now().timestamp(),
        };
        upsert_entry(&skill.cache_key, &rec, &[])?;
    }

    for w in &warns {
        if !ui.quiet {
            ui.message(format!("Warning: {w}"));
        }
        tracing::warn!(message = %w, "sync cache miss");
    }

    if shadowed_skills > 0 && !ui.quiet {
        ui.message(format!(
            "Note: {shadowed_skills} skill(s) are shadowed by full plugin(s) and will not get separate staging hubs."
        ));
    }

    if verify_only {
        let v = ui.spinner("Verify staging layout…");
        staging::verify_staging(project_root, &lock)?;
        Ui::finish_spinner(v.as_ref(), "Staging checks passed");
        return Ok(());
    }

    let st = ui.spinner("Rebuild plugin staging (symlinks)…");
    let oref = manifest.as_ref();
    staging::rebuild_staging(project_root, &lock, oref)?;
    staging::verify_staging(project_root, &lock)?;
    Ui::finish_spinner(st.as_ref(), "Staging ready");

    let kcount = list_keys()?.len();
    tracing::debug!(
        index_keys = kcount,
        skills = lock.skills.len(),
        plugins = lock.plugins.len(),
        "sync complete"
    );
    if !ui.quiet {
        ui.message(format!(
            "Sync finished — {} skill(s), {} plugin(s), {} cache index entr(ies). One merged bundle: agentpack-bundle.",
            lock.skills.len(),
            lock.plugins.len(),
            kcount
        ));
    }
    Ok(())
}

pub fn run_add(project_root: &Path, spec: &str, no_sync: bool, ui: &Ui) -> Result<()> {
    paths::ensure_user_agentpack_layout()?;
    ui.message(format!("Adding: {spec}"));
    if resolve_existing_path(spec.trim()).is_some() {
        return Err(AgentpackError::Cache(
            "filesystem package: add an entry under [dependencies] in agentpack.toml manually (file: pins are not auto-edited)"
                .into(),
        ));
    }
    let client = http_client()?;
    check_rate_limit_hint(&client);
    let _ = AgentpackManifest::load(project_root)?
        .ok_or_else(|| AgentpackError::ManifestMissing(paths::manifest_path(project_root)))?;
    let (fetched, shorthand) = resolve_add_spec(&client, spec, ui)?;
    let module_key = match &fetched {
        FetchedGithubAsset::Skill(s) => ModuleId::from_owner_repo_path(&s.owner, &s.repo, &s.path)
            .as_str()
            .to_string(),
        FetchedGithubAsset::Plugin(p) => ModuleId::from_owner_repo_path(&p.owner, &p.repo, &p.path)
            .as_str()
            .to_string(),
    };
    AgentpackManifest::append_dependency_key(project_root, &module_key)?;
    let manifest = AgentpackManifest::load(project_root)?
        .ok_or_else(|| AgentpackError::ManifestMissing(paths::manifest_path(project_root)))?;
    let lock = resolve_lock_from_manifest(&manifest, &client, ui)?;
    lock.save(project_root)?;
    upsert_fetched_index(&fetched, shorthand.as_deref())?;
    ui.message(format!(
        "Recorded {module_key} in agentpack.toml and refreshed pack.lock."
    ));

    if !no_sync {
        run_sync(project_root, false, false, ui)?;
    } else {
        ui.message("Skipping sync (--no-sync).");
    }
    Ok(())
}

fn module_key_candidates_from_github_source(src: &GitHubSource) -> Vec<String> {
    let owner = src.owner.to_lowercase();
    let repo = src.repo.to_lowercase();
    if path_in_repo_looks_like_file(&src.path) {
        blob_path_parent_prefixes(&src.path)
            .into_iter()
            .map(|p| {
                ModuleId::from_owner_repo_path(&owner, &repo, &p)
                    .as_str()
                    .to_string()
            })
            .collect()
    } else {
        vec![ModuleId::from_owner_repo_path(&owner, &repo, &src.path)
            .as_str()
            .to_string()]
    }
}

fn resolve_remove_spec_to_key(spec: &str, manifest: &AgentpackManifest) -> Result<String> {
    let spec = spec.trim();
    if spec.is_empty() {
        return Err(AgentpackError::Cache("empty remove spec".into()));
    }

    if spec.starts_with("http://") || spec.starts_with("https://") {
        let src = parse_github_url(spec)?;
        for k in module_key_candidates_from_github_source(&src) {
            if manifest.dependencies.contains_key(&k) {
                return Ok(k);
            }
        }
        return Err(AgentpackError::DependencyNotFound(spec.to_string()));
    }

    let parts: Vec<&str> = spec.split('/').filter(|s| !s.is_empty()).collect();
    if parts.len() == 1 {
        let tail = parts[0].to_lowercase();
        for k in manifest.dependencies.keys() {
            let kl = k.to_lowercase();
            if kl == tail || kl.ends_with(&format!("/{tail}")) {
                return Ok(k.clone());
            }
        }
    }

    let (base, _) = split_module_at_ref(spec);
    if parts.len() >= 2 && parts[0] != "github.com" {
        let owner = parts[0].to_lowercase();
        let repo = parts[1].to_lowercase();
        let path = parts[2..].join("/");
        let candidates: Vec<String> = if path_in_repo_looks_like_file(&path) {
            blob_path_parent_prefixes(&path)
                .into_iter()
                .map(|p| {
                    ModuleId::from_owner_repo_path(&owner, &repo, &p)
                        .as_str()
                        .to_string()
                })
                .collect()
        } else {
            vec![ModuleId::from_owner_repo_path(&owner, &repo, &path)
                .as_str()
                .to_string()]
        };
        for k in candidates {
            if manifest.dependencies.contains_key(&k) {
                return Ok(k);
            }
        }
        return Err(AgentpackError::DependencyNotFound(spec.to_string()));
    }

    let id = ModuleId::parse(base)?;
    let (owner, repo, path) = id.owner_repo_path_parts();
    let candidates: Vec<String> = if path_in_repo_looks_like_file(&path) {
        blob_path_parent_prefixes(&path)
            .into_iter()
            .map(|p| {
                ModuleId::from_owner_repo_path(&owner, &repo, &p)
                    .as_str()
                    .to_string()
            })
            .collect()
    } else {
        vec![id.as_str().to_string()]
    };
    for k in candidates {
        if manifest.dependencies.contains_key(&k) {
            return Ok(k);
        }
    }

    Err(AgentpackError::DependencyNotFound(spec.to_string()))
}

pub fn run_remove(project_root: &Path, spec: &str, no_sync: bool, ui: &Ui) -> Result<()> {
    paths::ensure_user_agentpack_layout()?;
    let manifest = AgentpackManifest::load(project_root)?
        .ok_or_else(|| AgentpackError::ManifestMissing(paths::manifest_path(project_root)))?;
    let key = resolve_remove_spec_to_key(spec, &manifest)?;
    AgentpackManifest::remove_dependency_entry(project_root, &key)?;
    let manifest = AgentpackManifest::load(project_root)?
        .ok_or_else(|| AgentpackError::ManifestMissing(paths::manifest_path(project_root)))?;
    let client = http_client()?;
    check_rate_limit_hint(&client);
    let lock = resolve_lock_from_manifest(&manifest, &client, ui)?;
    lock.save(project_root)?;
    if !ui.quiet {
        ui.message(format!(
            "Removed {} from {} and refreshed {}.",
            key,
            paths::manifest_path(project_root).display(),
            paths::lock_path(project_root).display()
        ));
    }
    if !no_sync {
        run_sync(project_root, false, false, ui)?;
    } else {
        ui.message("Skipping sync (--no-sync).");
    }
    Ok(())
}

pub fn run_lock(project_root: &Path, ui: &Ui) -> Result<()> {
    paths::ensure_user_agentpack_layout()?;
    let m = AgentpackManifest::load(project_root)?
        .ok_or_else(|| AgentpackError::ManifestMissing(paths::manifest_path(project_root)))?;
    let client = http_client()?;
    check_rate_limit_hint(&client);
    let lock = resolve_lock_from_manifest(&m, &client, ui)?;
    lock.save(project_root)?;
    if !ui.quiet {
        ui.message(format!(
            "Wrote {} ({} package(s)).",
            paths::lock_path(project_root).display(),
            lock.packages.len()
        ));
    }
    Ok(())
}

pub fn run_migrate(project_root: &Path, ui: &Ui) -> Result<()> {
    let mp = paths::manifest_path(project_root);
    if mp.is_file() {
        return Err(AgentpackError::Cache(format!(
            "{} already exists; nothing to migrate",
            mp.display()
        )));
    }
    let mut lock = PackLock::load(project_root)?;
    if lock.lockfile_version >= 2 && !lock.packages.is_empty() {
        return Err(AgentpackError::Cache(
            "pack.lock is already v2 with packages".into(),
        ));
    }
    if lock.skills.is_empty() && lock.plugins.is_empty() {
        return Err(AgentpackError::Cache(
            "legacy pack.lock has no skills or plugins".into(),
        ));
    }
    let pkgs = lock.packages_from_legacy();
    let mut body = String::new();
    body.push_str("# Migrated from legacy pack.lock — review and run `agentpack lock`.\n\n");
    body.push_str(&format!("name = {:?}\n", lock.meta.name));
    body.push_str(&format!("version = {:?}\n\n", lock.meta.version));
    body.push_str("[dependencies]\n");
    for p in &pkgs {
        body.push_str(&format!("{:?} = {{ commit = {:?} }}\n", p.module, p.commit));
    }
    std::fs::write(&mp, &body).map_err(|e| AgentpackError::io(&mp, e))?;

    lock.lockfile_version = 2;
    lock.packages = pkgs;
    lock.skills.clear();
    lock.plugins.clear();
    lock.agents.clear();
    lock.rules.clear();
    lock.hydrate_slices_from_packages();
    lock.save(project_root)?;
    if !ui.quiet {
        ui.message(format!(
            "Wrote {} and converted {}",
            mp.display(),
            paths::lock_path(project_root).display()
        ));
    }
    Ok(())
}

/// Used by binary `claude` to resolve project root then sync + exec.
pub fn sync_for_launch(project_root: &Path, ui: &Ui) -> Result<()> {
    run_sync(project_root, false, false, ui)
}

#[cfg(test)]
mod remove_spec_tests {
    use std::collections::BTreeMap;

    use super::*;
    use crate::manifest::DepSpecToml;

    fn man_with(dep_keys: &[&str]) -> AgentpackManifest {
        let mut deps = BTreeMap::new();
        for k in dep_keys {
            deps.insert(k.to_string(), DepSpecToml::Short(String::new()));
        }
        AgentpackManifest {
            name: "t".into(),
            version: "1".into(),
            description: String::new(),
            dependencies: deps,
            overrides: BTreeMap::new(),
        }
    }

    #[test]
    fn remove_key_from_owner_repo_shorthand() {
        let m =
            man_with(&["github.com/anthropics/claude-plugins-official/plugins/code-simplifier"]);
        let k = resolve_remove_spec_to_key(
            "anthropics/claude-plugins-official/plugins/code-simplifier",
            &m,
        )
        .unwrap();
        assert_eq!(
            k,
            "github.com/anthropics/claude-plugins-official/plugins/code-simplifier"
        );
    }

    #[test]
    fn remove_key_from_blob_file_url() {
        let m =
            man_with(&["github.com/anthropics/claude-plugins-official/plugins/code-simplifier"]);
        let k = resolve_remove_spec_to_key(
            "https://github.com/anthropics/claude-plugins-official/blob/main/plugins/code-simplifier/agents/code-simplifier.md",
            &m,
        )
        .unwrap();
        assert_eq!(
            k,
            "github.com/anthropics/claude-plugins-official/plugins/code-simplifier"
        );
    }
}
