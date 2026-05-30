use std::path::Path;

use super::codex_auth;
use crate::error::Result;

use super::constants::{CODEX_HOME_ENTRIES, CURSOR_USER_ROOT_ENTRIES};
use super::tree::copy_selected_entries;

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
