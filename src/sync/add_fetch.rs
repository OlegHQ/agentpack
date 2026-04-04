use std::env;
use std::fs;
use std::path::{Path, PathBuf};

use chrono::Utc;
use reqwest::blocking::Client;
use url::Url;

use crate::cache::{
    cache_entry_dir, cache_has_plugin_manifest, classify_materialized, claude_plugin_manifest_path,
    copy_package_dir_to_cache, cursor_plugin_manifest_path, ensure_lock_plugin_cached,
    ensure_lock_skill_cached, fetch_github_asset_from_url, materialize_github_tree,
    FetchedGithubAsset,
};
use crate::error::{AgentpackError, Result};
use crate::github::{
    canonical_github_tree_url, github_source_from_segments, parse_github_url, GitHubSource,
};
use crate::index::{
    aliases_for_github_entry, get_entry, lookup_alias, upsert_entry, CacheEntryRecord,
};
use crate::lockfile::{LockPlugin, LockSkill};
use crate::paths::{self};
use crate::ui::Ui;

pub(super) fn http_client() -> Result<Client> {
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

pub(super) fn resolve_add_spec(
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

pub(super) fn upsert_fetched_index(
    fetched: &FetchedGithubAsset,
    shorthand_alias: Option<&str>,
) -> Result<()> {
    let (rec, mut aliases) = record_and_aliases(fetched)?;
    merge_shorthand_alias(&mut aliases, shorthand_alias);
    upsert_entry(fetched.cache_key(), &rec, &aliases)
}

/// Used by `run_add` to reject bare paths early (manual manifest flow).
pub(super) fn resolve_existing_path_for_add(spec: &str) -> Option<PathBuf> {
    resolve_existing_path(spec.trim())
}
