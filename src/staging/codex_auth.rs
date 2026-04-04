//! Bridge Codex CLI credentials into a staged `CODEX_HOME`.
//!
//! Codex hashes the canonical `CODEX_HOME` path into the macOS Keychain account id, so a staging
//! path never matches the user's login. We copy or decode `auth.json` and, when needed, rewrite the
//! staged `config.toml` to use file-backed credentials only for that staging tree.

use std::fs::{self, OpenOptions};
use std::io::Write;
use std::path::Path;

use keyring::Entry;
use sha2::Digest;
use sha2::Sha256;

use crate::error::{AgentpackError, Result};

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

/// If the user stores credentials in the OS keychain under their real `~/.codex` home, copy the
/// serialized auth blob into **`staging_root`/auth.json** so the CLI can load it when combined with
/// [`patch_staged_codex_keyring_config_to_file`] if needed.
pub(super) fn try_materialize_codex_auth_json_from_user_keyring(
    user_codex_home: &Path,
    staging_root: &Path,
) -> Result<bool> {
    let account = codex_cli_keyring_account(user_codex_home);
    let entry = Entry::new(CODEX_AUTH_KEYRING_SERVICE, &account)
        .map_err(|e| AgentpackError::Staging(format!("codex keyring entry: {e}")))?;

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

    serde_json::from_str::<serde_json::Value>(&json).map_err(|e| {
        AgentpackError::Staging(format!(
            "codex keychain auth payload is not valid JSON: {e}"
        ))
    })?;

    let dest = staging_root.join("auth.json");
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
        .open(&dest)
        .map_err(|e| AgentpackError::io(&dest, e))?;
    file.write_all(json.as_bytes())
        .map_err(|e| AgentpackError::io(&dest, e))?;
    file.flush().map_err(|e| AgentpackError::io(&dest, e))?;

    tracing::debug!(
        "materialized Codex auth.json for staging from user keyring ({})",
        user_codex_home.display()
    );
    Ok(true)
}

/// When the **staged** copy of `config.toml` sets `cli_auth_credentials_store = "keyring"`, Codex
/// would look up the keychain using the **staging** path and find nothing. Rewrite **only the
/// staged file** to `"file"` so `auth.json` in the same tree is used.
pub(super) fn patch_staged_codex_keyring_config_to_file(staging_root: &Path) -> Result<()> {
    let path = staging_root.join("config.toml");
    if !path.is_file() {
        return Ok(());
    }
    let raw = fs::read_to_string(&path).map_err(|e| AgentpackError::io(&path, e))?;
    let mut v: toml::Value = toml::from_str(&raw)
        .map_err(|e| AgentpackError::Staging(format!("{}: {e}", path.display())))?;
    let Some(table) = v.as_table_mut() else {
        return Ok(());
    };
    if table
        .get("cli_auth_credentials_store")
        .and_then(|x| x.as_str())
        != Some("keyring")
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
        "staged Codex config.toml: cli_auth_credentials_store -> file ({})",
        path.display()
    );
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::codex_cli_keyring_account;
    use sha2::Digest;
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
}
