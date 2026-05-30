use std::path::Path;

use super::codex_auth;
use crate::error::Result;
use crate::fs_util::write_json_value;

use super::constants::{CODEX_HOME_ENTRIES, CURSOR_USER_ROOT_ENTRIES, OPENCODE_USER_ROOT_ENTRIES};
use super::tree::copy_selected_entries;

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

pub(crate) fn seed_opencode_root(root: &Path) -> Result<()> {
    let Some(home) = dirs::home_dir() else {
        write_opencode_config_stub(root)?;
        return Ok(());
    };
    let user_root = home.join(".config").join("opencode");
    copy_selected_entries(&user_root, root, OPENCODE_USER_ROOT_ENTRIES)?;
    write_opencode_config_stub(root)?;
    Ok(())
}

pub(crate) fn seed_codex_home(root: &Path) -> Result<()> {
    let Some(home) = dirs::home_dir() else {
        return Ok(());
    };
    let user_root = home.join(".codex");
    copy_selected_entries(&user_root, root, CODEX_HOME_ENTRIES)?;
    codex_auth::prepare_staged_codex_auth(&user_root, root)?;
    codex_auth::force_staged_codex_credentials_store_to_file(root)?;

    Ok(())
}

pub(super) fn seed_cursor_root(root: &Path) -> Result<()> {
    let Some(home) = dirs::home_dir() else {
        return Ok(());
    };
    let user_root = home.join(".cursor");
    copy_selected_entries(&user_root, root, CURSOR_USER_ROOT_ENTRIES)
}
