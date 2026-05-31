use std::env;
use std::fs;
use std::path::{Path, PathBuf};

use chrono::Utc;
use reqwest::blocking::Client;
use url::Url;

use crate::cache::index::{
    aliases_for_github_entry, get_entry, lookup_alias, upsert_entry, CacheEntryRecord,
};
use crate::cache::{
    cache_entry_dir, cache_has_plugin_manifest, classify_materialized, claude_plugin_manifest_path,
    copy_package_dir_to_cache, cursor_plugin_manifest_path, ensure_lock_cached,
    fetch_github_asset_from_url, materialize_github_tree,
};
use crate::error::{AgentpackError, Result};
use crate::github::{
    canonical_github_tree_url, github_source_from_segments, github_source_from_segments_ref,
    parse_github_url, GitHubSource, DEFAULT_GIT_REF, GITHUB_HOST,
};
use crate::lockfile::{LockPackage, PackageKind};
use crate::paths;
use crate::resolve::module_id::split_module_at_ref;
use crate::ui::Ui;

pub(super) fn http_client() -> Result<Client> {
    Client::builder()
        .user_agent("agentpack/0.1 (https://github.com/)")
        .build()
        .map_err(|e| AgentpackError::GitHubApi(e.to_string()))
}

fn read_plugin_package_name(cache_root: &Path) -> Option<String> {
    [
        claude_plugin_manifest_path(cache_root),
        cursor_plugin_manifest_path(cache_root),
    ]
    .iter()
    .filter(|p| p.is_file())
    .find_map(|p| {
        crate::fs_util::read_json_value(p)
            .ok()
            .and_then(|v| v.get("name").and_then(|x| x.as_str()).map(str::to_owned))
    })
}

fn record_and_aliases(fetched: &LockPackage) -> Result<(CacheEntryRecord, Vec<String>)> {
    let rec = CacheEntryRecord {
        kind: fetched.kind,
        source_url: fetched.url.clone(),
        owner: fetched.owner.clone(),
        repo: fetched.repo.clone(),
        path: fetched.path.clone(),
        commit: fetched.commit.clone(),
        fetched_at_unix: Utc::now().timestamp(),
    };
    let aliases = if fetched.kind == PackageKind::Skill {
        let name = crate::staging::skill_folder_name(fetched);
        aliases_for_github_entry(&fetched.owner, &fetched.repo, &fetched.path, Some(&name))
    } else {
        let cache_root = cache_entry_dir(&fetched.cache_key)?;
        let pkg = read_plugin_package_name(&cache_root);
        aliases_for_github_entry(&fetched.owner, &fetched.repo, &fetched.path, pkg.as_deref())
    };
    Ok((rec, aliases))
}

fn merge_shorthand_alias(aliases: &mut Vec<String>, shorthand: Option<&str>) {
    if let Some(s) = shorthand {
        let t = s.trim().to_lowercase();
        if !t.is_empty() && !aliases.contains(&t) {
            aliases.push(t);
        }
    }
}

pub(super) fn resolve_existing_path(spec: &str) -> Option<PathBuf> {
    let p = Path::new(spec);
    let candidate = if p.is_absolute() {
        p.to_path_buf()
    } else {
        env::current_dir().ok()?.join(p)
    };
    fs::canonicalize(&candidate).ok().filter(|c| c.is_dir())
}

fn add_from_filesystem(canon: &Path, _ui: &Ui) -> Result<LockPackage> {
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
) -> Result<LockPackage> {
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
    git_ref: Option<&str>,
    ui: &Ui,
) -> Result<LockPackage> {
    let spec = format!("{owner}/{repo}");
    // An explicit `@ref` bypasses the local mirror and cache-alias shortcuts (which are ref-blind)
    // so we always fetch the requested revision.
    if git_ref.is_none() {
        let mirror = paths::local_mirror_path_from_shorthand(&spec)?;
        if mirror.is_dir() {
            return add_from_local_mirror(&spec, owner, repo, "", ui);
        }
        if let Some(ck) = lookup_alias(&spec)? {
            if let Some(rec) = get_entry(&ck)? {
                return recreate_fetched_from_record(client, &rec, &ck, ui);
            }
        }
    }
    let source =
        github_source_from_segments_ref(owner, repo, "", git_ref.unwrap_or(DEFAULT_GIT_REF));
    let display = canonical_github_tree_url(&source);
    materialize_github_tree(client, &source, &display, ui, false)
}

