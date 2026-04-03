use std::env;
use std::fs;
use std::io::ErrorKind;
use std::path::{Path, PathBuf};

use serde_json::Value;

use crate::artifacts::{parse_markdown_artifact, staged_skill_support_path, HarnessTarget};
use crate::cache::{cache_entry_dir, cache_has_plugin_manifest};
use crate::codex_auth;
use crate::collision;
use crate::error::{AgentpackError, Result};
use crate::lockfile::{LockPlugin, LockSkill, PackLock};
use crate::manifest::AgentpackManifest;
use crate::paths::{
    cursor_overlay_manifest_path, cursor_workspace_dir, staging_codex_home_dir,
    staging_cursor_bundle_dir, staging_cursor_home_dir, staging_cursor_pack_plugin_dir,
    staging_opencode_dir, staging_plugins_dir, STAGED_AGENTPACK_BUNDLE_NAME,
};

/// Claude-specific support directories we can copy raw from plugin caches.
const CLAUDE_RAW_PLUGIN_SUBDIRS: &[&str] = &["hooks", "matchers", "core", "examples", "utils"];

/// Cursor plugin directories copied raw from cached plugins (see `plugin_spec.md` / Cursor plugins reference).
const CURSOR_RAW_PLUGIN_SUBDIRS: &[&str] = &["hooks", "assets", "scripts"];

/// OpenCode config root entries we preserve before overlaying pack content.
const OPENCODE_USER_ROOT_ENTRIES: &[&str] = &[
    "opencode.json",
    "agents",
    "commands",
    "modes",
    "plugins",
    "skills",
];

/// Codex home entries we preserve before overlaying pack content.
const CODEX_HOME_ENTRIES: &[&str] = &["config.toml", "auth.json", "skills", "themes"];

/// Cursor files copied from `~/.cursor` into **`$STAGING/cursor/`** before pack overlay.
/// Omit **`agents` / `commands` / `skills` / `rules`**: those come from **`pack.lock`** under
/// **`agentpack-bundle/`**; copying from the real profile pulls dangling symlinks and duplicates UX.
const CURSOR_USER_ROOT_ENTRIES: &[&str] = &["cli-config.json", "mcp.json"];

/// Top-level **`~/.cursor`** paths symlinked into **`$STAGING/cursor-home/.cursor`** for Cursor Agent auth/session.
const CURSOR_FAKE_HOME_CREDENTIAL_FILES: &[&str] = &[
    "cli-config.json",
    "machineid",
    "agent-cli-state.json",
    "argv.json",
    "ide_state.json",
];

/// Symlinked into **`$FAKE_HOME/.cursor/User/`** so they resolve to the **same on-disk trees** Cursor’s GUI + CLI use for
/// workspace trust (`state.vscdb` under **`workspaceStorage`**) and global state — usually under **`Library/Application Support/Cursor/User`**
/// (macOS), not only **`~/.cursor/User`**.
const CURSOR_USER_SUBDIRS_IN_FAKE_HOME: &[&str] = &["globalStorage", "workspaceStorage"];

/// Pack plugin dirs symlinked from **`agentpack-bundle/`** into **`$STAGING/cursor-home/.cursor`**.
const CURSOR_FAKE_HOME_PACK_SUBDIRS: &[&str] = &[
    "commands", "agents", "skills", "rules", "hooks", "assets", "scripts",
];

/// Relative to **`./.cursor/`** — symlink **`./.cursor/agents`** → staged pack agents for Cursor **`agent`** (`computeAgentsDirs`).
const CURSOR_WORKSPACE_AGENTS_OVERLAY: &str = "agents";

fn copy_merge_tree(src: &Path, dst: &Path) -> Result<()> {
    let effective = match fs::symlink_metadata(src) {
        Ok(m) if m.file_type().is_symlink() => match fs::canonicalize(src) {
            Ok(p) => p,
            Err(e) => {
                tracing::warn!(
                    path = %src.display(),
                    error = %e,
                    "skipping dangling symlink while merging trees"
                );
                return Ok(());
            }
        },
        Ok(_) => src.to_path_buf(),
        Err(e) => return Err(AgentpackError::io(src, e)),
    };

    if effective.is_dir() {
        fs::create_dir_all(dst).map_err(|e| AgentpackError::io(dst, e))?;
        for e in fs::read_dir(&effective).map_err(|e| AgentpackError::io(&effective, e))? {
            let e = e.map_err(|e| AgentpackError::io(&effective, e))?;
            copy_merge_tree(&e.path(), &dst.join(e.file_name()))?;
        }
        return Ok(());
    }

    if let Some(parent) = dst.parent() {
        fs::create_dir_all(parent).map_err(|e| AgentpackError::io(parent, e))?;
    }
    if dst.exists() {
        tracing::debug!(
            from = %src.display(),
            to = %dst.display(),
            "bundle overlay overwrites path"
        );
    }
    fs::copy(&effective, dst).map_err(|e| AgentpackError::io(dst, e))?;
    Ok(())
}

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

