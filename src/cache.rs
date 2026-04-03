use std::fs;
use std::path::{Path, PathBuf};

use serde_json::Value;
use sha2::{Digest, Sha256};
use walkdir::WalkDir;

use crate::error::{AgentpackError, Result};
use crate::github::{
    canonical_github_tree_url, choose_package_prefix_for_blob_path, collect_repo_relative_paths,
    download_and_extract, download_tarball_bytes, extract_tarball_with_prefix, parent_dir_in_repo,
    parse_github_url, path_in_repo_looks_like_file, resolve_ref_to_sha, GitHubSource,
};
use crate::lockfile::{LockPlugin, LockSkill};
use crate::manifest::AgentpackManifest;
use crate::paths::{self};
use crate::ui::Ui;
use reqwest::blocking::Client;

/// `hex(SHA256(identity_string))` — identity should include stable source + commit.
pub fn compute_cache_key(identity: &str) -> String {
    let mut h = Sha256::new();
    h.update(identity.as_bytes());
    hex::encode(h.finalize())
}

pub fn cache_entry_dir(cache_key: &str) -> Result<PathBuf> {
    Ok(paths::cache_dir()?.join(cache_key))
}

pub fn claude_plugin_manifest_path(cache_root: &Path) -> PathBuf {
    cache_root.join(".claude-plugin").join("plugin.json")
}

pub fn cursor_plugin_manifest_path(cache_root: &Path) -> PathBuf {
    cache_root.join(".cursor-plugin").join("plugin.json")
}

/// Legacy helper name: true if Claude and/or Cursor plugin manifest exists.
pub fn cache_has_plugin_manifest(cache_root: &Path) -> bool {
    claude_plugin_manifest_path(cache_root).is_file()
        || cursor_plugin_manifest_path(cache_root).is_file()
}

/// Paths relative to a **package directory** that identify it as a fetchable pack (plugin, skill, or nested manifest).
pub fn cache_dir_is_package_root_in_filesystem(dir: &Path) -> bool {
    dir.join("SKILL.md").is_file()
        || cache_has_plugin_manifest(dir)
        || dir.join(crate::paths::MANIFEST_NAME).is_file()
}

/// Same semantics as [`cache_dir_is_package_root_in_filesystem`], but for paths in a repo-relative path index (forward slashes).
pub fn repo_dir_is_package_root(rel_paths: &std::collections::HashSet<String>, dir: &str) -> bool {
    let dir = dir.trim_matches('/');
    let p = |leaf: &str| {
        if dir.is_empty() {
            leaf.to_string()
        } else {
            format!("{dir}/{leaf}")
        }
    };
    rel_paths.contains(&p(".claude-plugin/plugin.json"))
        || rel_paths.contains(&p(".cursor-plugin/plugin.json"))
        || rel_paths.contains(&p("SKILL.md"))
        || rel_paths.contains(&p(crate::paths::MANIFEST_NAME))
}

pub fn ensure_skill_md(cache_root: &Path) -> Result<()> {
    let skill = cache_root.join("SKILL.md");
    if skill.is_file() {
        return Ok(());
    }
    Err(AgentpackError::MissingSkillMd(cache_root.to_path_buf()))
}

pub fn ensure_plugin_manifest(cache_root: &Path) -> Result<()> {
    if cache_has_plugin_manifest(cache_root) {
        return Ok(());
    }
    Err(AgentpackError::MissingPluginManifest(
        cache_root.to_path_buf(),
    ))
}

