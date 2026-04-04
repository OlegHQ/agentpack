use std::env;
use std::path::Path;

use super::codex_auth;
use crate::error::Result;
use crate::fs_util::{read_json_value_opt, write_json_value};

use super::constants::{CODEX_HOME_ENTRIES, CURSOR_USER_ROOT_ENTRIES, OPENCODE_USER_ROOT_ENTRIES};
use super::tree::copy_merge_tree;

/// Copy user harness config into staged launch roots when **`1`** or unset.
/// Set **`AGENTPACK_BUNDLE_USER_SETTINGS`** or **`AGENTPACK_BUNDLE_USER_CLAUDE`** to **`0`** to skip.
fn copy_user_settings_enabled() -> bool {
    for key in [
        "AGENTPACK_BUNDLE_USER_SETTINGS",
        "AGENTPACK_BUNDLE_USER_CLAUDE",
    ] {
        if let Ok(v) = env::var(key) {
            return v != "0";
        }
    }
    true
}

/// Copies **`~/.claude/settings.json`** and **`~/.claude.json`** into the bundle.
/// Does **not** copy `commands/`, `agents/`, `skills/`, etc. (those stay user-scoped so slash
/// commands are not duplicated under `(agentpack-bundle)`).
pub(super) fn merge_user_settings_files_into_bundle(bundle: &Path) -> Result<()> {
    if !copy_user_settings_enabled() {
        return Ok(());
    }
    let Some(home) = dirs::home_dir() else {
        return Ok(());
    };

    let user_settings = home.join(".claude").join("settings.json");
    if let Some(v) = read_json_value_opt(&user_settings)? {
        let dst = bundle.join(".claude").join("settings.json");
        write_json_value(&dst, &v)?;
        tracing::debug!(from = %user_settings.display(), "copied user settings.json into bundle");
    }

    let user_app = home.join(".claude.json");
    if let Some(v) = read_json_value_opt(&user_app)? {
        let dst = bundle.join(".claude.json");
        write_json_value(&dst, &v)?;
        tracing::debug!(from = %user_app.display(), "copied user .claude.json into bundle");
    }

    Ok(())
}

fn copy_selected_entries(src_root: &Path, dst_root: &Path, entries: &[&str]) -> Result<()> {
    if !src_root.is_dir() {
        return Ok(());
    }
    for entry in entries {
        let src = src_root.join(entry);
        if src.exists() {
            copy_merge_tree(&src, &dst_root.join(entry))?;
        }
    }
    Ok(())
}

fn write_opencode_config_stub(root: &Path) -> Result<()> {
    let config_path = root.join("opencode.json");
    if config_path.exists() {
        return Ok(());
    }
    let value = serde_json::json!({
        "$schema": "https://opencode.ai/config.json"
    });
    write_json_value(&config_path, &value)
}

pub(super) fn seed_opencode_root(root: &Path) -> Result<()> {
    if !copy_user_settings_enabled() {
        write_opencode_config_stub(root)?;
        return Ok(());
    }
    let Some(home) = dirs::home_dir() else {
        write_opencode_config_stub(root)?;
        return Ok(());
    };
    let user_root = home.join(".config").join("opencode");
    copy_selected_entries(&user_root, root, OPENCODE_USER_ROOT_ENTRIES)?;
    write_opencode_config_stub(root)?;
    Ok(())
}

pub(super) fn seed_codex_home(root: &Path) -> Result<()> {
    if !copy_user_settings_enabled() {
        return Ok(());
    }
    let Some(home) = dirs::home_dir() else {
        return Ok(());
    };
    let user_root = home.join(".codex");
    copy_selected_entries(&user_root, root, CODEX_HOME_ENTRIES)?;

    let auth_json = root.join("auth.json");
    if !auth_json.is_file() {
        let _ = codex_auth::try_materialize_codex_auth_json_from_user_keyring(&user_root, root)?;
    }
    codex_auth::patch_staged_codex_keyring_config_to_file(root)?;

    Ok(())
}

pub(super) fn seed_cursor_root(root: &Path) -> Result<()> {
    if !copy_user_settings_enabled() {
        return Ok(());
    }
    let Some(home) = dirs::home_dir() else {
        return Ok(());
    };
    let user_root = home.join(".cursor");
    copy_selected_entries(&user_root, root, CURSOR_USER_ROOT_ENTRIES)
}
