//! Fingerprint inputs that affect launch-time sync so harness commands can skip redundant work.

use std::env;
use std::fs;
use std::path::Path;

use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};
use walkdir::WalkDir;

use crate::error::{AgentpackError, Result};
use crate::paths;

const LAUNCH_SYNC_STATE: &str = "launch-sync.state";

#[derive(Serialize, Deserialize)]
struct LaunchSyncState {
    digest: String,
}

/// Same semantics as [`crate::staging::seed`] for user-settings copies into staged roots.
fn bundle_user_settings_fingerprint() -> &'static str {
    for key in [
        "AGENTPACK_BUNDLE_USER_SETTINGS",
        "AGENTPACK_BUNDLE_USER_CLAUDE",
    ] {
        if let Ok(v) = env::var(key) {
            return if v == "0" { "0" } else { "1" };
        }
    }
    "1"
}

fn dot_agents_merge_fingerprint() -> &'static str {
    match env::var("AGENTPACK_DOT_AGENTS") {
        Ok(v) if v == "0" => "0",
        _ => "1",
    }
}

fn collision_env_fingerprint() -> String {
    env::var("AGENTPACK_IGNORE_USER_BUNDLE_COLLISION").unwrap_or_default()
}

fn staging_root_fingerprint() -> String {
    match env::var("AGENTPACK_STAGING_ROOT") {
        Ok(p) if !p.trim().is_empty() => p.trim().to_string(),
        _ => "\0AGENTPACK_STAGING_DEFAULT".to_string(),
    }
}

fn hash_dot_agents_tree(dir: &Path) -> Result<Vec<u8>> {
    use std::io::Read;

    if !dir.is_dir() {
        return Ok(Vec::from(b"__dot_agents_absent__" as &[u8]));
    }

    let mut entries: Vec<_> = WalkDir::new(dir)
        .into_iter()
        .filter_map(|e| e.ok())
        .filter(|e| e.file_type().is_file())
        .map(|e| e.into_path())
        .collect();
    entries.sort();

    let mut hasher = Sha256::new();
    let mut buf = [0u8; 8192];
    for path in &entries {
        let rel = path.strip_prefix(dir).map_err(|_| {
            AgentpackError::Cache("dot-agents path strip_prefix failed".to_string())
        })?;
        hasher.update(rel.as_os_str().as_encoded_bytes());
        hasher.update([0_u8]);
        // Stream file contents through hasher in chunks instead of loading entirely.
        let file = fs::File::open(path).map_err(|err| AgentpackError::io(path, err))?;
        let len = file.metadata().map(|m| m.len()).unwrap_or(0);
        hasher.update(len.to_le_bytes());
        let mut reader = std::io::BufReader::new(file);
        loop {
            let n = reader
                .read(&mut buf)
                .map_err(|err| AgentpackError::io(path, err))?;
            if n == 0 {
                break;
            }
            hasher.update(&buf[..n]);
        }
    }
    Ok(hasher.finalize().to_vec())
}

/// Stable digest of everything that affects `run_sync` staging output for this project.
pub fn compute_launch_sync_digest(project_root: &Path) -> Result<String> {
    let mut hasher = Sha256::new();

    let manifest_path = paths::manifest_path(project_root);
    hasher.update(b"manifest\0");
    if manifest_path.is_file() {
        let b = fs::read(&manifest_path).map_err(|e| AgentpackError::io(&manifest_path, e))?;
        hasher.update(&b);
    } else {
        hasher.update(b"__missing__");
    }

    let lock_path = paths::lock_path(project_root);
    hasher.update(b"lock\0");
    let lock_bytes = fs::read(&lock_path).map_err(|e| AgentpackError::io(&lock_path, e))?;
    hasher.update(&lock_bytes);

    hasher.update(b"dot_agents\0");
    let dot = paths::project_dot_agents_dir(project_root);
    hasher.update(hash_dot_agents_tree(&dot)?);

    hasher.update(b"staging_root\0");
    hasher.update(staging_root_fingerprint().as_bytes());

    hasher.update(b"bundle_user_settings\0");
    hasher.update(bundle_user_settings_fingerprint().as_bytes());

    hasher.update(b"dot_agents_env\0");
    hasher.update(dot_agents_merge_fingerprint().as_bytes());

    hasher.update(b"collision_env\0");
    hasher.update(collision_env_fingerprint().as_bytes());

    Ok(hex::encode(hasher.finalize()))
}

