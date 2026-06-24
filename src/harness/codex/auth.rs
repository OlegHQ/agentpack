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
    let parent = dest
        .parent()
        .ok_or_else(|| AgentpackError::Cache("codex auth path has no parent".into()))?;
    fs::create_dir_all(parent).map_err(|e| AgentpackError::io(parent, e))?;

    // Write to a per-process temp file then atomically rename into place. The shared auth file at
    // `$AGENTPACK_HOME/shared/codex/auth.json` may be materialized concurrently by two `agentpack
    // codex` launches in different projects; a plain truncate-then-write could expose a half-written
    // file to a third process reading through its symlink. Rename is atomic on POSIX.
    let tmp = parent.join(format!(".auth.json.tmp.{}", std::process::id()));
    let mut open_opts = OpenOptions::new();
    open_opts.write(true).create(true).truncate(true);
    #[cfg(unix)]
    {
        use std::os::unix::fs::OpenOptionsExt;
        open_opts.mode(0o600);
    }
    let mut file = open_opts
        .open(&tmp)
        .map_err(|e| AgentpackError::io(&tmp, e))?;
    file.write_all(json.as_bytes())
        .map_err(|e| AgentpackError::io(&tmp, e))?;
    file.flush().map_err(|e| AgentpackError::io(&tmp, e))?;
    drop(file);
    fs::rename(&tmp, dest).map_err(|e| {
        let _ = fs::remove_file(&tmp);
        AgentpackError::io(dest, e)
    })?;
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

fn shared_codex_auth_source(user_codex_home: &Path) -> Result<PathBuf> {
    let user_auth = user_codex_home.join("auth.json");
    if user_auth.is_file() {
        return Ok(user_auth);
    }

    let shared = paths::shared_codex_auth_path()?;
    if let Some(parent) = shared.parent() {
        fs::create_dir_all(parent).map_err(|e| AgentpackError::io(parent, e))?;
    }
    if shared.is_file() {
        return Ok(shared);
    }
    let _ = try_materialize_codex_auth_json_from_user_keyring(user_codex_home, &shared)?;
    Ok(shared)
}

pub(super) fn preserve_staged_codex_auth(staging_root: &Path) -> Result<()> {
    let shared = paths::shared_codex_auth_path()?;
    if shared.is_file() {
        return Ok(());
    }

    let staged_auth = staging_root.join("auth.json");
    let meta = match fs::symlink_metadata(&staged_auth) {
        Ok(meta) => meta,
        Err(err) if err.kind() == std::io::ErrorKind::NotFound => return Ok(()),
        Err(err) => return Err(AgentpackError::io(&staged_auth, err)),
    };
    if meta.file_type().is_symlink() || !meta.is_file() {
        return Ok(());
    }

    let json = fs::read_to_string(&staged_auth).map_err(|e| AgentpackError::io(&staged_auth, e))?;
    if serde_json::from_str::<serde_json::Value>(&json).is_err() {
        tracing::debug!(
            path = %staged_auth.display(),
            "staged Codex auth.json is not valid JSON; skipping preservation"
        );
        return Ok(());
    }

    write_codex_auth_json(&shared, &json)?;
    tracing::debug!(
        staged = %staged_auth.display(),
        shared = %shared.display(),
        "preserved staged Codex auth.json before rebuild"
    );
    Ok(())
}

fn link_staged_codex_auth(source: &Path, staged_auth: &Path) -> Result<()> {
    if let Some(parent) = staged_auth.parent() {
        fs::create_dir_all(parent).map_err(|e| AgentpackError::io(parent, e))?;
    }
    remove_path_any(staged_auth)?;

    let target = stable_auth_link_target(source);

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
            Err(symlink_err) if !target.exists() => {
                tracing::warn!(
                    target = %target.display(),
                    staged = %staged_auth.display(),
                    "could not create dangling Codex auth.json symlink on Windows: {symlink_err}; first login will be preserved on the next agentpack rebuild"
                );
                return Ok(());
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

fn stable_auth_link_target(source: &Path) -> PathBuf {
    if let Ok(target) = source.canonicalize() {
        return target;
    }
    if let (Some(parent), Some(name)) = (source.parent(), source.file_name()) {
        if let Ok(parent) = parent.canonicalize() {
            return parent.join(name);
        }
    }
    if source.is_absolute() {
        source.to_path_buf()
    } else {
        std::env::current_dir()
            .unwrap_or_else(|_| PathBuf::from("."))
            .join(source)
    }
}

pub(super) fn prepare_staged_codex_auth(user_codex_home: &Path, staging_root: &Path) -> Result<()> {
    let source = shared_codex_auth_source(user_codex_home)?;
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
        link_staged_codex_auth, preserve_staged_codex_auth,
    };
    use sha2::Digest;
    use std::ffi::OsString;
    use std::fs;
    use std::path::Path;

    struct AgentpackHomeGuard(Option<OsString>);

    impl AgentpackHomeGuard {
        fn set(value: impl AsRef<std::ffi::OsStr>) -> Self {
            let previous = std::env::var_os("AGENTPACK_HOME");
            std::env::set_var("AGENTPACK_HOME", value);
            Self(previous)
        }
    }

    impl Drop for AgentpackHomeGuard {
        fn drop(&mut self) {
            match self.0.take() {
                Some(value) => std::env::set_var("AGENTPACK_HOME", value),
                None => std::env::remove_var("AGENTPACK_HOME"),
            }
        }
    }

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

    #[cfg(unix)]
    #[test]
    fn staged_auth_link_allows_first_login_to_create_shared_source() {
        let dir = tempfile::tempdir().unwrap();
        let source = dir.path().join("shared").join("auth.json");
        let staged = dir.path().join("staged").join("auth.json");
        fs::create_dir_all(source.parent().unwrap()).unwrap();

        link_staged_codex_auth(&source, &staged).unwrap();
        fs::write(&staged, "{\"refresh_token\":\"new\"}\n").unwrap();

        let observed = fs::read_to_string(&source).unwrap();
        assert!(observed.contains("\"new\""));
    }

    #[test]
    #[serial_test::serial]
    fn preserve_staged_auth_copies_regular_file_to_shared_source() {
        let dir = tempfile::tempdir().unwrap();
        let _home_guard = AgentpackHomeGuard::set(dir.path().join("agentpack-home"));
        let staging = dir.path().join("staging");
        fs::create_dir_all(&staging).unwrap();
        fs::write(
            staging.join("auth.json"),
            "{\"tokens\":{\"refresh_token\":\"old\"}}\n",
        )
        .unwrap();

        preserve_staged_codex_auth(&staging).unwrap();

        let shared = crate::paths::shared_codex_auth_path().unwrap();
        let observed = fs::read_to_string(shared).unwrap();
        assert!(observed.contains("\"old\""));
    }
}