fn read_json_file(path: &Path) -> Result<Option<Value>> {
    match fs::read_to_string(path) {
        Ok(s) => {
            let v = serde_json::from_str(&s).map_err(|e| {
                AgentpackError::Staging(format!("invalid JSON in {}: {e}", path.display()))
            })?;
            Ok(Some(v))
        }
        Err(e) if e.kind() == ErrorKind::NotFound => Ok(None),
        Err(e) => Err(AgentpackError::io(path, e)),
    }
}

fn write_json_file(path: &Path, value: &Value) -> Result<()> {
    if let Some(parent) = path.parent() {
        fs::create_dir_all(parent).map_err(|e| AgentpackError::io(parent, e))?;
    }
    let s = serde_json::to_string_pretty(value)
        .map_err(|e| AgentpackError::Staging(format!("serialize JSON: {e}")))?;
    fs::write(path, s).map_err(|e| AgentpackError::io(path, e))?;
    Ok(())
}

fn write_text_file(path: &Path, contents: &str) -> Result<()> {
    if let Some(parent) = path.parent() {
        fs::create_dir_all(parent).map_err(|e| AgentpackError::io(parent, e))?;
    }
    fs::write(path, contents).map_err(|e| AgentpackError::io(path, e))?;
    Ok(())
}

/// Copies **`~/.claude/settings.json`** and **`~/.claude.json`** into the bundle.
/// Does **not** copy `commands/`, `agents/`, `skills/`, etc. (those stay user-scoped so slash
/// commands are not duplicated under `(agentpack-bundle)`).
fn merge_user_settings_files_into_bundle(bundle: &Path) -> Result<()> {
    if !copy_user_settings_enabled() {
        return Ok(());
    }
    let Some(home) = dirs::home_dir() else {
        return Ok(());
    };

    let user_settings = home.join(".claude").join("settings.json");
    if let Some(v) = read_json_file(&user_settings)? {
        let dst = bundle.join(".claude").join("settings.json");
        write_json_file(&dst, &v)?;
        tracing::debug!(from = %user_settings.display(), "copied user settings.json into bundle");
    }

    let user_app = home.join(".claude.json");
    if let Some(v) = read_json_file(&user_app)? {
        let dst = bundle.join(".claude.json");
        write_json_file(&dst, &v)?;
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

fn walk_source_files<F>(root: &Path, current: &Path, visitor: &mut F) -> Result<()>
where
    F: FnMut(&Path, &Path) -> Result<()>,
{
    let dir = if current.as_os_str().is_empty() {
        root.to_path_buf()
    } else {
        root.join(current)
    };
    for entry in fs::read_dir(&dir).map_err(|e| AgentpackError::io(&dir, e))? {
        let entry = entry.map_err(|e| AgentpackError::io(&dir, e))?;
        let path = entry.path();
        let rel = if current.as_os_str().is_empty() {
            PathBuf::from(entry.file_name())
        } else {
            current.join(entry.file_name())
        };
        let file_type = entry
            .file_type()
            .map_err(|e| AgentpackError::io(&path, e))?;
        if file_type.is_dir() {
            walk_source_files(root, &rel, visitor)?;
        } else if file_type.is_file() {
            visitor(&path, &rel)?;
        }
    }
    Ok(())
}

fn rel_key(rel: &Path) -> String {
    rel.iter()
        .map(|c| c.to_string_lossy())
        .collect::<Vec<_>>()
        .join("/")
}

/// Paths are package-root-relative (forward slashes). A pattern matches the exact path or any file under it as a directory prefix.
fn rel_is_disabled(rel: &Path, disabled: &[String]) -> bool {
    if disabled.is_empty() {
        return false;
    }
    let rel_str = rel_key(rel);
    for raw in disabled {
        let d = raw.trim().replace('\\', "/");
        let d = d.trim_start_matches("./");
        if d.is_empty() {
            continue;
        }
        if rel_str == d || rel_str.starts_with(&format!("{d}/")) {
            return true;
        }
    }
    false
}

fn disable_list_for_entry(manifest: Option<&AgentpackManifest>, module: &str) -> Vec<String> {
    if module.is_empty() {
        return Vec::new();
    }
    manifest
        .map(|m| m.disable_paths_for_module(module))
        .unwrap_or_default()
}

fn stage_source_tree(
    src_root: &Path,
    dest_root: &Path,
    target: HarnessTarget,
    bare_skill_name: Option<&str>,
    disabled: &[String],
) -> Result<()> {
    if !src_root.is_dir() {
        return Ok(());
    }

    walk_source_files(src_root, Path::new(""), &mut |src, rel| {
        if rel_is_disabled(rel, disabled) {
            return Ok(());
        }
        if let Some(dest_rel) = staged_skill_support_path(rel, bare_skill_name) {
            copy_merge_tree(src, &dest_root.join(dest_rel))?;
            return Ok(());
        }

        let ext = src
            .extension()
            .and_then(|ext| ext.to_str())
            .unwrap_or_default();
        if ext != "md" && ext != "mdc" {
            return Ok(());
        }

        let contents = fs::read_to_string(src).map_err(|e| AgentpackError::io(src, e))?;
        if let Some(artifact) = parse_markdown_artifact(rel, &contents, bare_skill_name)? {
            tracing::debug!(
                source = %rel.display(),
                kind = ?artifact.kind,
                source_variant = ?artifact.source_variant,
                target = ?target,
                "rendering staged markdown artifact"
            );
            let rendered = artifact.render(target);
            write_text_file(&dest_root.join(rendered.relative_path), &rendered.contents)?;
        }
        Ok(())
    })
}

fn write_opencode_config_stub(root: &Path) -> Result<()> {
    let config_path = root.join("opencode.json");
    if config_path.exists() {
        return Ok(());
    }
    let value = serde_json::json!({
        "$schema": "https://opencode.ai/config.json"
    });
    write_json_file(&config_path, &value)
}

fn merge_named_subdirs(
    from_base: &Path,
    bundle: &Path,
    subdirs: &[&str],
    label: &'static str,
) -> Result<()> {
    if !from_base.is_dir() {
        return Ok(());
    }
    for sub in subdirs {
        let s = from_base.join(sub);
        if s.is_dir() {
            tracing::debug!(label, sub, "merging into bundle");
            copy_merge_tree(&s, &bundle.join(sub))?;
        }
    }
    Ok(())
}

fn copy_raw_plugin_support_dirs(
    src_root: &Path,
    dest_root: &Path,
    target: HarnessTarget,
    disabled: &[String],
) -> Result<()> {
    let subdirs: &[&str] = match target {
        HarnessTarget::Claude => CLAUDE_RAW_PLUGIN_SUBDIRS,
        HarnessTarget::Cursor => CURSOR_RAW_PLUGIN_SUBDIRS,
        HarnessTarget::OpenCode | HarnessTarget::Codex => &[],
    };
    if subdirs.is_empty() {
        return Ok(());
    }
    if disabled.is_empty() {
        merge_named_subdirs(src_root, dest_root, subdirs, "portable raw support")
    } else {
        for sub in subdirs {
            let s = src_root.join(sub);
            if s.is_dir() {
                walk_source_files(&s, Path::new(""), &mut |src, rel| {
                    let full_rel = Path::new(sub).join(rel);
                    if rel_is_disabled(&full_rel, disabled) {
                        return Ok(());
                    }
                    copy_merge_tree(src, &dest_root.join(&full_rel))?;
                    Ok(())
                })?;
            }
        }
        Ok(())
    }
}

fn copy_plugin_root_file_if_present(
    cache_root: &Path,
    dest_root: &Path,
    file_name: &str,
    disabled: &[String],
) -> Result<()> {
    if rel_is_disabled(Path::new(file_name), disabled) {
        return Ok(());
    }
    let src = cache_root.join(file_name);
    if src.is_file() {
        copy_merge_tree(&src, &dest_root.join(file_name))?;
    }
    Ok(())
}

fn stage_plugin_cache_for_target(
    cache_root: &Path,
    dest_root: &Path,
    target: HarnessTarget,
    disabled: &[String],
) -> Result<()> {
    copy_raw_plugin_support_dirs(cache_root, dest_root, target, disabled)?;
    if target == HarnessTarget::Cursor {
        copy_plugin_root_file_if_present(cache_root, dest_root, "mcp.json", disabled)?;
    }
    stage_source_tree(cache_root, dest_root, target, None, disabled)
}

fn stage_bare_skill_cache_for_target(
    cache_root: &Path,
    dest_root: &Path,
    target: HarnessTarget,
    skill_name: &str,
    disabled: &[String],
) -> Result<()> {
    stage_source_tree(cache_root, dest_root, target, Some(skill_name), disabled)
}

fn stage_pack_plugins_for_target(
    _project_root: &Path,
    lock: &PackLock,
    dest_root: &Path,
    target: HarnessTarget,
    manifest: Option<&AgentpackManifest>,
) -> Result<()> {
    let mut plug_list: Vec<&LockPlugin> = lock.plugins.iter().collect();
    plug_list.sort_by(|a, b| a.cache_key.cmp(&b.cache_key));
    for plugin in plug_list {
        if plugin.cache_key.is_empty() {
            tracing::warn!("skipping plugin staging: empty cache_key (run sync to backfill)");
            continue;
        }
        if plugin_disabled_in_config(lock, plugin) {
            tracing::info!(cache_key = %plugin.cache_key, "skip disabled plugin");
            continue;
        }
        let cache_path = match cache_entry_dir(&plugin.cache_key) {
            Ok(p) => p,
            Err(e) => {
                tracing::warn!(cache_key = %plugin.cache_key, error = %e, "skip plugin staging: cache path");
                continue;
            }
        };
        if !cache_has_plugin_manifest(&cache_path) {
            tracing::warn!(
                path = %cache_path.display(),
                "skip plugin staging: cache missing manifest"
            );
            continue;
        }
        let disabled = disable_list_for_entry(manifest, &plugin.module);
        stage_plugin_cache_for_target(&cache_path, dest_root, target, &disabled)?;
    }
    Ok(())
}

fn stage_pack_skills_for_target(
    _project_root: &Path,
    lock: &PackLock,
    dest_root: &Path,
    target: HarnessTarget,
    manifest: Option<&AgentpackManifest>,
) -> Result<()> {
    let mut skill_list: Vec<&LockSkill> = lock.skills.iter().collect();
    skill_list.sort_by(|a, b| a.cache_key.cmp(&b.cache_key));
    for skill in skill_list {
        if skill_disabled_in_config(lock, skill) {
            tracing::info!(cache_key = %skill.cache_key, "skip disabled skill");
            continue;
        }
        if skill_is_shadowed(skill, &lock.plugins) {
            tracing::info!(
                cache_key = %skill.cache_key,
                path = %skill.path,
                "skip skill: shadowed by full plugin"
            );
            continue;
        }
        let cache_path = match cache_entry_dir(&skill.cache_key) {
            Ok(p) => p,
            Err(e) => {
                tracing::warn!(cache_key = %skill.cache_key, error = %e, "skip skill staging: cache path");
                continue;
            }
        };
        let name = skill_folder_name(skill);
        let disabled = disable_list_for_entry(manifest, &skill.module);
        if !cache_path.join("SKILL.md").is_file() {
            tracing::warn!(
                path = %cache_path.display(),
                "skip skill staging: SKILL.md missing"
            );
            continue;
        }
        stage_bare_skill_cache_for_target(&cache_path, dest_root, target, &name, &disabled)?;
    }
    Ok(())
}

fn seed_opencode_root(root: &Path) -> Result<()> {
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

fn seed_codex_home(root: &Path) -> Result<()> {
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

fn seed_cursor_root(root: &Path) -> Result<()> {
    if !copy_user_settings_enabled() {
        return Ok(());
    }
    let Some(home) = dirs::home_dir() else {
        return Ok(());
    };
    let user_root = home.join(".cursor");
    copy_selected_entries(&user_root, root, CURSOR_USER_ROOT_ENTRIES)
}

fn remove_path_any(path: &Path) -> Result<()> {
    let meta = match fs::symlink_metadata(path) {
        Ok(meta) => meta,
        Err(err) if err.kind() == ErrorKind::NotFound => return Ok(()),
        Err(err) => return Err(AgentpackError::io(path, err)),
    };

    if meta.file_type().is_symlink() || meta.is_file() {
        fs::remove_file(path).map_err(|err| AgentpackError::io(path, err))?;
    } else if meta.is_dir() {
        fs::remove_dir_all(path).map_err(|err| AgentpackError::io(path, err))?;
    } else {
        fs::remove_file(path).map_err(|err| AgentpackError::io(path, err))?;
    }

    Ok(())
}

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

/// Symlink Cursor’s **Electron user-data** tree into the fake HOME. With **`HOME=$STAGING/cursor-home`**, macOS would otherwise use an empty
/// **`~/Library/Application Support/Cursor`** and the CLI would not see your login (state DB, cookies, **`machineid`**, etc.).
///
/// macOS also needs **`~/Library/Keychains`**: the bundled agent reads OAuth tokens from the login keychain via **`/usr/bin/security`**,
/// which resolves the default keychain using **`$HOME/Library/Keychains`**. Without this symlink, **`agent whoami`** and login storage see “not logged in”.
fn materialize_cursor_platform_user_data(fake_home: &Path, real_home: &Path) -> Result<()> {
    #[cfg(target_os = "macos")]
    {
        let real_keychains = real_home.join("Library/Keychains");
        if real_keychains.is_dir() {
            let dst = fake_home.join("Library/Keychains");
            if let Some(parent) = dst.parent() {
                fs::create_dir_all(parent).map_err(|e| AgentpackError::io(parent, e))?;
            }
            symlink_or_copy_into_fake_home(&real_keychains, &dst, true)?;
        }
        let real = real_home.join("Library/Application Support/Cursor");
        if real.is_dir() {
            let dst = fake_home.join("Library/Application Support/Cursor");
            if let Some(parent) = dst.parent() {
                fs::create_dir_all(parent).map_err(|e| AgentpackError::io(parent, e))?;
            }
            symlink_or_copy_into_fake_home(&real, &dst, true)?;
        }
    }
    #[cfg(target_os = "linux")]
    {
        let config_base = env::var_os("XDG_CONFIG_HOME")
            .map(PathBuf::from)
            .unwrap_or_else(|| real_home.join(".config"));
        let real = config_base.join("Cursor");
        if real.is_dir() {
            let fake_cfg_root = fake_home.join(".config");
            fs::create_dir_all(&fake_cfg_root)
                .map_err(|e| AgentpackError::io(&fake_cfg_root, e))?;
            let dst = fake_cfg_root.join("Cursor");
            symlink_or_copy_into_fake_home(&real, &dst, true)?;
        }
        let data_base = env::var_os("XDG_DATA_HOME")
            .map(PathBuf::from)
            .unwrap_or_else(|| real_home.join(".local/share"));
        let real_share = data_base.join("Cursor");
        if real_share.is_dir() {
            let fake_data_root = fake_home.join(".local/share");
            fs::create_dir_all(&fake_data_root)
                .map_err(|e| AgentpackError::io(&fake_data_root, e))?;
            let dst = fake_data_root.join("Cursor");
            symlink_or_copy_into_fake_home(&real_share, &dst, true)?;
        }
    }
    #[cfg(target_os = "windows")]
    {
        let real = real_home.join("AppData").join("Roaming").join("Cursor");
        if real.is_dir() {
            let dst = fake_home.join("AppData").join("Roaming").join("Cursor");
            if let Some(parent) = dst.parent() {
                fs::create_dir_all(parent).map_err(|e| AgentpackError::io(parent, e))?;
            }
            symlink_or_copy_into_fake_home(&real, &dst, true)?;
        }
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
        let config_base = env::var_os("XDG_CONFIG_HOME")
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

/// **`$STAGING/cursor-home`**: **`HOME`** for **`agentpack agent`**, with **`.cursor`** blending pack symlinks and real **`~/.cursor`** credential paths.
fn materialize_cursor_fake_home(project_root: &Path) -> Result<()> {
    let fake_home = staging_cursor_home_dir(project_root)?;
    if fake_home.exists() {
        fs::remove_dir_all(&fake_home).map_err(|e| AgentpackError::io(&fake_home, e))?;
    }
    let fake_cursor = fake_home.join(".cursor");
    fs::create_dir_all(&fake_cursor).map_err(|e| AgentpackError::io(&fake_cursor, e))?;

    let pack = staging_cursor_pack_plugin_dir(project_root)?;
    for sub in CURSOR_FAKE_HOME_PACK_SUBDIRS {
        let src = pack.join(sub);
        if !src.exists() {
            continue;
        }
        let as_dir = fs::metadata(&src)
            .map(|m| m.is_dir())
            .map_err(|e| AgentpackError::io(&src, e))?;
        symlink_or_copy_into_fake_home(&src, &fake_cursor.join(sub), as_dir)?;
    }

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
            for name in CURSOR_FAKE_HOME_CREDENTIAL_FILES {
                let src = rc.join(name);
                if !src.exists() {
                    continue;
                }
                let as_dir = fs::metadata(&src)
                    .map(|m| m.is_dir())
                    .map_err(|e| AgentpackError::io(&src, e))?;
                symlink_or_copy_into_fake_home(&src, &fake_cursor.join(name), as_dir)?;
            }
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

fn read_cursor_overlay_manifest(project_root: &Path) -> Result<Vec<PathBuf>> {
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
    write_cursor_overlay_manifest(project_root, &[])
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
    write_json_file(&marketplace_dir.join("marketplace.json"), &marketplace)?;

    let plugin_json: Value = serde_json::json!({
        "name": STAGED_AGENTPACK_BUNDLE_NAME,
        "displayName": "agentpack bundle",
        "version": "1.0.0",
        "description": "Merged plugins and skills from pack.lock (agentpack).",
        "author": { "name": "agentpack" },
        "license": "MIT",
        "keywords": ["agentpack", "pack.lock"]
    });
    write_json_file(&plugin_manifest_dir.join("plugin.json"), &plugin_json)?;
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

fn rebuild_opencode_staging(
    project_root: &Path,
    lock: &PackLock,
    manifest: Option<&AgentpackManifest>,
) -> Result<()> {
    let root = staging_opencode_dir(project_root)?;
    fs::create_dir_all(&root).map_err(|e| AgentpackError::io(&root, e))?;
    seed_opencode_root(&root)?;
    stage_pack_plugins_for_target(project_root, lock, &root, HarnessTarget::OpenCode, manifest)?;
    stage_pack_skills_for_target(project_root, lock, &root, HarnessTarget::OpenCode, manifest)?;
    Ok(())
}

fn rebuild_codex_home(
    project_root: &Path,
    lock: &PackLock,
    manifest: Option<&AgentpackManifest>,
) -> Result<()> {
    let root = staging_codex_home_dir(project_root)?;
    fs::create_dir_all(&root).map_err(|e| AgentpackError::io(&root, e))?;
    seed_codex_home(&root)?;
    stage_pack_plugins_for_target(project_root, lock, &root, HarnessTarget::Codex, manifest)?;
    stage_pack_skills_for_target(project_root, lock, &root, HarnessTarget::Codex, manifest)?;
    Ok(())
}

fn rebuild_cursor_staging(
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
    stage_pack_plugins_for_target(
        project_root,
        lock,
        &pack_plugin,
        HarnessTarget::Cursor,
        manifest,
    )?;
    stage_pack_skills_for_target(
        project_root,
        lock,
        &pack_plugin,
        HarnessTarget::Cursor,
        manifest,
    )?;
    write_cursor_pack_plugin_readme(&pack_plugin)?;
    materialize_cursor_fake_home(project_root)?;
    let cursor_overlay = materialize_workspace_cursor_agents_symlink(project_root)?;
    write_cursor_overlay_manifest(project_root, &cursor_overlay)?;
    Ok(())
}

pub fn entry_short_id(cache_key: &str) -> String {
    cache_key.chars().take(16).collect()
}

fn skill_folder_name(skill: &LockSkill) -> String {
    if skill.path.is_empty() {
        return skill.repo.clone();
    }
    Path::new(&skill.path)
        .file_name()
        .map(|s| s.to_string_lossy().into_owned())
        .unwrap_or_else(|| skill.repo.clone())
}

fn skill_disabled_in_config(lock: &PackLock, skill: &LockSkill) -> bool {
    let sid = entry_short_id(&skill.cache_key);
    lock.config
        .disabled_plugins
        .iter()
        .any(|id| id == &skill.cache_key || id == &sid)
}

fn plugin_disabled_in_config(lock: &PackLock, plugin: &LockPlugin) -> bool {
    let sid = entry_short_id(&plugin.cache_key);
    lock.config
        .disabled_plugins
        .iter()
        .any(|id| id == &plugin.cache_key || id == &sid)
}

fn plugin_ready_for_shadowing(p: &LockPlugin) -> bool {
    !p.cache_key.is_empty() && !p.commit.is_empty() && !p.owner.is_empty() && !p.repo.is_empty()
}

/// True when this skill path is already provided by a full plugin at the same commit.
pub fn skill_is_shadowed(skill: &LockSkill, plugins: &[LockPlugin]) -> bool {
    plugins
        .iter()
        .filter(|p| plugin_ready_for_shadowing(p))
        .any(|p| {
            if skill.owner != p.owner || skill.repo != p.repo || skill.commit != p.commit {
                return false;
            }
            let pref = p.path.trim_end_matches('/');
            let sp = skill.path.trim_end_matches('/');
            if pref.is_empty() {
                return true;
            }
            sp == pref || sp.starts_with(&format!("{pref}/"))
        })
}

fn write_bundle_manifest(bundle: &Path) -> Result<()> {
    let plugin_dir = bundle.join(".claude-plugin");
    fs::create_dir_all(&plugin_dir).map_err(|e| AgentpackError::io(&plugin_dir, e))?;
    let manifest = r#"{"name":"agentpack-bundle","version":"1.0.0","description":"Merged pack.lock plugins/skills; optional user settings.json and .claude.json"}"#;
    let pj = plugin_dir.join("plugin.json");
    fs::write(&pj, manifest).map_err(|e| AgentpackError::io(&pj, e))?;
    Ok(())
}

/// Build one plugin tree: optional copies of user **`settings.json`** / **`.claude.json`**, then
/// `[[plugins]]`, then bare `[[skills]]`. Later layers overwrite same relative paths under
/// extension dirs (`agents`, `commands`, …).
///
/// When **`manifest`** is set, **`[overrides.<module>.disable]`** paths are omitted from staging for that package.
pub fn rebuild_staging(
    project_root: &Path,
    lock: &PackLock,
    manifest: Option<&AgentpackManifest>,
) -> Result<Vec<PathBuf>> {
    for dir in [
        staging_plugins_dir(project_root)?,
        staging_opencode_dir(project_root)?,
        staging_codex_home_dir(project_root)?,
        staging_cursor_bundle_dir(project_root)?,
        staging_cursor_home_dir(project_root)?,
    ] {
        if dir.exists() {
            fs::remove_dir_all(&dir).map_err(|e| AgentpackError::io(&dir, e))?;
        }
    }

    let plugins_base = staging_plugins_dir(project_root)?;
    fs::create_dir_all(&plugins_base).map_err(|e| AgentpackError::io(&plugins_base, e))?;

    let bundle = plugins_base.join(crate::paths::STAGED_AGENTPACK_BUNDLE_NAME);
    fs::create_dir_all(&bundle).map_err(|e| AgentpackError::io(&bundle, e))?;
    write_bundle_manifest(&bundle)?;

    merge_user_settings_files_into_bundle(&bundle)?;

    stage_pack_plugins_for_target(project_root, lock, &bundle, HarnessTarget::Claude, manifest)?;
    stage_pack_skills_for_target(project_root, lock, &bundle, HarnessTarget::Claude, manifest)?;

    rebuild_opencode_staging(project_root, lock, manifest)?;
    rebuild_codex_home(project_root, lock, manifest)?;
    rebuild_cursor_staging(project_root, lock, manifest)?;

    Ok(vec![bundle])
}

/// Enumerate plugin directories after `rebuild_staging` / `sync`.
pub fn list_plugin_dirs(project_root: &Path) -> Result<Vec<PathBuf>> {
    let base = staging_plugins_dir(project_root)?;
    if !base.is_dir() {
        return Ok(Vec::new());
    }
    let mut dirs = Vec::new();
    for rd in fs::read_dir(&base).map_err(|e| AgentpackError::io(&base, e))? {
        let p = rd.map_err(|e| AgentpackError::io(&base, e))?.path();
        if p.join(".claude-plugin/plugin.json").is_file() {
            dirs.push(p);
        }
    }
    dirs.sort();
    Ok(dirs)
}

/// Ensure staging layout: exactly one bundle and cache integrity for lockfile entries.
pub fn verify_staging(project_root: &Path, lock: &PackLock) -> Result<()> {
    let dirs = list_plugin_dirs(project_root)?;
    if dirs.len() != 1 {
        return Err(AgentpackError::Staging(format!(
            "expected exactly one merged plugin dir (agentpack-bundle), got {}",
            dirs.len()
        )));
    }
    let bundle = &dirs[0];
    if !bundle.join(".claude-plugin/plugin.json").is_file() {
        return Err(AgentpackError::Staging(format!(
            "bundle missing manifest {}",
            bundle.display()
        )));
    }
    let opencode_root = staging_opencode_dir(project_root)?;
    if !opencode_root.is_dir() {
        return Err(AgentpackError::Staging(format!(
            "opencode staging missing {}",
            opencode_root.display()
        )));
    }
    let codex_home = staging_codex_home_dir(project_root)?;
    if !codex_home.is_dir() {
        return Err(AgentpackError::Staging(format!(
            "codex home staging missing {}",
            codex_home.display()
        )));
    }
    let cursor_bundle = staging_cursor_bundle_dir(project_root)?;
    if !cursor_bundle.is_dir() {
        return Err(AgentpackError::Staging(format!(
            "cursor staging missing {}",
            cursor_bundle.display()
        )));
    }
    let cursor_pack = staging_cursor_pack_plugin_dir(project_root)?;
    if !cursor_pack.join(".cursor-plugin/plugin.json").is_file() {
        return Err(AgentpackError::Staging(format!(
            "cursor pack plugin missing {}",
            cursor_pack.join(".cursor-plugin/plugin.json").display()
        )));
    }
    if !cursor_bundle
        .join(".cursor-plugin/marketplace.json")
        .is_file()
    {
        return Err(AgentpackError::Staging(format!(
            "cursor staging missing {}",
            cursor_bundle
                .join(".cursor-plugin/marketplace.json")
                .display()
        )));
    }
    let cursor_home = staging_cursor_home_dir(project_root)?;
    if !cursor_home.join(".cursor").is_dir() {
        return Err(AgentpackError::Staging(format!(
            "cursor fake home missing .cursor/ under {}",
            cursor_home.display()
        )));
    }

    for rel in read_cursor_overlay_manifest(project_root)? {
        let tracked = cursor_workspace_dir(project_root).join(&rel);
        if !tracked.exists() {
            return Err(AgentpackError::Staging(format!(
                "cursor workspace overlay missing at {} (from cursor-overlay.manifest entry {})",
                tracked.display(),
                rel.display()
            )));
        }
    }

    for plugin in &lock.plugins {
        if plugin.cache_key.is_empty() || plugin_disabled_in_config(lock, plugin) {
            continue;
        }
        let Ok(cache_root) = cache_entry_dir(&plugin.cache_key) else {
            continue;
        };
        if cache_has_plugin_manifest(&cache_root) {
            tracing::debug!(path = %cache_root.display(), "plugin cache present for verify");
        }
    }

    for skill in &lock.skills {
        if skill_disabled_in_config(lock, skill) {
            continue;
        }
        if skill_is_shadowed(skill, &lock.plugins) {
            continue;
        }
        let md = match cache_entry_dir(&skill.cache_key) {
            Ok(p) => p.join("SKILL.md"),
            Err(_) => continue,
        };
        if !md.is_file() {
            continue;
        }
        let name = skill_folder_name(skill);
        let bundled = bundle.join("skills").join(&name).join("SKILL.md");
        if !bundled.is_file() {
            return Err(AgentpackError::Staging(format!(
                "bundle missing skill SKILL.md {}",
                bundled.display()
            )));
        }
        let opencode_skill = opencode_root.join("skills").join(&name).join("SKILL.md");
        if !opencode_skill.is_file() {
            return Err(AgentpackError::Staging(format!(
                "opencode staging missing skill SKILL.md {}",
                opencode_skill.display()
            )));
        }
        let codex_skill = codex_home.join("skills").join(&name).join("SKILL.md");
        if !codex_skill.is_file() {
            return Err(AgentpackError::Staging(format!(
                "codex staging missing skill SKILL.md {}",
                codex_skill.display()
            )));
        }
        let cursor_skill = cursor_pack.join("skills").join(&name).join("SKILL.md");
        if !cursor_skill.is_file() {
            return Err(AgentpackError::Staging(format!(
                "cursor staging missing skill SKILL.md {}",
                cursor_skill.display()
            )));
        }
    }

    collision::verify_bundle_disjoint_from_user_claude(bundle)?;
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;

    fn commit() -> String {
        "c".repeat(40)
    }

    #[test]
    fn skill_shadowed_when_under_plugin_path() {
        let skill = LockSkill {
            module: "".into(),
            url: "".into(),
            owner: "a".into(),
            repo: "b".into(),
            path: "plugins/foo/skills/bar".into(),
            commit: commit(),
            cache_key: "s".repeat(64),
        };
        let plugin = LockPlugin {
            module: "".into(),
            name: "".into(),
            url: "".into(),
            owner: "a".into(),
            repo: "b".into(),
            path: "plugins/foo".into(),
            commit: commit(),
            cache_key: "p".repeat(64),
        };
        assert!(skill_is_shadowed(&skill, std::slice::from_ref(&plugin)));
        let skill2 = LockSkill {
            path: "plugins/other".into(),
            ..skill.clone()
        };
        assert!(!skill_is_shadowed(&skill2, &[plugin]));
    }

    #[test]
    #[cfg(unix)]
    fn copy_merge_tree_skips_dangling_symlink() {
        use std::os::unix::fs::symlink;

        let t = tempfile::tempdir().unwrap();
        let src = t.path().join("agents");
        fs::create_dir_all(&src).unwrap();
        symlink(
            "/this-path-should-not-exist-for-agentpack-test",
            src.join("code-simplifier.md"),
        )
        .unwrap();
        fs::write(src.join("ok.md"), "# ok").unwrap();
        let dst = t.path().join("out");
        copy_merge_tree(&src, &dst).unwrap();
        assert!(dst.join("ok.md").is_file());
        assert!(!dst.join("code-simplifier.md").exists());
    }

    #[test]
    fn skill_shadowed_when_repo_root_plugin() {
        let skill = LockSkill {
            module: "".into(),
            url: "".into(),
            owner: "a".into(),
            repo: "b".into(),
            path: "any/nested".into(),
            commit: commit(),
            cache_key: "s".repeat(64),
        };
        let plugin = LockPlugin {
            module: "".into(),
            name: "".into(),
            url: "".into(),
            owner: "a".into(),
            repo: "b".into(),
            path: "".into(),
            commit: commit(),
            cache_key: "p".repeat(64),
        };
        assert!(skill_is_shadowed(&skill, &[plugin]));
    }
}