/// Hash directory contents for stable path-sourced pins (40 hex for `pack.lock` commit field).
pub fn hash_directory_contents(root: &Path) -> Result<String> {
    let mut rel_paths: Vec<PathBuf> = Vec::new();
    for entry in WalkDir::new(root)
        .follow_links(false)
        .into_iter()
        .filter_map(|e| e.ok())
    {
        if !entry.file_type().is_file() {
            continue;
        }
        let p = entry.path();
        let rel = p
            .strip_prefix(root)
            .map_err(|e| AgentpackError::Cache(e.to_string()))?;
        rel_paths.push(rel.to_path_buf());
    }
    rel_paths.sort();
    let mut h = Sha256::new();
    for rel in rel_paths {
        h.update(rel.as_os_str().as_encoded_bytes());
        h.update(&[0]);
        let bytes = fs::read(root.join(&rel)).map_err(|e| AgentpackError::io(root.join(rel), e))?;
        h.update(&bytes);
    }
    let full = hex::encode(h.finalize());
    Ok(full.chars().take(40).collect())
}

fn read_json(path: &Path) -> Result<Value> {
    let s = fs::read_to_string(path).map_err(|e| AgentpackError::io(path, e))?;
    serde_json::from_str(&s).map_err(|e| AgentpackError::Cache(format!("{}: {e}", path.display())))
}

/// Ensure both `.claude-plugin` and `.cursor-plugin` exist when one side is present.
pub fn normalize_plugin_cache_layout(cache_root: &Path) -> Result<()> {
    let claude_p = claude_plugin_manifest_path(cache_root);
    let cursor_p = cursor_plugin_manifest_path(cache_root);
    match (claude_p.is_file(), cursor_p.is_file()) {
        (true, true) => Ok(()),
        (true, false) => {
            let v = read_json(&claude_p)?;
            let cursor_dir = cache_root.join(".cursor-plugin");
            fs::create_dir_all(&cursor_dir).map_err(|e| AgentpackError::io(&cursor_dir, e))?;
            let name = v["name"].as_str().unwrap_or("plugin");
            let stub = serde_json::json!({
                "name": name,
                "displayName": v.get("displayName").and_then(|x| x.as_str()).unwrap_or(name),
                "version": v.get("version").and_then(|x| x.as_str()).unwrap_or("1.0.0"),
                "description": v.get("description").and_then(|x| x.as_str()).unwrap_or(""),
            });
            let out = cursor_dir.join("plugin.json");
            fs::write(
                &out,
                serde_json::to_string_pretty(&stub)
                    .map_err(|e| AgentpackError::Cache(e.to_string()))?,
            )
            .map_err(|e| AgentpackError::io(&out, e))?;
            Ok(())
        }
        (false, true) => {
            let v = read_json(&cursor_p)?;
            let claude_dir = cache_root.join(".claude-plugin");
            fs::create_dir_all(&claude_dir).map_err(|e| AgentpackError::io(&claude_dir, e))?;
            let name = v["name"].as_str().unwrap_or("plugin");
            let stub = serde_json::json!({
                "name": name,
                "version": v.get("version").and_then(|x| x.as_str()).unwrap_or("1.0.0"),
                "description": v.get("description").or_else(|| v.get("displayName")).and_then(|x| x.as_str()).unwrap_or(""),
            });
            let out = claude_dir.join("plugin.json");
            fs::write(
                &out,
                serde_json::to_string_pretty(&stub)
                    .map_err(|e| AgentpackError::Cache(e.to_string()))?,
            )
            .map_err(|e| AgentpackError::io(&out, e))?;
            Ok(())
        }
        (false, false) => {
            // Plugin package defined only by **agentpack.toml** (skills/commands tree under root).
            let Some(m) = AgentpackManifest::load(cache_root)? else {
                return Ok(());
            };
            let claude_dir = cache_root.join(".claude-plugin");
            fs::create_dir_all(&claude_dir).map_err(|e| AgentpackError::io(&claude_dir, e))?;
            let claude_stub = serde_json::json!({
                "name": m.name,
                "version": m.version,
                "description": m.description,
            });
            let claude_out = claude_dir.join("plugin.json");
            fs::write(
                &claude_out,
                serde_json::to_string_pretty(&claude_stub)
                    .map_err(|e| AgentpackError::Cache(e.to_string()))?,
            )
            .map_err(|e| AgentpackError::io(&claude_out, e))?;

            let cursor_dir = cache_root.join(".cursor-plugin");
            fs::create_dir_all(&cursor_dir).map_err(|e| AgentpackError::io(&cursor_dir, e))?;
            let cursor_stub = serde_json::json!({
                "name": m.name,
                "displayName": m.name,
                "version": m.version,
                "description": m.description,
            });
            let cursor_out = cursor_dir.join("plugin.json");
            fs::write(
                &cursor_out,
                serde_json::to_string_pretty(&cursor_stub)
                    .map_err(|e| AgentpackError::Cache(e.to_string()))?,
            )
            .map_err(|e| AgentpackError::io(&cursor_out, e))?;
            Ok(())
        }
    }
}

