//! Cursor fake HOME materialization: symlink pack trees and real credential files
//! into `$STAGING/cursor-home` so `agentpack agent` runs with a blended HOME.

use std::fs;
use std::path::{Path, PathBuf};

use crate::error::{AgentpackError, Result};
#[cfg(windows)]
use crate::fs_util::copy_merge_tree;
use crate::fs_util::remove_path_any;
use crate::paths::{staging_cursor_home_dir_for_mode, staging_cursor_pack_plugin_dir_for_mode};
use crate::staging::mcp::load_mcp_json;

use super::{
    force_cursor_fake_home_attribution_off, CURSOR_FAKE_HOME_CREDENTIAL_FILES,
    CURSOR_FAKE_HOME_PACK_SUBDIRS, CURSOR_USER_SUBDIRS_IN_FAKE_HOME,
};

pub(super) fn symlink_or_copy_into_fake_home(src: &Path, dst: &Path, as_dir: bool) -> Result<()> {
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

fn symlink_dir_if_present(src: &Path, dst: &Path) -> Result<()> {
    if !src.is_dir() {
        return Ok(());
    }
    if let Some(parent) = dst.parent() {
        fs::create_dir_all(parent).map_err(|e| AgentpackError::io(parent, e))?;
    }
    symlink_or_copy_into_fake_home(src, dst, true)
}

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
            .filter(|s| !s.as_os_str().is_empty())
            .unwrap_or_else(|| real_home.join(".config"));
        symlink_dir_if_present(
            &config_base.join("Cursor"),
            &fake_home.join(".config/Cursor"),
        )?;
        let data_base = std::env::var_os("XDG_DATA_HOME")
            .map(PathBuf::from)
            .filter(|s| !s.as_os_str().is_empty())
            .unwrap_or_else(|| real_home.join(".local/share"));
        symlink_dir_if_present(
            &data_base.join("Cursor"),
            &fake_home.join(".local/share/Cursor"),
        )?;
    }
    #[cfg(windows)]
    {
        symlink_dir_if_present(
            &real_home.join("AppData/Roaming/Cursor"),
            &fake_home.join("AppData/Roaming/Cursor"),
        )?;
    }
    Ok(())
}

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

pub(super) fn materialize_cursor_fake_home(project_root: &Path, mode_name: &str) -> Result<()> {
    let fake_home = staging_cursor_home_dir_for_mode(project_root, mode_name)?;
    if fake_home.exists() {
        fs::remove_dir_all(&fake_home).map_err(|e| AgentpackError::io(&fake_home, e))?;
    }
    let fake_cursor = fake_home.join(".cursor");
    fs::create_dir_all(&fake_cursor).map_err(|e| AgentpackError::io(&fake_cursor, e))?;

    let pack = staging_cursor_pack_plugin_dir_for_mode(project_root, mode_name)?;
    symlink_entries_into(&pack, &fake_cursor, CURSOR_FAKE_HOME_PACK_SUBDIRS)?;

    let real_home = dirs::home_dir();
    let real_cursor = real_home.as_ref().map(|h| h.join(".cursor"));
    let user_mcp = real_cursor
        .as_ref()
        .map(|rc| rc.join("mcp.json"))
        .filter(|p| p.is_file());
    let pack_mcp = pack.join("mcp.json");
    let mcp_dest = fake_cursor.join("mcp.json");
    if let Some(ref user_path) = user_mcp {
        if pack_mcp.is_file() {
            // Merge: pack base (plugins + manifest + .agents), user entries win on conflict.
            let mut cfg = load_mcp_json(&pack_mcp)?;
            cfg.mcp_servers
                .extend(load_mcp_json(user_path)?.mcp_servers);
            let json = serde_json::to_string_pretty(&cfg)
                .map_err(|e| AgentpackError::Staging(format!("mcp.json merge: {e}")))?;
            fs::write(&mcp_dest, json).map_err(|e| AgentpackError::io(&mcp_dest, e))?;
        } else {
            symlink_or_copy_into_fake_home(user_path, &mcp_dest, false)?;
        }
    } else if pack_mcp.is_file() {
        symlink_or_copy_into_fake_home(&pack_mcp, &mcp_dest, false)?;
    }

    merge_cursor_hooks_into_fake_home(&pack, real_cursor.as_deref(), &fake_cursor)?;

    if let Some(ref rc) = real_cursor {
        if rc.is_dir() {
            symlink_entries_into(rc, &fake_cursor, CURSOR_FAKE_HOME_CREDENTIAL_FILES)?;
        }
    }

    // Replace any `cli-config.json` symlink with a real file containing forced attribution off.
    // Otherwise writes from agentpack would bleed back into the user's real `~/.cursor`.
    let real_cli_config = real_cursor
        .as_ref()
        .map(|rc| rc.join("cli-config.json"))
        .filter(|p| p.is_file());
    force_cursor_fake_home_attribution_off(&fake_cursor, real_cli_config.as_deref())?;

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

/// Cursor reads hooks from `~/.cursor/hooks.json`, not plugin directories. Concatenate user
/// and pack entries per event so both fire. Pack entries come second, so user hooks observe
/// tool invocations first and pack hooks run after (fine for observability; not a
/// decision-precedence choice since Cursor runs all `failClosed=true` gates anyway).
fn merge_cursor_hooks_into_fake_home(
    pack: &Path,
    real_cursor: Option<&Path>,
    fake_cursor: &Path,
) -> Result<()> {
    use serde_json::{Map, Value};

    let pack_hooks = pack.join("hooks/hooks.json");
    let user_hooks = real_cursor.map(|rc| rc.join("hooks.json"));
    let pack_present = pack_hooks.is_file();
    let user_present = user_hooks.as_ref().is_some_and(|p| p.is_file());
    if !pack_present && !user_present {
        return Ok(());
    }

    fn read_hook_file(path: &Path) -> Result<Value> {
        let raw = fs::read_to_string(path).map_err(|e| AgentpackError::io(path, e))?;
        crate::fs_util::parse_jsonc(&raw)
            .map_err(|e| AgentpackError::Staging(format!("{}: {e}", path.display())))
    }

    fn merge_event_arrays(dest: &mut Map<String, Value>, src: Value) {
        let Value::Object(src_hooks) = src else {
            return;
        };
        for (event, entries) in src_hooks {
            let Value::Array(new_entries) = entries else {
                continue;
            };
            let slot = dest
                .entry(event)
                .or_insert_with(|| Value::Array(Vec::new()));
            if let Value::Array(existing) = slot {
                existing.extend(new_entries);
            }
        }
    }

    let mut merged_hooks: Map<String, Value> = Map::new();
    if let Some(user_path) = user_hooks.as_ref().filter(|p| p.is_file()) {
        if let Value::Object(mut root) = read_hook_file(user_path)? {
            if let Some(user_hooks_obj) = root.remove("hooks") {
                merge_event_arrays(&mut merged_hooks, user_hooks_obj);
            }
        }
    }
    if pack_present {
        if let Value::Object(mut root) = read_hook_file(&pack_hooks)? {
            if let Some(pack_hooks_obj) = root.remove("hooks") {
                merge_event_arrays(&mut merged_hooks, pack_hooks_obj);
            }
        }
    }

    if merged_hooks.is_empty() {
        return Ok(());
    }
    let out = serde_json::json!({ "version": 1, "hooks": Value::Object(merged_hooks) });
    let dest = fake_cursor.join("hooks.json");
    crate::fs_util::write_text_file(
        &dest,
        &serde_json::to_string_pretty(&out)
            .map_err(|e| AgentpackError::Staging(format!("hooks.json merge: {e}")))?,
    )
}
