//! Bridge Codex CLI credentials into a staged `CODEX_HOME`.
//!
//! Codex hashes the canonical `CODEX_HOME` path into the macOS Keychain account id, so a staging
//! path never matches the user's login. To avoid per-project refresh-token drift, every staged
//! `CODEX_HOME` links `auth.json` to a shared source instead of taking its own snapshot, then
//! forces the staged `config.toml` to use file-backed credentials only for that staging tree.

use std::fs::{self, OpenOptions};
use std::io::Write;
use std::path::{Path, PathBuf};

use keyring::Entry;
use sha2::Digest;
use sha2::Sha256;

use crate::error::{AgentpackError, Result};
use crate::fs_util::remove_path_any;
use crate::paths;

const CODEX_AUTH_KEYRING_SERVICE: &str = "Codex Auth";

/// Matches `compute_store_key` in `openai/codex` (`codex-rs/login/src/auth/storage.rs`).
pub(super) fn codex_cli_keyring_account(codex_home: &Path) -> String {
    let canonical = codex_home
        .canonicalize()
        .unwrap_or_else(|_| codex_home.to_path_buf());
    let path_str = canonical.to_string_lossy();
    let mut hasher = Sha256::new();
    hasher.update(path_str.as_bytes());
    let digest = hasher.finalize();
    let hex = format!("{digest:x}");
    let truncated = hex.get(..16).unwrap_or(&hex);
    format!("cli|{truncated}")
}

fn write_codex_auth_json(dest: &Path, json: &str) -> Result<()> {
    if let Some(parent) = dest.parent() {
        fs::create_dir_all(parent).map_err(|e| AgentpackError::io(parent, e))?;
    }
    let mut open_opts = OpenOptions::new();
    open_opts.write(true).create(true).truncate(true);
    #[cfg(unix)]
    {
        use std::os::unix::fs::OpenOptionsExt;
        open_opts.mode(0o600);
    }
    let mut file = open_opts
        .open(dest)
        .map_err(|e| AgentpackError::io(dest, e))?;
    file.write_all(json.as_bytes())
        .map_err(|e| AgentpackError::io(dest, e))?;
    file.flush().map_err(|e| AgentpackError::io(dest, e))?;
    Ok(())
}

/// If the user stores credentials in the OS keychain under their real `~/.codex` home, copy the
/// serialized auth blob into the supplied **`dest_auth_json`** path so staged Codex homes can link
/// to one shared file.
pub(super) fn try_materialize_codex_auth_json_from_user_keyring(
    user_codex_home: &Path,
    dest_auth_json: &Path,
) -> Result<bool> {
    let account = codex_cli_keyring_account(user_codex_home);
    let entry = match Entry::new(CODEX_AUTH_KEYRING_SERVICE, &account) {
        Ok(e) => e,
        Err(e) => {
            tracing::debug!(
                "could not open Codex CLI keychain entry ({}): {e}",
                user_codex_home.display()
            );
            return Ok(false);
        }
    };

    let json = match entry.get_password() {
        Ok(s) => s,
        Err(keyring::Error::NoEntry) => {
            tracing::debug!(
                "no Codex CLI keychain entry for {}",
                user_codex_home.display()
            );
            return Ok(false);
        }
        Err(e) => {
            tracing::debug!(
                "could not read Codex CLI keychain ({}): {e}",
                user_codex_home.display()
            );
            return Ok(false);
        }
    };

    if serde_json::from_str::<serde_json::Value>(&json).is_err() {
        tracing::debug!(
            "Codex keychain auth payload is not valid JSON ({}); skipping bridge",
            user_codex_home.display()
        );
        return Ok(false);
    }

    write_codex_auth_json(dest_auth_json, &json)?;

    tracing::debug!(
        "materialized shared Codex auth.json from user keyring ({})",
        user_codex_home.display(),
    );
    Ok(true)
}

fn shared_codex_auth_source(user_codex_home: &Path) -> Result<Option<PathBuf>> {
    let user_auth = user_codex_home.join("auth.json");
    if user_auth.is_file() {
        return Ok(Some(user_auth));
    }

    let shared = paths::shared_codex_auth_path()?;
    if let Some(parent) = shared.parent() {
        fs::create_dir_all(parent).map_err(|e| AgentpackError::io(parent, e))?;
    }
    if shared.is_file() {
        return Ok(Some(shared));
    }
    let materialized = try_materialize_codex_auth_json_from_user_keyring(user_codex_home, &shared)?;
    if materialized {
        Ok(Some(shared))
    } else {
        Ok(None)
    }
}

fn link_staged_codex_auth(source: &Path, staged_auth: &Path) -> Result<()> {
    if let Some(parent) = staged_auth.parent() {
        fs::create_dir_all(parent).map_err(|e| AgentpackError::io(parent, e))?;
    }
    remove_path_any(staged_auth)?;

    let target = source
        .canonicalize()
        .unwrap_or_else(|_| source.to_path_buf());

    #[cfg(unix)]
    {
        std::os::unix::fs::symlink(&target, staged_auth)
            .map_err(|e| AgentpackError::io(staged_auth, e))?;
    }

    #[cfg(windows)]
    {
        match std::os::windows::fs::symlink_file(&target, staged_auth) {
            Ok(()) => {}
            Err(symlink_err) if target.is_file() => {
                fs::hard_link(&target, staged_auth).map_err(|hard_link_err| {
                    AgentpackError::Staging(format!(
                        "failed to link staged Codex auth.json to {}: symlink: {}; hard link: {}",
                        target.display(),
                        symlink_err,
                        hard_link_err
                    ))
                })?;
            }
            Err(symlink_err) => {
                return Err(AgentpackError::Staging(format!(
                    "failed to symlink staged Codex auth.json to {}: {}",
                    target.display(),
                    symlink_err
                )));
            }
        }
    }

    tracing::debug!(
        staged = %staged_auth.display(),
        source = %target.display(),
        "linked staged Codex auth.json to shared source"
    );
    Ok(())
}