fn copy_tree_files(src: &Path, dst: &Path) -> Result<()> {
    let effective = match fs::symlink_metadata(src) {
        Ok(m) if m.file_type().is_symlink() => match fs::canonicalize(src) {
            Ok(p) => p,
            Err(e) => {
                tracing::warn!(
                    path = %src.display(),
                    error = %e,
                    "skipping dangling symlink while copying into cache"
                );
                return Ok(());
            }
        },
        Ok(_) => src.to_path_buf(),
        Err(e) => return Err(AgentpackError::io(src, e)),
    };

    if effective.is_file() {
        if let Some(parent) = dst.parent() {
            fs::create_dir_all(parent).map_err(|e| AgentpackError::io(parent, e))?;
        }
        fs::copy(&effective, dst).map_err(|e| AgentpackError::io(dst, e))?;
        return Ok(());
    }

    if effective.is_dir() {
        fs::create_dir_all(dst).map_err(|e| AgentpackError::io(dst, e))?;
        for e in fs::read_dir(&effective).map_err(|e| AgentpackError::io(&effective, e))? {
            let e = e.map_err(|e| AgentpackError::io(&effective, e))?;
            copy_tree_files(&e.path(), &dst.join(e.file_name()))?;
        }
        return Ok(());
    }

    Ok(())
}

/// Copy a local directory into the content-addressed cache (path / local mirror / file adds).
/// Returns **`cache_key`**, **40-hex content fingerprint** (for `pack.lock` `commit`), and cache path.
pub fn copy_package_dir_to_cache(
    from: &Path,
    identity_prefix: &str,
) -> Result<(String, String, PathBuf)> {
    paths::ensure_user_agentpack_layout()?;
    let commit = hash_directory_contents(from)?;
    let identity = format!("{identity_prefix}\0{commit}");
    let cache_key = compute_cache_key(&identity);
    let out = cache_entry_dir(&cache_key)?;
    if out.exists() {
        fs::remove_dir_all(&out).map_err(|e| AgentpackError::io(&out, e))?;
    }
    let cdir = paths::cache_dir()?;
    fs::create_dir_all(&cdir).map_err(|e| AgentpackError::io(&cdir, e))?;
    fs::create_dir_all(&out).map_err(|e| AgentpackError::io(&out, e))?;
    copy_tree_files(from, &out)?;
    normalize_plugin_cache_layout(&out)?;
    Ok((cache_key, commit, out))
}

#[derive(Debug, Clone)]
pub enum FetchedGithubAsset {
    Skill(LockSkill),
    Plugin(LockPlugin),
}

fn lock_skill_from(
    display_url: &str,
    source: &GitHubSource,
    commit: String,
    cache_key: String,
) -> LockSkill {
    let module = if source.owner != "path" && source.owner != "local" {
        crate::module_id::ModuleId::from_owner_repo_path(&source.owner, &source.repo, &source.path)
            .as_str()
            .to_string()
    } else {
        String::new()
    };
    LockSkill {
        module,
        url: display_url.to_string(),
        owner: source.owner.clone(),
        repo: source.repo.clone(),
        path: source.path.clone(),
        commit,
        cache_key,
    }
}

