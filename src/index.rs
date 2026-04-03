use redb::{Database, ReadableTable, TableDefinition};
use serde::{Deserialize, Serialize};

use crate::error::{AgentpackError, Result};
use crate::paths::{self};

const ENTRIES: TableDefinition<&str, &[u8]> = TableDefinition::new("cache_entries");
const ALIASES: TableDefinition<&str, &str> = TableDefinition::new("aliases");

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct CacheEntryRecord {
    pub kind: String,
    pub source_url: String,
    pub owner: String,
    pub repo: String,
    pub path: String,
    pub commit: String,
    pub fetched_at_unix: i64,
}

/// Upsert cache entry and optional slash-form aliases (lowercased) for repeat `add`.
pub fn upsert_entry(cache_key: &str, record: &CacheEntryRecord, aliases: &[String]) -> Result<()> {
    paths::ensure_user_agentpack_layout()?;
    let p = crate::paths::cache_db_path()?;
    if let Some(parent) = p.parent() {
        std::fs::create_dir_all(parent).map_err(|e| AgentpackError::io(parent, e))?;
    }
    let bytes = serde_json::to_vec(record)
        .map_err(|e| AgentpackError::Database(format!("serialize: {e}")))?;
    let db = if p.exists() {
        Database::open(&p).map_err(|e| AgentpackError::Database(e.to_string()))?
    } else {
        Database::create(&p).map_err(|e| AgentpackError::Database(e.to_string()))?
    };
    let write = db
        .begin_write()
        .map_err(|e| AgentpackError::Database(e.to_string()))?;
    {
        let mut table = write
            .open_table(ENTRIES)
            .map_err(|e| AgentpackError::Database(e.to_string()))?;
        table
            .insert(cache_key, &bytes[..])
            .map_err(|e| AgentpackError::Database(e.to_string()))?;
        if !aliases.is_empty() {
            let mut at = write
                .open_table(ALIASES)
                .map_err(|e| AgentpackError::Database(e.to_string()))?;
            for a in aliases {
                let key = a.trim().to_lowercase();
                if key.is_empty() {
                    continue;
                }
                at.insert(key.as_str(), cache_key)
                    .map_err(|e| AgentpackError::Database(e.to_string()))?;
            }
        }
    }
    write
        .commit()
        .map_err(|e| AgentpackError::Database(e.to_string()))?;
    Ok(())
}

/// Lookup cached `cache_key` by shorthand like `anthropics/skills/algorithmic-art`.
pub fn lookup_alias(alias: &str) -> Result<Option<String>> {
    let p = crate::paths::cache_db_path()?;
    if !p.exists() {
        return Ok(None);
    }
    let db = Database::open(&p).map_err(|e| AgentpackError::Database(e.to_string()))?;
    let read = db
        .begin_read()
        .map_err(|e| AgentpackError::Database(e.to_string()))?;
    let table = read
        .open_table(ALIASES)
        .map_err(|e| AgentpackError::Database(e.to_string()))?;
    let key = alias.trim().to_lowercase();
    let Some(v) = table
        .get(key.as_str())
        .map_err(|e| AgentpackError::Database(e.to_string()))?
    else {
        return Ok(None);
    };
    Ok(Some(v.value().to_string()))
}

/// Read primary entry by `cache_key`.
#[allow(dead_code)]
pub fn get_entry(cache_key: &str) -> Result<Option<CacheEntryRecord>> {
    let p = crate::paths::cache_db_path()?;
    if !p.exists() {
        return Ok(None);
    }
    let db = Database::open(&p).map_err(|e| AgentpackError::Database(e.to_string()))?;
    let read = db
        .begin_read()
        .map_err(|e| AgentpackError::Database(e.to_string()))?;
    let table = read
        .open_table(ENTRIES)
        .map_err(|e| AgentpackError::Database(e.to_string()))?;
    let Some(v) = table
        .get(cache_key)
        .map_err(|e| AgentpackError::Database(e.to_string()))?
    else {
        return Ok(None);
    };
    let s = v.value();
    let record: CacheEntryRecord = serde_json::from_slice(s)
        .map_err(|e| AgentpackError::Database(format!("deserialize: {e}")))?;
    Ok(Some(record))
}

pub fn list_keys() -> Result<Vec<String>> {
    let p = crate::paths::cache_db_path()?;
    if !p.exists() {
        return Ok(Vec::new());
    }
    let db = Database::open(&p).map_err(|e| AgentpackError::Database(e.to_string()))?;
    let read = db
        .begin_read()
        .map_err(|e| AgentpackError::Database(e.to_string()))?;
    let table = read
        .open_table(ENTRIES)
        .map_err(|e| AgentpackError::Database(e.to_string()))?;
    let mut keys = Vec::new();
    for item in table
        .iter()
        .map_err(|e| AgentpackError::Database(e.to_string()))?
    {
        let (k, _) = item.map_err(|e| AgentpackError::Database(e.to_string()))?;
        keys.push(k.value().to_string());
    }
    Ok(keys)
}

/// Build alias list for a GitHub-backed row (slash forms + optional package name).
pub fn aliases_for_github_entry(
    owner: &str,
    repo: &str,
    in_repo_path: &str,
    package_name: Option<&str>,
) -> Vec<String> {
    let o = owner.to_lowercase();
    let r = repo.to_lowercase();
    let mut v = Vec::new();
    let p = in_repo_path.trim_matches('/');
    if p.is_empty() {
        v.push(format!("{o}/{r}"));
    } else {
        v.push(format!("{o}/{r}/{p}"));
    }
    if let Some(name) = package_name {
        let n = name.trim().to_lowercase();
        if !n.is_empty() {
            v.push(n);
        }
    }
    v
}

#[cfg(test)]
mod tests {
    use super::*;
    use tempfile::tempdir;

    #[test]
    fn redb_roundtrip() {
        let dir = tempdir().unwrap();
        std::env::set_var("AGENTPACK_HOME", dir.path());
        let rec = CacheEntryRecord {
            kind: "skill".into(),
            source_url: "https://example.com".into(),
            owner: "o".into(),
            repo: "r".into(),
            path: "p".into(),
            commit: "c".repeat(40),
            fetched_at_unix: 0,
        };
        upsert_entry("deadbeef", &rec, &["o/r/p".to_string()]).unwrap();
        let got = get_entry("deadbeef").unwrap().unwrap();
        assert_eq!(got.commit, rec.commit);
        let keys = list_keys().unwrap();
        assert_eq!(keys.len(), 1);
        assert_eq!(keys[0], "deadbeef");
        assert_eq!(lookup_alias("O/R/P").unwrap().as_deref(), Some("deadbeef"));
    }
}
