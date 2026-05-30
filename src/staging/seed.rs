use std::path::Path;

use crate::error::Result;

use super::constants::CURSOR_USER_ROOT_ENTRIES;
use super::tree::copy_selected_entries;

pub(super) fn seed_cursor_root(root: &Path) -> Result<()> {
    let Some(home) = dirs::home_dir() else {
        return Ok(());
    };
    let user_root = home.join(".cursor");
    copy_selected_entries(&user_root, root, CURSOR_USER_ROOT_ENTRIES)
}
