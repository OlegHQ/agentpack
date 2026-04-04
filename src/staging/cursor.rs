use std::fs;
use std::io::ErrorKind;
use std::path::{Path, PathBuf};

use serde_json::Value;

use crate::artifacts::HarnessTarget;
use crate::error::{AgentpackError, Result};
use crate::fs_util::{remove_path_any, write_json_value, write_text_file};
use crate::lockfile::PackLock;
use crate::manifest::AgentpackManifest;
use crate::paths::{
    cursor_overlay_manifest_path, cursor_workspace_dir, staging_cursor_bundle_dir,
    staging_cursor_home_dir, staging_cursor_pack_plugin_dir, STAGED_AGENTPACK_BUNDLE_NAME,
};

use super::constants::{
    CURSOR_FAKE_HOME_CREDENTIAL_FILES, CURSOR_FAKE_HOME_PACK_SUBDIRS,
    CURSOR_USER_SUBDIRS_IN_FAKE_HOME, CURSOR_WORKSPACE_AGENTS_OVERLAY,
};
use super::pack_overlay::{stage_pack_plugins_for_target, stage_pack_skills_for_target};
use super::seed::seed_cursor_root;
#[cfg(windows)]
use super::tree::copy_merge_tree;

/// Symlink **src** → **dst** for Cursor fake HOME (absolute target); fall back to **`copy_merge_tree`** on Windows when symlinks fail.
fn symlink_or_copy_into_fake_home(src: &Path, dst: &Path, as_dir: bool) -> Result<()> {
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

/// Symlink Cursor’s **Electron user-data** tree into the fake HOME.
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

/// Electron **`User`** dir (workspace trust + machine state); see VS Code’s workspace identity / `workspaceStorage` layout.
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

/// Physical **`globalStorage`** / **`workspaceStorage`** directory to expose at **`$FAKE_HOME/.cursor/User/<name>`**.
///
/// Prefer the Electron user-data **`User`** folder (where the desktop app records trust); fall back to legacy **`~/.cursor/User`**.
/// If neither exists, create the Electron path so new trust state persists outside ephemeral staging.
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

/// **`$STAGING/cursor-home`**: **`HOME`** for **`agentpack agent`**, with **`.cursor`** blending pack symlinks and real **`~/.cursor`** credential paths.
fn materialize_cursor_fake_home(project_root: &Path) -> Result<()> {
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

pub(super) fn read_cursor_overlay_manifest(project_root: &Path) -> Result<Vec<PathBuf>> {
    let manifest = cursor_overlay_manifest_path(project_root)?;
    match fs::read_to_string(&manifest) {
        Ok(contents) => Ok(contents
            .lines()
            .map(str::trim)
            .filter(|line| !line.is_empty())
            .map(PathBuf::from)
            .collect()),
        Err(err) if err.kind() == ErrorKind::NotFound => Ok(Vec::new()),
        Err(err) => Err(AgentpackError::io(&manifest, err)),
    }
}

fn write_cursor_overlay_manifest(project_root: &Path, entries: &[PathBuf]) -> Result<()> {
    let manifest = cursor_overlay_manifest_path(project_root)?;
    if entries.is_empty() {
        remove_path_any(&manifest)?;
        return Ok(());
    }

    if let Some(parent) = manifest.parent() {
        fs::create_dir_all(parent).map_err(|err| AgentpackError::io(parent, err))?;
    }

    let mut normalized: Vec<String> = entries
        .iter()
        .map(|entry| entry.to_string_lossy().into_owned())
        .collect();
    normalized.sort();
    normalized.dedup();
    fs::write(&manifest, format!("{}\n", normalized.join("\n")))
        .map_err(|err| AgentpackError::io(&manifest, err))?;
    Ok(())
}

fn dir_has_cursor_agent_markdown(dir: &Path) -> bool {
    let Ok(rd) = fs::read_dir(dir) else {
        return false;
    };
    rd.filter_map(|e| e.ok()).any(|e| {
        e.path()
            .extension()
            .is_some_and(|x| x.eq_ignore_ascii_case("md") || x.eq_ignore_ascii_case("mdc"))
    })
}

/// Drop a tracked workspace path from a prior **`sync`**. Only removes **symlinks** or **files** — never
/// **`remove_dir_all`** on a directory (avoids wiping a real **`./.cursor/agents`** tree someone created).
fn remove_cursor_overlay_path_safe(path: &Path) -> Result<()> {
    let meta = match fs::symlink_metadata(path) {
        Ok(m) => m,
        Err(e) if e.kind() == ErrorKind::NotFound => return Ok(()),
        Err(e) => return Err(AgentpackError::io(path, e)),
    };
    if meta.file_type().is_symlink() || meta.is_file() {
        fs::remove_file(path).map_err(|e| AgentpackError::io(path, e))?;
    } else if meta.is_dir() {
        tracing::warn!(
            path = %path.display(),
            "cursor overlay cleanup: manifest entry is a directory; leaving in place (not removing)"
        );
    }
    Ok(())
}

/// Removes workspace **`.cursor/`** paths listed in **`cursor-overlay.manifest`** (agentpack-owned symlinks).
fn cleanup_cursor_overlay(project_root: &Path) -> Result<()> {
    let cursor_root = cursor_workspace_dir(project_root);
    for rel in read_cursor_overlay_manifest(project_root)? {
        remove_cursor_overlay_path_safe(&cursor_root.join(rel))?;
    }
    write_cursor_overlay_manifest(project_root, &[])?;
    Ok(())
}

/// **`./.cursor/agents`** → **`$STAGING/cursor/agentpack-bundle/agents`** so Cursor **`agent`** finds subagents under **`--workspace`**.
fn materialize_workspace_cursor_agents_symlink(project_root: &Path) -> Result<Vec<PathBuf>> {
    let pack_agents = staging_cursor_pack_plugin_dir(project_root)?.join("agents");
    if !pack_agents.is_dir() || !dir_has_cursor_agent_markdown(&pack_agents) {
        return Ok(Vec::new());
    }
    let source = pack_agents
        .canonicalize()
        .map_err(|e| AgentpackError::io(&pack_agents, e))?;

    let cursor_root = cursor_workspace_dir(project_root);
    let agents_link = cursor_root.join(CURSOR_WORKSPACE_AGENTS_OVERLAY);

    match fs::symlink_metadata(&agents_link) {
        Ok(meta) if meta.is_dir() => {
            tracing::warn!(
                path = %agents_link.display(),
                "agentpack: ./.cursor/agents exists as a directory; not replacing with pack symlink"
            );
            return Ok(Vec::new());
        }
        Ok(meta) if meta.is_file() => {
            tracing::warn!(
                path = %agents_link.display(),
                "agentpack: ./.cursor/agents exists as a file; not replacing with pack symlink"
            );
            return Ok(Vec::new());
        }
        Ok(_) => {
            remove_cursor_overlay_path_safe(&agents_link)?;
        }
        Err(e) if e.kind() == ErrorKind::NotFound => {}
        Err(e) => return Err(AgentpackError::io(&agents_link, e)),
    }

    fs::create_dir_all(&cursor_root).map_err(|e| AgentpackError::io(&cursor_root, e))?;
    symlink_or_copy_into_fake_home(&source, &agents_link, true)?;
    Ok(vec![PathBuf::from(CURSOR_WORKSPACE_AGENTS_OVERLAY)])
}

/// Writes **`$STAGING/cursor/.cursor-plugin/marketplace.json`** and **`$STAGING/cursor/<bundle>/.cursor-plugin/plugin.json`** per Cursor multi-plugin layout (`plugin_spec.md`).
fn write_cursor_pack_plugin_manifests(cursor_root: &Path) -> Result<()> {
    let marketplace_dir = cursor_root.join(".cursor-plugin");
    fs::create_dir_all(&marketplace_dir).map_err(|e| AgentpackError::io(&marketplace_dir, e))?;

    let pack_plugin = cursor_root.join(STAGED_AGENTPACK_BUNDLE_NAME);
    let plugin_manifest_dir = pack_plugin.join(".cursor-plugin");
    fs::create_dir_all(&plugin_manifest_dir)
        .map_err(|e| AgentpackError::io(&plugin_manifest_dir, e))?;

    let marketplace: Value = serde_json::json!({
        "name": "agentpack",
        "owner": { "name": "agentpack" },
        "metadata": {
            "description": "Pinned GitHub skills and Claude plugins from pack.lock",
            "version": "1.0.0"
        },
        "plugins": [{
            "name": STAGED_AGENTPACK_BUNDLE_NAME,
            "source": STAGED_AGENTPACK_BUNDLE_NAME,
            "description": "Merged pack.lock content staged by agentpack"
        }]
    });
    write_json_value(&marketplace_dir.join("marketplace.json"), &marketplace)?;

    let plugin_json: Value = serde_json::json!({
        "name": STAGED_AGENTPACK_BUNDLE_NAME,
        "displayName": "agentpack bundle",
        "version": "1.0.0",
        "description": "Merged plugins and skills from pack.lock (agentpack).",
        "author": { "name": "agentpack" },
        "license": "MIT",
        "keywords": ["agentpack", "pack.lock"]
    });
    write_json_value(&plugin_manifest_dir.join("plugin.json"), &plugin_json)?;
    Ok(())
}

fn write_cursor_pack_plugin_readme(pack_plugin: &Path) -> Result<()> {
    let readme = pack_plugin.join("README.md");
    let body = r#"# agentpack bundle

This directory is generated by **agentpack** from `pack.lock` under `$STAGING/cursor` when you run `sync`.
It mirrors the [Cursor plugin layout](https://cursor.com/docs/reference/plugins) (`plugin_spec.md` in the agentpack repo).

Do not commit this tree — it is a local staging copy; `agentpack agent` uses **`$STAGING/cursor-home`** as **`HOME`** and defaults **`--workspace`** to the canonical project root.
"#;
    write_text_file(&readme, body)
}

/// Cursor pack tree and marketplace layout only. **`rebuild_staging`** calls **`stage_dot_agents_overlay`**
/// next, then **`finalize_cursor_staging`** (fake **`HOME`** + workspace **`agents`** symlink), so dot-agents
/// content is included before symlinks are written.
pub(super) fn rebuild_cursor_staging_without_finalize(
    project_root: &Path,
    lock: &PackLock,
    manifest: Option<&AgentpackManifest>,
) -> Result<()> {
    cleanup_cursor_overlay(project_root)?;
    let root = staging_cursor_bundle_dir(project_root)?;
    fs::create_dir_all(&root).map_err(|e| AgentpackError::io(&root, e))?;
    seed_cursor_root(&root)?;
    let pack_plugin = staging_cursor_pack_plugin_dir(project_root)?;
    fs::create_dir_all(&pack_plugin).map_err(|e| AgentpackError::io(&pack_plugin, e))?;
    write_cursor_pack_plugin_manifests(&root)?;
    stage_pack_plugins_for_target(lock, &pack_plugin, HarnessTarget::Cursor, manifest)?;
    stage_pack_skills_for_target(lock, &pack_plugin, HarnessTarget::Cursor, manifest)?;
    write_cursor_pack_plugin_readme(&pack_plugin)?;
    Ok(())
}

/// Runs after pack **and** dot-agents overlay so staged **`agents/`** reflects merged content.
pub(super) fn finalize_cursor_staging(project_root: &Path) -> Result<()> {
    materialize_cursor_fake_home(project_root)?;
    let cursor_overlay = materialize_workspace_cursor_agents_symlink(project_root)?;
    write_cursor_overlay_manifest(project_root, &cursor_overlay)?;
    Ok(())
}