pub fn read_stored_launch_digest(project_root: &Path) -> Result<Option<String>> {
    let path = paths::project_state_dir(project_root)?.join(LAUNCH_SYNC_STATE);
    if !path.is_file() {
        return Ok(None);
    }
    let bytes = fs::read(&path).map_err(|e| AgentpackError::io(&path, e))?;
    let state: LaunchSyncState = serde_json::from_slice(&bytes)
        .map_err(|e| AgentpackError::Cache(format!("launch-sync.state: {e}")))?;
    Ok(Some(state.digest))
}

pub fn write_launch_sync_state(project_root: &Path, digest: &str) -> Result<()> {
    let dir = paths::project_state_dir(project_root)?;
    fs::create_dir_all(&dir).map_err(|e| AgentpackError::io(&dir, e))?;
    let path = dir.join(LAUNCH_SYNC_STATE);
    let state = LaunchSyncState {
        digest: digest.into(),
    };
    let json = serde_json::to_vec_pretty(&state)
        .map_err(|e| AgentpackError::Cache(format!("launch-sync.state serialize: {e}")))?;
    fs::write(&path, json).map_err(|e| AgentpackError::io(&path, e))?;
    Ok(())
}

pub fn launch_full_sync_forced() -> bool {
    env::var("AGENTPACK_LAUNCH_FULL_SYNC")
        .map(|v| {
            let v = v.to_ascii_lowercase();
            matches!(v.as_str(), "1" | "true" | "yes")
        })
        .unwrap_or(false)
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::io::Write;

    use tempfile::TempDir;

    fn write_file(dir: &Path, rel: &str, body: &[u8]) {
        let p = dir.join(rel);
        if let Some(par) = p.parent() {
            fs::create_dir_all(par).unwrap();
        }
        fs::File::create(&p).unwrap().write_all(body).unwrap();
    }

    #[test]
    fn digest_stable_for_same_inputs() {
        let t = TempDir::new().unwrap();
        let root = t.path();
        write_file(root, "agentpack.toml", b"name=\"x\"\nversion=\"1\"\n");
        write_file(root, "pack.lock", b"lockfile-version=2\n");
        let a = compute_launch_sync_digest(root).unwrap();
        let b = compute_launch_sync_digest(root).unwrap();
        assert_eq!(a, b);
    }

    #[test]
    fn digest_changes_when_manifest_changes() {
        let t = TempDir::new().unwrap();
        let root = t.path();
        write_file(root, "agentpack.toml", b"a");
        write_file(root, "pack.lock", b"lock");
        let d1 = compute_launch_sync_digest(root).unwrap();
        write_file(root, "agentpack.toml", b"b");
        let d2 = compute_launch_sync_digest(root).unwrap();
        assert_ne!(d1, d2);
    }

    #[test]
    fn digest_changes_when_dot_agents_changes() {
        let t = TempDir::new().unwrap();
        let root = t.path();
        write_file(root, "agentpack.toml", b"n=\"x\"\nv=\"1\"\n");
        write_file(root, "pack.lock", b"lockfile-version=2\n");
        write_file(root, ".agents/foo.md", b"1");
        let d1 = compute_launch_sync_digest(root).unwrap();
        write_file(root, ".agents/foo.md", b"2");
        let d2 = compute_launch_sync_digest(root).unwrap();
        assert_ne!(d1, d2);
    }

    #[test]
    #[serial_test::serial]
    fn launch_full_sync_forced_reads_env() {
        std::env::remove_var("AGENTPACK_LAUNCH_FULL_SYNC");
        assert!(!super::launch_full_sync_forced());
        std::env::set_var("AGENTPACK_LAUNCH_FULL_SYNC", "1");
        assert!(super::launch_full_sync_forced());
        std::env::set_var("AGENTPACK_LAUNCH_FULL_SYNC", "yes");
        assert!(super::launch_full_sync_forced());
        std::env::remove_var("AGENTPACK_LAUNCH_FULL_SYNC");
    }
}