fn lock_plugin_from(
    display_url: &str,
    source: &GitHubSource,
    commit: String,
    cache_key: String,
) -> LockPlugin {
    let module = if source.owner != "path" && source.owner != "local" {
        crate::module_id::ModuleId::from_owner_repo_path(&source.owner, &source.repo, &source.path)
            .as_str()
            .to_string()
    } else {
        String::new()
    };
    LockPlugin {
        module,
        name: String::new(),
        url: display_url.to_string(),
        owner: source.owner.clone(),
        repo: source.repo.clone(),
        path: source.path.clone(),
        commit,
        cache_key,
    }
}

/// After tree is on disk: skill vs full plugin.
pub(crate) fn classify_materialized(
    cache_root: &Path,
    display_url: &str,
    source: &GitHubSource,
    commit: String,
    cache_key: String,
) -> Result<FetchedGithubAsset> {
    normalize_plugin_cache_layout(cache_root)?;
    if claude_plugin_manifest_path(cache_root).is_file()
        || cursor_plugin_manifest_path(cache_root).is_file()
    {
        ensure_plugin_manifest(cache_root)?;
        return Ok(FetchedGithubAsset::Plugin(lock_plugin_from(
            display_url,
            source,
            commit,
            cache_key,
        )));
    }
    if cache_root.join("SKILL.md").is_file() {
        return Ok(FetchedGithubAsset::Skill(lock_skill_from(
            display_url,
            source,
            commit,
            cache_key,
        )));
    }
    Err(AgentpackError::InvalidCacheLayout(cache_root.to_path_buf()))
}

/// Parent directories of a repo-relative file path, deepest first, ending at repo root (`""`).
pub(crate) fn blob_path_parent_prefixes(blob_file_path: &str) -> Vec<String> {
    let path = blob_file_path.trim_matches('/');
    let mut out = Vec::new();
    let mut cur = Path::new(path).parent();
    while let Some(dir) = cur {
        let trimmed = dir.to_string_lossy().trim_matches('/').to_string();
        if trimmed.is_empty() {
            break;
        }
        out.push(trimmed);
        cur = dir.parent();
    }
    out.push(String::new());
    out
}

/// Whether **`owner` / `repo` / `path_prefix` / `commit`** already has a plugin or skill tree in `AGENTPACK_HOME/cache`.
fn github_prefix_cache_ready(owner: &str, repo: &str, commit: &str, path_prefix: &str) -> bool {
    let eff = GitHubSource {
        owner: owner.to_string(),
        repo: repo.to_string(),
        git_ref: "HEAD".into(),
        path: path_prefix.trim_matches('/').to_string(),
    };
    let identity = crate::github::normalized_identity(&eff, commit);
    let cache_key = compute_cache_key(&identity);
    let Ok(out) = cache_entry_dir(&cache_key) else {
        return false;
    };
    cache_has_plugin_manifest(&out)
        || out.join("SKILL.md").is_file()
        || out.join(crate::paths::MANIFEST_NAME).is_file()
}