pub(super) fn prepare_staged_codex_auth(user_codex_home: &Path, staging_root: &Path) -> Result<()> {
    let Some(source) = shared_codex_auth_source(user_codex_home)? else {
        tracing::debug!(
            "no Codex auth source available ({}); skipping staged auth.json link",
            user_codex_home.display()
        );
        let staged_auth = staging_root.join("auth.json");
        let _ = remove_path_any(&staged_auth);
        return Ok(());
    };
    let staged_auth = staging_root.join("auth.json");
    link_staged_codex_auth(&source, &staged_auth)
}

/// Rewrite the staged copy of `config.toml` to use file-backed credentials so every staged home
/// resolves to the linked `auth.json` instead of a path-derived keyring slot.
pub(super) fn force_staged_codex_credentials_store_to_file(staging_root: &Path) -> Result<()> {
    let path = staging_root.join("config.toml");
    let mut v = crate::fs_util::read_toml_value_or_default(&path)?;
    let Some(table) = v.as_table_mut() else {
        return Ok(());
    };
    if table
        .get("cli_auth_credentials_store")
        .and_then(|x| x.as_str())
        == Some("file")
    {
        return Ok(());
    }
    table.insert(
        "cli_auth_credentials_store".to_string(),
        toml::Value::String("file".into()),
    );
    let out = toml::to_string(&v)
        .map_err(|e| AgentpackError::Staging(format!("serialize {}: {e}", path.display())))?;
    fs::write(&path, out).map_err(|e| AgentpackError::io(&path, e))?;
    tracing::debug!(
        "staged Codex config.toml: forced cli_auth_credentials_store = file ({})",
        path.display()
    );
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::{
        codex_cli_keyring_account, force_staged_codex_credentials_store_to_file,
        link_staged_codex_auth,
    };
    use sha2::Digest;
    use std::fs;
    use std::path::Path;

    #[test]
    fn codex_keyring_account_matches_sha256_prefix_rule() {
        let tmp = std::env::temp_dir().join("agentpack-codex-keytest");
        let _ = std::fs::remove_dir_all(&tmp);
        std::fs::create_dir_all(&tmp).unwrap();
        let p = tmp.join("fake-codex");
        std::fs::create_dir_all(&p).unwrap();
        let canon = p.canonicalize().unwrap();
        let path_str = canon.to_string_lossy();
        let mut hasher = sha2::Sha256::new();
        hasher.update(path_str.as_bytes());
        let hex = format!("{:x}", hasher.finalize());
        let want = format!("cli|{}", &hex[..16]);
        assert_eq!(codex_cli_keyring_account(&p), want);
    }

    #[test]
    fn codex_keyring_account_nonexistent_falls_back_to_literal_path() {
        let p = Path::new("/no/such/codex/home/agentpack-test");
        let path_str = p.to_string_lossy();
        let mut hasher = sha2::Sha256::new();
        hasher.update(path_str.as_bytes());
        let hex = format!("{:x}", hasher.finalize());
        let want = format!("cli|{}", &hex[..16]);
        assert_eq!(codex_cli_keyring_account(p), want);
    }

    #[test]
    fn force_codex_store_to_file_creates_missing_config() {
        let dir = tempfile::tempdir().unwrap();
        force_staged_codex_credentials_store_to_file(dir.path()).unwrap();
        let config = fs::read_to_string(dir.path().join("config.toml")).unwrap();
        assert!(config.contains("cli_auth_credentials_store = \"file\""));
    }

    #[test]
    fn force_codex_store_to_file_overrides_existing_value() {
        let dir = tempfile::tempdir().unwrap();
        let config_path = dir.path().join("config.toml");
        fs::write(&config_path, "model = \"gpt-5.4\"\n").unwrap();
        force_staged_codex_credentials_store_to_file(dir.path()).unwrap();
        let config = fs::read_to_string(config_path).unwrap();
        assert!(config.contains("cli_auth_credentials_store = \"file\""));
        assert!(config.contains("model = \"gpt-5.4\""));
    }

    #[test]
    fn staged_auth_link_sees_source_updates() {
        let dir = tempfile::tempdir().unwrap();
        let source = dir.path().join("source-auth.json");
        let staged = dir.path().join("staged").join("auth.json");
        fs::write(&source, "{\"refresh_token\":\"old\"}\n").unwrap();

        link_staged_codex_auth(&source, &staged).unwrap();
        fs::write(&source, "{\"refresh_token\":\"new\"}\n").unwrap();

        let observed = fs::read_to_string(&staged).unwrap();
        assert!(observed.contains("\"new\""));
    }
}
