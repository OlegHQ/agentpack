use std::fs;
use std::path::{Path, PathBuf};

use crate::artifacts::{parse_markdown_artifact, staged_skill_support_path, HarnessTarget};
use crate::cache::{cache_entry_dir, cache_has_plugin_manifest};
use crate::error::{AgentpackError, Result};
use crate::lockfile::{LockPlugin, LockSkill, PackLock};
use crate::manifest::AgentpackManifest;

use super::json_local::write_text_file;
use super::tree::copy_merge_tree;

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
    let subdirs = target.raw_plugin_subdirs();
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
    if target.stages_plugin_root_mcp_json() {
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

pub(super) fn stage_pack_plugins_for_target(
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

pub(super) fn stage_pack_skills_for_target(
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

pub(crate) fn entry_short_id(cache_key: &str) -> String {
    cache_key.chars().take(16).collect()
}

pub(super) fn skill_folder_name(skill: &LockSkill) -> String {
    if skill.path.is_empty() {
        return skill.repo.clone();
    }
    Path::new(&skill.path)
        .file_name()
        .map(|s| s.to_string_lossy().into_owned())
        .unwrap_or_else(|| skill.repo.clone())
}

pub(super) fn skill_disabled_in_config(lock: &PackLock, skill: &LockSkill) -> bool {
    let sid = entry_short_id(&skill.cache_key);
    lock.config
        .disabled_plugins
        .iter()
        .any(|id| id == &skill.cache_key || id == &sid)
}

pub(super) fn plugin_disabled_in_config(lock: &PackLock, plugin: &LockPlugin) -> bool {
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