/// Pin ref, download if needed, detect full plugin vs skill at cache root.
pub fn materialize_github_tree(
    client: &Client,
    source: &GitHubSource,
    display_url: &str,
    ui: &Ui,
) -> Result<FetchedGithubAsset> {
    paths::ensure_user_agentpack_layout()?;
    let pb = ui.spinner("Resolve Git ref → commit SHA");
    let commit = resolve_ref_to_sha(client, &source.owner, &source.repo, &source.git_ref)?;
    Ui::finish_spinner(
        pb.as_ref(),
        format!("Pinned {}…{}", &commit[..4], &commit[commit.len() - 4..]),
    );

    let blob_file = display_url.contains("/blob/") && path_in_repo_looks_like_file(&source.path);

    let mut eff = source.clone();
    let mut prefetched: Option<Vec<u8>> = None;

    if blob_file {
        let mut resolved_from_cache: Option<String> = None;
        for prefix in blob_path_parent_prefixes(&source.path) {
            if github_prefix_cache_ready(&source.owner, &source.repo, &commit, &prefix) {
                resolved_from_cache = Some(prefix);
                break;
            }
        }
        eff.path = if let Some(prefix) = resolved_from_cache {
            prefix
        } else {
            prefetched = Some(download_tarball_bytes(
                client,
                &source.owner,
                &source.repo,
                &commit,
                ui,
            )?);
            let index = collect_repo_relative_paths(prefetched.as_ref().unwrap())?;
            choose_package_prefix_for_blob_path(&index, &source.path)
                .unwrap_or_else(|| parent_dir_in_repo(&source.path))
        };
    } else {
        eff.path = source.path.trim_matches('/').to_string();
    }

    let identity = crate::github::normalized_identity(&eff, &commit);
    let cache_key = compute_cache_key(&identity);
    let out = cache_entry_dir(&cache_key)?;

    let cache_ready = cache_has_plugin_manifest(&out)
        || out.join("SKILL.md").is_file()
        || out.join(crate::paths::MANIFEST_NAME).is_file();

    if !cache_ready {
        let cdir = paths::cache_dir()?;
        fs::create_dir_all(&cdir).map_err(|e| AgentpackError::io(&cdir, e))?;
        let bytes = match prefetched {
            Some(b) => b,
            None => download_tarball_bytes(client, &source.owner, &source.repo, &commit, ui)?,
        };
        extract_tarball_with_prefix(&bytes, &eff.path, &out, ui)?;
    }

    let display = canonical_github_tree_url(&eff);
    classify_materialized(&out, &display, &eff, commit, cache_key)
}

pub fn fetch_github_asset_from_url(
    client: &Client,
    raw_url: &str,
    ui: &Ui,
) -> Result<FetchedGithubAsset> {
    let parsed = parse_github_url(raw_url)?;
    materialize_github_tree(client, &parsed, raw_url, ui)
}

/// Fetch skill into cache if missing; returns paths and metadata for lock/db.
/// Errors if the URL points at a full `.claude-plugin` tree (use [`fetch_github_asset_from_url`]).
pub fn fetch_skill_from_url(client: &Client, raw_url: &str, ui: &Ui) -> Result<LockSkill> {
    match fetch_github_asset_from_url(client, raw_url, ui)? {
        FetchedGithubAsset::Skill(s) => Ok(s),
        FetchedGithubAsset::Plugin(_) => Err(AgentpackError::Cache(
            "this URL is a full Claude plugin directory (.claude-plugin present); \
             add it as a plugin entry instead of a bare skill"
                .into(),
        )),
    }
}

pub fn fetch_skill_from_parsed(
    client: &Client,
    source: &GitHubSource,
    display_url: &str,
    ui: &Ui,
) -> Result<LockSkill> {
    match materialize_github_tree(client, source, display_url, ui)? {
        FetchedGithubAsset::Skill(s) => Ok(s),
        FetchedGithubAsset::Plugin(_) => Err(AgentpackError::Cache(
            "this path is a full Claude plugin directory (.claude-plugin present)".into(),
        )),
    }
}

/// Fill missing pin fields for legacy `[[plugins]]` rows (url only).
pub fn backfill_plugin_lock_entry(client: &Client, plugin: &mut LockPlugin, ui: &Ui) -> Result<()> {
    if !plugin.needs_backfill() {
        return Ok(());
    }
    let url = plugin.url.clone();
    match fetch_github_asset_from_url(client, &url, ui)? {
        FetchedGithubAsset::Plugin(p) => *plugin = p,
        FetchedGithubAsset::Skill(_) => {
            return Err(AgentpackError::Cache(format!(
                "plugin URL {} resolved to a skill subtree, not a .claude-plugin package",
                plugin.url
            )));
        }
    }
    Ok(())
}

