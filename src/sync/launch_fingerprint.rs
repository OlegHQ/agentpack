//! Fingerprint inputs that affect launch-time sync so harness commands can skip redundant work.

use std::env;
use std::fs;
use std::path::Path;

use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};
use walkdir::WalkDir;

use crate::error::{AgentpackError, Result};
use crate::mode::filter::EffectiveMode;
use crate::paths;

#[derive(Serialize, Deserialize)]
struct LaunchSyncState {
    digest: String,
}

fn hash_dot_agents_tree(dir: &Path) -> Result<Vec<u8>> {
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
    for path in &entries {
        let rel = path.strip_prefix(dir).map_err(|_| {
            AgentpackError::Cache("dot-agents path strip_prefix failed".to_string())
        })?;
        hasher.update(rel.as_os_str().as_encoded_bytes());
        hasher.update([0_u8]);
        let len = fs::metadata(path).map(|m| m.len()).unwrap_or(0);
        hasher.update(len.to_le_bytes());
        crate::fs_util::stream_file_into_hasher(path, &mut hasher)?;
    }
    Ok(hasher.finalize().to_vec())
}

/// Stable digest of everything that affects `run_sync` staging output for this project and mode.
pub fn compute_launch_sync_digest(project_root: &Path, mode: &EffectiveMode) -> Result<String> {
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
    hasher.update(
        env::var("AGENTPACK_STAGING_ROOT")
            .unwrap_or_default()
            .as_bytes(),
    );

    hasher.update(b"mode\0");
    hasher.update(mode.fingerprint_material().as_bytes());

    Ok(hex::encode(hasher.finalize()))
}

pub fn read_stored_launch_digest(project_root: &Path, mode_name: &str) -> Result<Option<String>> {
    let path = paths::launch_sync_state_path(project_root, mode_name)?;
    if !path.is_file() {
        return Ok(None);
    }
    let bytes = fs::read(&path).map_err(|e| AgentpackError::io(&path, e))?;
    let state: LaunchSyncState = serde_json::from_slice(&bytes)
        .map_err(|e| AgentpackError::Cache(format!("launch-sync.state: {e}")))?;
    Ok(Some(state.digest))
}

pub fn write_launch_sync_state(project_root: &Path, mode_name: &str, digest: &str) -> Result<()> {
    let dir = paths::project_state_dir(project_root)?;
    fs::create_dir_all(&dir).map_err(|e| AgentpackError::io(&dir, e))?;
    let path = paths::launch_sync_state_path(project_root, mode_name)?;
    let state = LaunchSyncState {
        digest: digest.into(),
    };
    let json = serde_json::to_vec_pretty(&state)
        .map_err(|e| AgentpackError::Cache(format!("launch-sync.state serialize: {e}")))?;
    fs::write(&path, json).map_err(|e| AgentpackError::io(&path, e))?;
    Ok(())
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
        let mode = EffectiveMode::implicit_default();
        let a = compute_launch_sync_digest(root, &mode).unwrap();
        let b = compute_launch_sync_digest(root, &mode).unwrap();
        assert_eq!(a, b);
    }

    #[test]
    fn digest_changes_when_manifest_changes() {
        let t = TempDir::new().unwrap();
        let root = t.path();
        write_file(root, "agentpack.toml", b"a");
        write_file(root, "pack.lock", b"lock");
        let mode = EffectiveMode::implicit_default();
        let d1 = compute_launch_sync_digest(root, &mode).unwrap();
        write_file(root, "agentpack.toml", b"b");
        let d2 = compute_launch_sync_digest(root, &mode).unwrap();
        assert_ne!(d1, d2);
    }

    #[test]
    fn digest_changes_when_dot_agents_changes() {
        let t = TempDir::new().unwrap();
        let root = t.path();
        write_file(root, "agentpack.toml", b"n=\"x\"\nv=\"1\"\n");
        write_file(root, "pack.lock", b"lockfile-version=2\n");
        write_file(root, ".agents/foo.md", b"1");
        let mode = EffectiveMode::implicit_default();
        let d1 = compute_launch_sync_digest(root, &mode).unwrap();
        write_file(root, ".agents/foo.md", b"2");
        let d2 = compute_launch_sync_digest(root, &mode).unwrap();
        assert_ne!(d1, d2);
    }

    #[test]
    fn digest_changes_when_mode_changes() {
        let t = TempDir::new().unwrap();
        let root = t.path();
        write_file(root, "agentpack.toml", b"name=\"x\"\nversion=\"1\"\n");
        write_file(root, "pack.lock", b"lockfile-version=2\n");

        let default_mode = EffectiveMode::implicit_default();
        let d1 = compute_launch_sync_digest(root, &default_mode).unwrap();
        let selective_mode = EffectiveMode::from_definition(
            "design",
            crate::mode::ModeDefinition {
                base: crate::mode::ModeBase::None,
                enable: vec!["mcp:filesystem".into()],
                disable: Vec::new(),
            },
        )
        .unwrap();
        let d2 = compute_launch_sync_digest(root, &selective_mode).unwrap();
        assert_ne!(d1, d2);
    }
}
