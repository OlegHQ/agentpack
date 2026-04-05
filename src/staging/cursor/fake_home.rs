//! Cursor fake HOME materialization: symlink pack trees and real credential files
//! into `$STAGING/cursor-home` so `agentpack agent` runs with a blended HOME.

use std::fs;
use std::path::{Path, PathBuf};

use crate::error::{AgentpackError, Result};
use crate::fs_util::remove_path_any;
use crate::paths::{staging_cursor_home_dir, staging_cursor_pack_plugin_dir};

use super::super::constants::{
    CURSOR_FAKE_HOME_CREDENTIAL_FILES, CURSOR_FAKE_HOME_PACK_SUBDIRS,
    CURSOR_USER_SUBDIRS_IN_FAKE_HOME,
};
#[cfg(windows)]
use super::super::tree::copy_merge_tree;

/// Symlink **src** → **dst** for Cursor fake HOME (absolute target); fall back to
/// **`copy_merge_tree`** on Windows when symlinks fail.
pub(in crate::staging) fn symlink_or_copy_into_fake_home(
    src: &Path,
    dst: &Path,
    as_dir: bool,
) -> Result<()> {
    if fs::symlink_metadata(dst).is_ok() {
        remove_path_any(dst)?;
    }
    if let Some(parent) = dst.parent() {
        fs::create_dir_all(parent).map_err(|e| AgentpackError::io(parent, e))?;
    }
    let target = src.canonicalize().map_err(|e| AgentpackError::io(src, e))?;
    #[cfg(unix)]
    {
        let _ = as_dir;
        std::os::unix::fs::symlink(&target, dst).map_err(|e| AgentpackError::io(dst, e))?;
        Ok(())
    }
    #[cfg(windows)]
    {
        let r = if as_dir {
            std::os::windows::fs::symlink_dir(&target, dst)
        } else {
            std::os::windows::fs::symlink_file(&target, dst)
        };
        if r.is_ok() {
            return Ok(());
        }
        copy_merge_tree(src, dst)
    }
}

/// Symlink **src** dir → **dst** if src exists, creating parent directories as needed.
fn symlink_dir_if_present(src: &Path, dst: &Path) -> Result<()> {
    if !src.is_dir() {
        return Ok(());
    }
    if let Some(parent) = dst.parent() {
        fs::create_dir_all(parent).map_err(|e| AgentpackError::io(parent, e))?;
    }
    symlink_or_copy_into_fake_home(src, dst, true)
}

/// Symlink Cursor's **Electron user-data** tree into the fake HOME.
fn materialize_cursor_platform_user_data(fake_home: &Path, real_home: &Path) -> Result<()> {
    #[cfg(target_os = "macos")]
    {
        symlink_dir_if_present(
            &real_home.join("Library/Keychains"),
            &fake_home.join("Library/Keychains"),
        )?;
        symlink_dir_if_present(
            &real_home.join("Library/Application Support/Cursor"),
            &fake_home.join("Library/Application Support/Cursor"),
        )?;
    }
    #[cfg(target_os = "linux")]
    {
        let config_base = std::env::var_os("XDG_CONFIG_HOME")
            .map(PathBuf::from)
            .unwrap_or_else(|| real_home.join(".config"));
        symlink_dir_if_present(
            &config_base.join("Cursor"),
            &fake_home.join(".config/Cursor"),
        )?;
        let data_base = std::env::var_os("XDG_DATA_HOME")
            .map(PathBuf::from)
            .unwrap_or_else(|| real_home.join(".local/share"));
        symlink_dir_if_present(
            &data_base.join("Cursor"),
            &fake_home.join(".local/share/Cursor"),
        )?;
    }
    #[cfg(target_os = "windows")]
    {
        symlink_dir_if_present(
            &real_home.join("AppData/Roaming/Cursor"),
            &fake_home.join("AppData/Roaming/Cursor"),
        )?;
    }
    Ok(())
}

/// Electron **`User`** dir (workspace trust + machine state).
fn cursor_electron_user_dir(real_home: &Path) -> PathBuf {
    #[cfg(target_os = "macos")]
    {
        real_home.join("Library/Application Support/Cursor/User")
    }
    #[cfg(all(unix, not(target_os = "macos")))]
    {
        let config_base = std::env::var_os("XDG_CONFIG_HOME")
            .map(PathBuf::from)
            .filter(|s| !s.as_os_str().is_empty())
            .unwrap_or_else(|| real_home.join(".config"));
        config_base.join("Cursor/User")
    }
    #[cfg(windows)]
    {
        real_home
            .join("AppData")
            .join("Roaming")
            .join("Cursor/User")
    }
    #[cfg(not(any(unix, windows)))]
    {
        real_home.join(".cursor/User")
    }
}

