use chrono::Utc;
use redb::{Database, TableDefinition};
use serde::{Deserialize, Serialize};

use crate::error::{AgentpackError, Result};
use crate::paths;

const REF_CACHE: TableDefinition<&str, &[u8]> = TableDefinition::new("github_ref_cache");
const TAG_CACHE: TableDefinition<&str, &[u8]> = TableDefinition::new("github_tag_cache");

const REF_CACHE_TTL_SECS: i64 = 15 * 60;
const TAG_CACHE_TTL_SECS: i64 = 60 * 60;

#[derive(Debug, Clone, Serialize, Deserialize)]
pub(crate) struct CachedRef {
    pub(crate) sha: String,
    pub(crate) checked_at_unix: i64,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub(crate) struct CachedTags {
    pub(crate) tags: Vec<(String, String)>,
    pub(crate) checked_at_unix: i64,
}

// Facade over the persisted GitHub metadata tables so resolve/list callers do not touch RedDB.
pub(crate) struct GitHubMetadataCache;

impl GitHubMetadataCache {
    pub(crate) fn load_ref(owner: &str, repo: &str, git_ref: &str) -> Result<Option<CachedRef>> {
        read_entry(REF_CACHE, &ref_key(owner, repo, git_ref))
    }

    pub(crate) fn store_ref(owner: &str, repo: &str, git_ref: &str, sha: &str) -> Result<()> {
        write_entry(
            REF_CACHE,
            &ref_key(owner, repo, git_ref),
            &CachedRef {
                sha: sha.trim().to_lowercase(),
                checked_at_unix: Utc::now().timestamp(),
            },
        )
    }

    pub(crate) fn load_tags(owner: &str, repo: &str) -> Result<Option<CachedTags>> {
        read_entry(TAG_CACHE, &repo_key(owner, repo))
    }

    pub(crate) fn store_tags(owner: &str, repo: &str, tags: &[(String, String)]) -> Result<()> {
        write_entry(
            TAG_CACHE,
            &repo_key(owner, repo),
            &CachedTags {
                tags: tags.to_vec(),
                checked_at_unix: Utc::now().timestamp(),
            },
        )
    }

    pub(crate) fn ref_is_fresh(entry: &CachedRef) -> bool {
        is_fresh(entry.checked_at_unix, REF_CACHE_TTL_SECS)
    }

    pub(crate) fn tags_are_fresh(entry: &CachedTags) -> bool {
        is_fresh(entry.checked_at_unix, TAG_CACHE_TTL_SECS)
    }
}

fn ref_key(owner: &str, repo: &str, git_ref: &str) -> String {
    format!(
        "{}\0{}\0{}",
        owner.trim().to_lowercase(),
        repo.trim().to_lowercase(),
        git_ref.trim()
    )
}

fn repo_key(owner: &str, repo: &str) -> String {
    format!(
        "{}\0{}",
        owner.trim().to_lowercase(),
        repo.trim().to_lowercase()
    )
}

fn is_fresh(checked_at_unix: i64, ttl_secs: i64) -> bool {
    Utc::now().timestamp() - checked_at_unix <= ttl_secs
}

fn open_db_if_present() -> Result<Option<Database>> {
    let p = paths::cache_db_path()?;
    if !p.exists() {
        return Ok(None);
    }
    let db = Database::open(&p).map_err(|e| AgentpackError::Database(e.to_string()))?;
    Ok(Some(db))
}

fn open_or_create_db() -> Result<Database> {
    paths::ensure_user_agentpack_layout()?;
    let p = paths::cache_db_path()?;
    if let Some(parent) = p.parent() {
        std::fs::create_dir_all(parent).map_err(|e| AgentpackError::io(parent, e))?;
    }
    if p.exists() {
        Database::open(&p).map_err(|e| AgentpackError::Database(e.to_string()))
    } else {
        Database::create(&p).map_err(|e| AgentpackError::Database(e.to_string()))
    }
}

fn read_entry<T>(table_def: TableDefinition<&str, &[u8]>, key: &str) -> Result<Option<T>>
where
    T: for<'de> Deserialize<'de>,
{
    let Some(db) = open_db_if_present()? else {
        return Ok(None);
    };
    let read = db
        .begin_read()
        .map_err(|e| AgentpackError::Database(e.to_string()))?;
    let table = match read.open_table(table_def) {
        Ok(table) => table,
        Err(err) if err.to_string().contains("does not exist") => return Ok(None),
        Err(err) => return Err(AgentpackError::Database(err.to_string())),
    };
    let Some(value) = table
        .get(key)
        .map_err(|e| AgentpackError::Database(e.to_string()))?
    else {
        return Ok(None);
    };
    serde_json::from_slice(value.value())
        .map(Some)
        .map_err(|e| AgentpackError::Database(format!("deserialize: {e}")))
}

fn write_entry<T>(table_def: TableDefinition<&str, &[u8]>, key: &str, value: &T) -> Result<()>
where
    T: Serialize,
{
    let db = open_or_create_db()?;
    let write = db
        .begin_write()
        .map_err(|e| AgentpackError::Database(e.to_string()))?;
    let bytes = serde_json::to_vec(value)
        .map_err(|e| AgentpackError::Database(format!("serialize: {e}")))?;
    {
        let mut table = write
            .open_table(table_def)
            .map_err(|e| AgentpackError::Database(e.to_string()))?;
        table
            .insert(key, &bytes[..])
            .map_err(|e| AgentpackError::Database(e.to_string()))?;
    }
    write
        .commit()
        .map_err(|e| AgentpackError::Database(e.to_string()))?;
    Ok(())
}

#[cfg(test)]
mod tests {
    use serial_test::serial;
    use tempfile::tempdir;

    use super::{CachedRef, CachedTags, GitHubMetadataCache};

    #[test]
    #[serial]
    fn roundtrips_ref_entries() {
        let dir = tempdir().unwrap();
        std::env::set_var("AGENTPACK_HOME", dir.path());

        GitHubMetadataCache::store_ref("Owner", "Repo", "main", &"a".repeat(40)).unwrap();
        let cached = GitHubMetadataCache::load_ref("owner", "repo", "main")
            .unwrap()
            .unwrap();
        assert_eq!(cached.sha, "a".repeat(40));
        assert!(GitHubMetadataCache::ref_is_fresh(&cached));
    }

    #[test]
    #[serial]
    fn roundtrips_tag_entries() {
        let dir = tempdir().unwrap();
        std::env::set_var("AGENTPACK_HOME", dir.path());

        let tags = vec![("v1.2.3".to_string(), "b".repeat(40))];
        GitHubMetadataCache::store_tags("owner", "repo", &tags).unwrap();
        let cached = GitHubMetadataCache::load_tags("Owner", "Repo")
            .unwrap()
            .unwrap();
        assert_eq!(cached.tags, tags);
        assert!(GitHubMetadataCache::tags_are_fresh(&cached));
    }

    #[test]
    #[serial]
    fn freshness_windows_expire() {
        let stale_ref = CachedRef {
            sha: "c".repeat(40),
            checked_at_unix: 0,
        };
        let stale_tags = CachedTags {
            tags: vec![("v1.0.0".to_string(), "d".repeat(40))],
            checked_at_unix: 0,
        };

        assert!(!GitHubMetadataCache::ref_is_fresh(&stale_ref));
        assert!(!GitHubMetadataCache::tags_are_fresh(&stale_tags));
    }
}
