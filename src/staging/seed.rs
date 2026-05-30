use std::fs;
use std::path::Path;

use super::codex_auth;
use crate::error::{AgentpackError, Result};
use crate::fs_util::{remove_path_any, write_json_value};

use super::constants::{
    CODEX_HOME_ENTRIES, CURSOR_USER_ROOT_ENTRIES, GROK_HOME_CREDENTIAL_FILES, GROK_HOME_ENTRIES,
    OPENCODE_USER_ROOT_ENTRIES,
};
use super::tree::copy_merge_tree;

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
    let Some(home) = dirs::home_dir() else {
        return Ok(());
    };
    let user_root = home.join(".codex");
    copy_selected_entries(&user_root, root, CODEX_HOME_ENTRIES)?;
    codex_auth::prepare_staged_codex_auth(&user_root, root)?;
    codex_auth::force_staged_codex_credentials_store_to_file(root)?;

    Ok(())
}

fn symlink_or_copy_file(src: &Path, dst: &Path) -> Result<()> {
    if !src.is_file() {
        return Ok(());
    }
    if fs::symlink_metadata(dst).is_ok() {
        remove_path_any(dst)?;
    }
    if let Some(parent) = dst.parent() {
        fs::create_dir_all(parent).map_err(|e| AgentpackError::io(parent, e))?;
    }
    #[cfg(unix)]
    {
        std::os::unix::fs::symlink(src, dst).map_err(|e| AgentpackError::io(dst, e))?;
        Ok(())
    }
    #[cfg(windows)]
    {
        std::os::windows::fs::symlink_file(src, dst)
            .or_else(|_| fs::copy(src, dst).map(|_| ()))
            .map_err(|e| AgentpackError::io(dst, e))
    }
    #[cfg(not(any(unix, windows)))]
    {
        fs::copy(src, dst)
            .map(|_| ())
            .map_err(|e| AgentpackError::io(dst, e))
    }
}

fn ensure_grok_plugin_path(config_path: &Path, plugin_bundle: &Path) -> Result<()> {
    let mut doc = crate::fs_util::read_toml_value_or_default(config_path)?;
    let root = doc.as_table_mut().ok_or_else(|| {
        AgentpackError::Staging(format!(
            "{}: top-level must be a TOML table",
            config_path.display()
        ))
    })?;
    let plugins = root
        .entry("plugins".to_string())
        .or_insert_with(|| toml::Value::Table(Default::default()))
        .as_table_mut()
        .ok_or_else(|| {
            AgentpackError::Staging(format!(
                "{}: `plugins` must be a TOML table",
                config_path.display()
            ))
        })?;
    let paths = plugins
        .entry("paths".to_string())
        .or_insert_with(|| toml::Value::Array(Vec::new()))
        .as_array_mut()
        .ok_or_else(|| {
            AgentpackError::Staging(format!(
                "{}: `plugins.paths` must be an array",
                config_path.display()
            ))
        })?;
    let plugin_path = plugin_bundle.to_string_lossy().into_owned();
    if !paths
        .iter()
        .any(|value| value.as_str().is_some_and(|path| path == plugin_path))
    {
        paths.push(toml::Value::String(plugin_path));
    }
    let out = toml::to_string(&doc)
        .map_err(|e| AgentpackError::Staging(format!("{}: {e}", config_path.display())))?;
    crate::fs_util::write_text_file(config_path, &out)
}

pub(super) fn seed_grok_home(root: &Path, plugin_bundle: &Path) -> Result<()> {
    let Some(home) = dirs::home_dir() else {
        ensure_grok_plugin_path(&root.join("config.toml"), plugin_bundle)?;
        return Ok(());
    };
    let user_root = home.join(".grok");
    copy_selected_entries(&user_root, root, GROK_HOME_ENTRIES)?;
    for name in GROK_HOME_CREDENTIAL_FILES {
        symlink_or_copy_file(&user_root.join(name), &root.join(name))?;
    }
    ensure_grok_plugin_path(&root.join("config.toml"), plugin_bundle)?;
    Ok(())
}

pub(super) fn seed_cursor_root(root: &Path) -> Result<()> {
    let Some(home) = dirs::home_dir() else {
        return Ok(());
    };
    let user_root = home.join(".cursor");
    copy_selected_entries(&user_root, root, CURSOR_USER_ROOT_ENTRIES)
}