fn add_multi_segment(
    client: &Client,
    parts: &[&str],
    git_ref: Option<&str>,
    ui: &Ui,
) -> Result<LockPackage> {
    let owner = parts[0];
    let repo = parts[1];
    let in_path = parts[2..].join("/");
    let spec = parts.join("/");
    if git_ref.is_none() {
        let mirror = paths::local_mirror_path_from_shorthand(&spec)?;
        if mirror.is_dir() {
            return add_from_local_mirror(&spec, owner, repo, &in_path, ui);
        }
        if let Some(ck) = lookup_alias(&spec)? {
            if let Some(rec) = get_entry(&ck)? {
                return recreate_fetched_from_record(client, &rec, &ck, ui);
            }
        }
    }
    let source =
        github_source_from_segments_ref(owner, repo, &in_path, git_ref.unwrap_or(DEFAULT_GIT_REF));
    let display = canonical_github_tree_url(&source);
    materialize_github_tree(client, &source, &display, ui, false)
}

fn add_one_segment(client: &Client, name: &str, ui: &Ui) -> Result<LockPackage> {
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
) -> Result<LockPackage> {
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
        let proxy = LockPackage {
            module: String::new(),
            direct: false,
            kind: rec.kind,
            name: String::new(),
            url: rec.source_url.clone(),
            owner: rec.owner.clone(),
            repo: rec.repo.clone(),
            path: rec.path.clone(),
            commit: rec.commit.clone(),
            cache_key: cache_key.to_string(),
        };
        ensure_lock_cached(client, &proxy, ui)?;
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
        git_ref: DEFAULT_GIT_REF.into(),
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

/// Outcome of resolving an `add` spec: the fetched package, an optional shorthand alias to record
/// in the cache index, and the optional ref (`@branch`/`@tag`/`@commit`) to persist in the manifest.
pub(super) struct ResolvedAdd {
    pub package: LockPackage,
    pub shorthand: Option<String>,
    pub git_ref: Option<String>,
}

pub(super) fn resolve_add_spec(client: &Client, spec: &str, ui: &Ui) -> Result<ResolvedAdd> {
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
        let package = fetch_github_asset_from_url(client, spec, ui)?;
        return Ok(ResolvedAdd {
            package,
            shorthand: None,
            git_ref: None,
        });
    }
    if let Some(canon) = resolve_existing_path(spec) {
        let package = add_from_filesystem(&canon, ui)?;
        return Ok(ResolvedAdd {
            package,
            shorthand: None,
            git_ref: None,
        });
    }
    // Shorthand form: peel an optional `@ref`, then drop a leading `github.com/` host segment so the
    // canonical module-id form (`github.com/owner/repo/path`, as shown in docs/manifest/pack.lock)
    // and the bare `owner/repo/path` form both resolve.
    let (base, git_ref) = split_module_at_ref(spec);
    let mut parts: Vec<&str> = base.split('/').filter(|s| !s.is_empty()).collect();
    if parts.first() == Some(&GITHUB_HOST) {
        parts.remove(0);
    }
    match parts.len() {
        0 => Err(AgentpackError::Cache("invalid add spec".into())),
        // A bare single segment is a local-mirror / cache-alias lookup; `@ref` is not meaningful.
        1 => Ok(ResolvedAdd {
            package: add_one_segment(client, parts[0], ui)?,
            shorthand: None,
            git_ref: None,
        }),
        2 => {
            let package = add_two_segment(client, parts[0], parts[1], git_ref, ui)?;
            Ok(ResolvedAdd {
                package,
                shorthand: Some(parts.join("/")),
                git_ref: git_ref.map(str::to_string),
            })
        }
        _ => {
            let package = add_multi_segment(client, &parts, git_ref, ui)?;
            Ok(ResolvedAdd {
                package,
                shorthand: Some(parts.join("/")),
                git_ref: git_ref.map(str::to_string),
            })
        }
    }
}