/// Physical **`globalStorage`** / **`workspaceStorage`** directory to expose at
/// **`$FAKE_HOME/.cursor/User/<name>`**.
fn cursor_user_storage_src(real_home: &Path, dot_cursor: &Path, leaf: &str) -> Result<PathBuf> {
    let user_dir = cursor_electron_user_dir(real_home);
    let electron_leaf = user_dir.join(leaf);
    let legacy_leaf = dot_cursor.join("User").join(leaf);
    if electron_leaf.exists() {
        return Ok(electron_leaf);
    }
    if legacy_leaf.exists() {
        return Ok(legacy_leaf);
    }
    fs::create_dir_all(&user_dir).map_err(|e| AgentpackError::io(&user_dir, e))?;
    fs::create_dir_all(&electron_leaf).map_err(|e| AgentpackError::io(&electron_leaf, e))?;
    Ok(electron_leaf)
}

/// Symlink each entry from **src_base** into **dst_base**, auto-detecting file vs dir.
fn symlink_entries_into(src_base: &Path, dst_base: &Path, names: &[&str]) -> Result<()> {
    for name in names {
        let src = src_base.join(name);
        if !src.exists() {
            continue;
        }
        let as_dir = fs::metadata(&src)
            .map(|m| m.is_dir())
            .map_err(|e| AgentpackError::io(&src, e))?;
        symlink_or_copy_into_fake_home(&src, &dst_base.join(name), as_dir)?;
    }
    Ok(())
}

/// **`$STAGING/cursor-home`**: **`HOME`** for **`agentpack agent`**, with **`.cursor`** blending
/// pack symlinks and real **`~/.cursor`** credential paths.
pub(in crate::staging) fn materialize_cursor_fake_home(project_root: &Path) -> Result<()> {
    let fake_home = staging_cursor_home_dir(project_root)?;
    if fake_home.exists() {
        fs::remove_dir_all(&fake_home).map_err(|e| AgentpackError::io(&fake_home, e))?;
    }
    let fake_cursor = fake_home.join(".cursor");
    fs::create_dir_all(&fake_cursor).map_err(|e| AgentpackError::io(&fake_cursor, e))?;

    let pack = staging_cursor_pack_plugin_dir(project_root)?;
    symlink_entries_into(&pack, &fake_cursor, CURSOR_FAKE_HOME_PACK_SUBDIRS)?;

    let real_home = dirs::home_dir();
    let real_cursor = real_home.as_ref().map(|h| h.join(".cursor"));
    let user_mcp = real_cursor.as_ref().and_then(|rc| {
        let p = rc.join("mcp.json");
        p.is_file().then_some(p)
    });
    let pack_mcp = pack.join("mcp.json");
    if let Some(ref um) = user_mcp {
        symlink_or_copy_into_fake_home(um, &fake_cursor.join("mcp.json"), false)?;
    } else if pack_mcp.is_file() {
        symlink_or_copy_into_fake_home(&pack_mcp, &fake_cursor.join("mcp.json"), false)?;
    }

    if let Some(ref rc) = real_cursor {
        if rc.is_dir() {
            symlink_entries_into(rc, &fake_cursor, CURSOR_FAKE_HOME_CREDENTIAL_FILES)?;
        }
    }

    if let Some(ref rh) = real_home {
        let dot_cursor = rh.join(".cursor");
        let fake_user = fake_cursor.join("User");
        fs::create_dir_all(&fake_user).map_err(|e| AgentpackError::io(&fake_user, e))?;
        for sub in CURSOR_USER_SUBDIRS_IN_FAKE_HOME {
            let src = cursor_user_storage_src(rh, &dot_cursor, sub)?;
            let as_dir = fs::metadata(&src)
                .map(|m| m.is_dir())
                .map_err(|e| AgentpackError::io(&src, e))?;
            symlink_or_copy_into_fake_home(&src, &fake_user.join(sub), as_dir)?;
        }
    }

    if let Some(rh) = dirs::home_dir() {
        materialize_cursor_platform_user_data(&fake_home, &rh)?;
    }

    Ok(())
}
