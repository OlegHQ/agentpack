use std::fs;
use std::path::Path;

use walkdir::WalkDir;

use crate::artifacts::{parse_markdown_artifact, staged_skill_support_path, HarnessTarget};
use crate::cache::{cache_entry_dir, cache_has_plugin_manifest};
use crate::error::{AgentpackError, Result};
use crate::lockfile::{LockPackage, PackLock, PackageKind};
use crate::manifest::AgentpackManifest;

use crate::fs_util::write_text_file;

use super::tree::copy_merge_tree;

fn walk_source_files<F>(root: &Path, visitor: &mut F) -> Result<()>
where
    F: FnMut(&Path, &Path) -> Result<()>,
{
    for entry in WalkDir::new(root).follow_links(false) {
        let entry = entry.map_err(|e| AgentpackError::Staging(e.to_string()))?;
        if !entry.file_type().is_file() {
            continue;
        }
        let path = entry.path();
        let rel = path.strip_prefix(root).map_err(|_| {
            AgentpackError::Staging(format!("path outside root: {}", path.display()))
        })?;
        visitor(path, rel)?;
    }
    Ok(())
}

fn rel_key(rel: &Path) -> String {
    rel.to_string_lossy()
        .replace(std::path::MAIN_SEPARATOR, "/")
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

fn disable_list_for_entry<'a>(
    manifest: Option<&'a AgentpackManifest>,
    module: &str,
) -> &'a [String] {
    if module.is_empty() {
        return &[];
    }
    manifest
        .map(|m| m.disable_paths_for_module(module))
        .unwrap_or(&[])
}

/// Destination roots for the four harness trees that receive merged pack content.
pub(super) struct PackHarnessRoots<'a> {
    pub claude_bundle: &'a Path,
    pub opencode: &'a Path,
    pub codex: &'a Path,
    pub cursor_pack: &'a Path,
}

impl PackHarnessRoots<'_> {
    fn targets_and_roots(&self) -> [(HarnessTarget, &Path); 4] {
        [
            (HarnessTarget::Claude, self.claude_bundle),
            (HarnessTarget::OpenCode, self.opencode),
            (HarnessTarget::Codex, self.codex),
            (HarnessTarget::Cursor, self.cursor_pack),
        ]
    }
}

/// One walk over **`src_root`**: copy skill support files and read each markdown artifact once, then
/// render per harness. Avoids repeating directory walks and YAML/markdown parsing for every target
/// (previously ~4× I/O and CPU per plugin).
fn stage_source_tree_all_harnesses(
    src_root: &Path,
    dests: &PackHarnessRoots<'_>,
    bare_skill_name: Option<&str>,
    disabled: &[String],
) -> Result<()> {
    if !src_root.is_dir() {
        return Ok(());
    }

    let pairs = dests.targets_and_roots();
    walk_source_files(src_root, &mut |src, rel| {
        if rel_is_disabled(rel, disabled) {
            return Ok(());
        }
        if let Some(dest_rel) = staged_skill_support_path(rel, bare_skill_name) {
            for (_, dest_root) in &pairs {
                copy_merge_tree(src, &dest_root.join(&dest_rel))?;
            }
            return Ok(());
        }

        let ext = src.extension().and_then(|e| e.to_str()).unwrap_or("");
        if !ext.eq_ignore_ascii_case("md") && !ext.eq_ignore_ascii_case("mdc") {
            return Ok(());
        }

        let contents = fs::read_to_string(src).map_err(|e| AgentpackError::io(src, e))?;
        if let Some(artifact) = parse_markdown_artifact(rel, &contents, bare_skill_name)? {
            for (target, dest_root) in &pairs {
                tracing::debug!(
                    source = %rel.display(),
                    kind = ?artifact.kind,
                    source_variant = ?artifact.source_variant,
                    target = ?target,
                    "rendering staged markdown artifact"
                );
                let rendered = artifact.render(*target);
                write_text_file(&dest_root.join(rendered.relative_path), &rendered.contents)?;
            }
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
                walk_source_files(&s, &mut |src, rel| {
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

fn stage_bare_skill_cache_all_harnesses(
    cache_root: &Path,
    dests: &PackHarnessRoots<'_>,
    skill_name: &str,
    disabled: &[String],
) -> Result<()> {
    stage_source_tree_all_harnesses(cache_root, dests, Some(skill_name), disabled)
}

fn stage_plugin_cache_all_harnesses(
    cache_root: &Path,
    dests: &PackHarnessRoots<'_>,
    disabled: &[String],
) -> Result<()> {
    for (target, root) in dests.targets_and_roots() {
        copy_raw_plugin_support_dirs(cache_root, root, target, disabled)?;
        if target.stages_plugin_root_mcp_json() {
            copy_plugin_root_file_if_present(cache_root, root, "mcp.json", disabled)?;
        }
    }
    stage_source_tree_all_harnesses(cache_root, dests, None, disabled)
}

pub(super) fn stage_pack_plugins_all_harnesses(
    lock: &PackLock,
    dests: &PackHarnessRoots<'_>,
    manifest: Option<&AgentpackManifest>,
) -> Result<()> {
    let mut plug_list: Vec<&LockPackage> = lock.plugins().collect();
    plug_list.sort_by(|a, b| a.cache_key.cmp(&b.cache_key));
    for plugin in plug_list {
        if plugin.cache_key.is_empty() {
            tracing::warn!("skipping plugin staging: empty cache_key (run sync to backfill)");
            continue;
        }
        if disabled_in_config(lock, plugin) {
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
        stage_plugin_cache_all_harnesses(&cache_path, dests, disabled)?;
    }
    Ok(())
}

pub(super) fn stage_pack_skills_all_harnesses(
    lock: &PackLock,
    dests: &PackHarnessRoots<'_>,
    manifest: Option<&AgentpackManifest>,
) -> Result<()> {
    let plugins: Vec<&LockPackage> = lock.plugins().collect();
    let mut skill_list: Vec<&LockPackage> = lock.skills().collect();
    skill_list.sort_by(|a, b| a.cache_key.cmp(&b.cache_key));
    for skill in skill_list {
        if disabled_in_config(lock, skill) {
            tracing::info!(cache_key = %skill.cache_key, "skip disabled skill");
            continue;
        }
        if skill_is_shadowed(skill, &plugins) {
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
        stage_bare_skill_cache_all_harnesses(&cache_path, dests, &name, disabled)?;
    }
    Ok(())
}

fn entry_short_id(cache_key: &str) -> String {
    crate::fs_util::truncate_str(cache_key, 16)
}

pub(crate) fn skill_folder_name(pkg: &LockPackage) -> String {
    if pkg.path.is_empty() {
        return pkg.repo.clone();
    }
    Path::new(&pkg.path)
        .file_name()
        .map(|s| s.to_string_lossy().into_owned())
        .unwrap_or_else(|| pkg.repo.clone())
}

/// Check if a package is disabled in the lock config.
pub(super) fn disabled_in_config(lock: &PackLock, pkg: &LockPackage) -> bool {
    let sid = entry_short_id(&pkg.cache_key);
    lock.config
        .disabled_plugins
        .iter()
        .any(|id| id == &pkg.cache_key || id == &sid)
}

fn plugin_ready_for_shadowing(p: &LockPackage) -> bool {
    p.kind == PackageKind::Plugin
        && !p.cache_key.is_empty()
        && !p.commit.is_empty()
        && !p.owner.is_empty()
        && !p.repo.is_empty()
}

/// True when this skill path is already provided by a full plugin at the same commit.
pub fn skill_is_shadowed(skill: &LockPackage, plugins: &[&LockPackage]) -> bool {
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