fn is_path_or_local_source(skill: &LockSkill) -> bool {
    skill.owner == "path" || skill.owner == "local" || skill.url.starts_with("file:")
}

fn is_path_or_local_plugin(plugin: &LockPlugin) -> bool {
    plugin.owner == "path" || plugin.owner == "local" || plugin.url.starts_with("file:")
}

fn resolve_local_mirror_from_url(url: &str) -> Option<PathBuf> {
    let prefix = "agentpack-local:";
    if let Some(rest) = url.strip_prefix(prefix) {
        return paths::local_mirror_path_from_shorthand(rest).ok();
    }
    None
}

fn path_from_file_url(url: &str) -> Option<PathBuf> {
    let u = url::Url::parse(url).ok()?;
    if u.scheme() != "file" {
        return None;
    }
    u.to_file_path().ok()
}

/// Ensure an existing lock entry is present in cache; re-download if needed.
pub fn ensure_lock_skill_cached(client: &Client, skill: &LockSkill, ui: &Ui) -> Result<bool> {
    let out = cache_entry_dir(&skill.cache_key)?;
    if out.join("SKILL.md").is_file() || cache_has_plugin_manifest(&out) {
        return Ok(true);
    }

    if is_path_or_local_source(skill) {
        let src =
            path_from_file_url(&skill.url).or_else(|| resolve_local_mirror_from_url(&skill.url));
        if let Some(p) = src {
            if p.is_dir() {
                let cdir = paths::cache_dir()?;
                fs::create_dir_all(&cdir).map_err(|e| AgentpackError::io(&cdir, e))?;
                if out.exists() {
                    fs::remove_dir_all(&out).map_err(|e| AgentpackError::io(&out, e))?;
                }
                fs::create_dir_all(&out).map_err(|e| AgentpackError::io(&out, e))?;
                copy_tree_files(&p, &out)?;
                normalize_plugin_cache_layout(&out)?;
                return Ok(out.join("SKILL.md").is_file() || cache_has_plugin_manifest(&out));
            }
        }
        tracing::warn!(
            cache_key = %skill.cache_key,
            url = %skill.url,
            "skill cache missing and path/local source unavailable; skipping"
        );
        return Ok(false);
    }

    let cdir = paths::cache_dir()?;
    fs::create_dir_all(&cdir).map_err(|e| AgentpackError::io(&cdir, e))?;
    let blob_hint = (skill.url.contains("/blob/") && path_in_repo_looks_like_file(&skill.path))
        .then_some(skill.path.as_str());
    download_and_extract(
        client,
        &skill.owner,
        &skill.repo,
        &skill.commit,
        &skill.path,
        &out,
        ui,
        blob_hint,
    )?;
    ensure_skill_md(&out)?;
    Ok(true)
}