pub(super) fn upsert_fetched_index(
    fetched: &LockPackage,
    shorthand_alias: Option<&str>,
) -> Result<()> {
    let (rec, mut aliases) = record_and_aliases(fetched)?;
    merge_shorthand_alias(&mut aliases, shorthand_alias);
    upsert_entry(&fetched.cache_key, &rec, &aliases)
}

#[cfg(test)]
mod tests {
    use std::fs;

    use chrono::Utc;
    use reqwest::blocking::Client;
    use serial_test::serial;
    use tempfile::tempdir;

    use crate::cache::index::{upsert_entry, CacheEntryRecord};
    use crate::lockfile::PackageKind;
    use crate::ui::Ui;

    use super::resolve_add_spec;

    #[test]
    #[serial]
    fn resolve_add_spec_reuses_cached_slash_alias() {
        let dir = tempdir().unwrap();
        std::env::set_var("AGENTPACK_HOME", dir.path());

        let cache_key = "cached-owner-repo-path";
        let cache_root = dir.path().join("cache").join(cache_key);
        fs::create_dir_all(&cache_root).unwrap();
        fs::write(cache_root.join("SKILL.md"), "# skill\n").unwrap();

        upsert_entry(
            cache_key,
            &CacheEntryRecord {
                kind: PackageKind::Skill,
                source_url: "https://github.com/owner/repo/tree/main/skills/reuse-me".into(),
                owner: "owner".into(),
                repo: "repo".into(),
                path: "skills/reuse-me".into(),
                commit: "a".repeat(40),
                fetched_at_unix: Utc::now().timestamp(),
            },
            &["owner/repo/skills/reuse-me".to_string()],
        )
        .unwrap();

        let client = Client::builder().build().unwrap();
        let resolved =
            resolve_add_spec(&client, "owner/repo/skills/reuse-me", &Ui::test_stub()).unwrap();

        assert_eq!(resolved.package.cache_key, cache_key);
        assert_eq!(
            resolved.shorthand.as_deref(),
            Some("owner/repo/skills/reuse-me")
        );
        assert_eq!(resolved.git_ref, None);
    }

    #[test]
    #[serial]
    fn resolve_add_spec_strips_github_host_prefix() {
        // The canonical `github.com/owner/repo/path` form (with host) must reuse the same cached
        // alias as the bare `owner/repo/path` form rather than treating `github.com` as the owner.
        let dir = tempdir().unwrap();
        std::env::set_var("AGENTPACK_HOME", dir.path());

        let cache_key = "host-prefixed-alias";
        let cache_root = dir.path().join("cache").join(cache_key);
        fs::create_dir_all(&cache_root).unwrap();
        fs::write(cache_root.join("SKILL.md"), "# skill\n").unwrap();
        upsert_entry(
            cache_key,
            &CacheEntryRecord {
                kind: PackageKind::Skill,
                source_url: "https://github.com/owner/repo/tree/main/skills/reuse-me".into(),
                owner: "owner".into(),
                repo: "repo".into(),
                path: "skills/reuse-me".into(),
                commit: "a".repeat(40),
                fetched_at_unix: Utc::now().timestamp(),
            },
            &["owner/repo/skills/reuse-me".to_string()],
        )
        .unwrap();

        let client = Client::builder().build().unwrap();
        let resolved = resolve_add_spec(
            &client,
            "github.com/owner/repo/skills/reuse-me",
            &Ui::test_stub(),
        )
        .unwrap();

        assert_eq!(resolved.package.cache_key, cache_key);
        assert_eq!(
            resolved.shorthand.as_deref(),
            Some("owner/repo/skills/reuse-me")
        );
    }
}