pub fn ensure_lock_plugin_cached(client: &Client, plugin: &LockPlugin, ui: &Ui) -> Result<bool> {
    let out = cache_entry_dir(&plugin.cache_key)?;
    normalize_plugin_cache_layout(&out)?;
    if cache_has_plugin_manifest(&out) {
        return Ok(true);
    }

    if is_path_or_local_plugin(plugin) {
        let src =
            path_from_file_url(&plugin.url).or_else(|| resolve_local_mirror_from_url(&plugin.url));
        if let Some(p) = src {
            if p.is_dir() {
                let cdir = paths::cache_dir()?;
                fs::create_dir_all(&cdir).map_err(|e| AgentpackError::io(&cdir, e))?;
                if out.exists() {
                    fs::remove_dir_all(&out).map_err(|e| AgentpackError::io(&out, e))?;
                }
                fs::create_dir_all(&out).map_err(|e| AgentpackError::io(&out, e))?;
                copy_tree_files(&p, &out)?;
                normalize_plugin_cache_layout(&out)?;
                return Ok(cache_has_plugin_manifest(&out));
            }
        }
        tracing::warn!(
            cache_key = %plugin.cache_key,
            url = %plugin.url,
            "plugin cache missing and path/local source unavailable; skipping"
        );
        return Ok(false);
    }

    let cdir = paths::cache_dir()?;
    fs::create_dir_all(&cdir).map_err(|e| AgentpackError::io(&cdir, e))?;
    let blob_hint = (plugin.url.contains("/blob/") && path_in_repo_looks_like_file(&plugin.path))
        .then_some(plugin.path.as_str());
    download_and_extract(
        client,
        &plugin.owner,
        &plugin.repo,
        &plugin.commit,
        &plugin.path,
        &out,
        ui,
        blob_hint,
    )?;
    ensure_plugin_manifest(&out)?;
    Ok(true)
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::github::GitHubSource;

    #[test]
    fn cache_key_stable() {
        let id = "github:foo/bar\0path\0".to_string() + &"a".repeat(40);
        let a = compute_cache_key(&id);
        let b = compute_cache_key(&id);
        assert_eq!(a, b);
        assert_eq!(a.len(), 64);
    }

    #[test]
    fn normalized_identity_ignores_git_ref_same_commit() {
        let src = GitHubSource {
            owner: "o".into(),
            repo: "r".into(),
            git_ref: "main".into(),
            path: "skills/x".into(),
        };
        let commit = "c".repeat(40);
        let k1 = compute_cache_key(&crate::github::normalized_identity(&src, &commit));
        let mut src2 = src.clone();
        src2.git_ref = "HEAD".into();
        let k2 = compute_cache_key(&crate::github::normalized_identity(&src2, &commit));
        assert_eq!(k1, k2);
        let mut src3 = src.clone();
        src3.path = "skills/y".into();
        let k3 = compute_cache_key(&crate::github::normalized_identity(&src3, &commit));
        assert_ne!(k1, k3);
    }

    #[test]
    fn classify_prefers_plugin_when_manifest_present() {
        let dir = tempfile::tempdir().unwrap();
        let root = dir.path().join("c");
        fs::create_dir_all(root.join(".claude-plugin")).unwrap();
        fs::write(root.join(".claude-plugin/plugin.json"), "{}").unwrap();
        fs::write(root.join("SKILL.md"), "# x").unwrap();
        assert!(cache_has_plugin_manifest(&root));
    }

    #[test]
    fn blob_path_parent_prefixes_deepest_first() {
        let p = super::blob_path_parent_prefixes("plugins/p/agents/a.md");
        assert_eq!(
            p,
            vec![
                "plugins/p/agents".into(),
                "plugins/p".into(),
                "plugins".into(),
                String::new(),
            ]
        );
    }

    #[test]
    fn package_root_detectors_agree_for_skill_dir() {
        let dir = tempfile::tempdir().unwrap();
        let root = dir.path();
        fs::write(root.join("SKILL.md"), "# s").unwrap();
        assert!(cache_dir_is_package_root_in_filesystem(root));
        let mut idx = std::collections::HashSet::new();
        idx.insert("SKILL.md".into());
        assert!(repo_dir_is_package_root(&idx, ""));
    }

    #[test]
    fn agentpack_toml_only_synthesizes_plugin_manifests() {
        let dir = tempfile::tempdir().unwrap();
        let root = dir.path();
        fs::write(
            root.join("agentpack.toml"),
            r#"name = "pkg-a"
version = "2.0.0"
description = "test plugin from manifest only"

[dependencies]
"#,
        )
        .unwrap();
        normalize_plugin_cache_layout(root).unwrap();
        assert!(claude_plugin_manifest_path(root).is_file());
        assert!(cursor_plugin_manifest_path(root).is_file());
        let v: serde_json::Value =
            serde_json::from_str(&fs::read_to_string(claude_plugin_manifest_path(root)).unwrap())
                .unwrap();
        assert_eq!(v["name"], "pkg-a");
        assert_eq!(v["version"], "2.0.0");
        assert_eq!(v["description"], "test plugin from manifest only");
    }
}
