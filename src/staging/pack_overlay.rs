use std::collections::BTreeMap;
use std::fs;
use std::path::{Path, PathBuf};

use rayon::prelude::*;

use crate::artifacts::{parse_markdown_artifact, staged_skill_support_path};
use crate::cache::{cache_entry_dir, cache_has_plugin_manifest};
use crate::error::{AgentpackError, Result};
use crate::harness::HarnessTarget;
use crate::lockfile::{LockPackage, PackLock, PackageKind};
use crate::mode::filter::EffectiveMode;

use crate::fs_util::{fast_copy_file, strip_under_root, walk_dir, write_text_file, WalkDirOpts};

fn walk_source_files<F>(root: &Path, visitor: &mut F) -> Result<()>
where
    F: FnMut(&Path, &Path) -> Result<()>,
{
    for entry in walk_dir(root, WalkDirOpts::files()) {
        let entry = entry?;
        if !entry.file_type().is_file() {
            continue;
        }
        let path = entry.path();
        let rel = strip_under_root(path, root)?;
        visitor(path, rel)?;
    }
    Ok(())
}

/// Each harness's pack-content root, paired with its id — derived from the registry
/// (`harness::all()` + `staged_root`) by the pipeline, so this single-walk pass fans out to all
/// harnesses without any harness-specific knowledge here.
pub(super) type PackHarnessRoots<'a> = [(HarnessTarget, &'a Path)];

/// One walk over **`src_root`**: copy skill support files and read each markdown artifact once, then
/// render per harness. Avoids repeating directory walks and YAML/markdown parsing for every target
/// (previously ~4× I/O and CPU per plugin).
fn stage_source_tree_all_harnesses(
    src_root: &Path,
    dests: &PackHarnessRoots<'_>,
    bare_skill_name: Option<&str>,
    module: &str,
    mode: &EffectiveMode,
) -> Result<()> {
    if !src_root.is_dir() {
        return Ok(());
    }

    walk_source_files(src_root, &mut |src, rel| {
        if !mode.allows_package_path(module, rel)? {
            return Ok(());
        }
        if let Some(dest_rel) = staged_skill_support_path(rel, bare_skill_name) {
            for (_, dest_root) in dests {
                fast_copy_file(src, &dest_root.join(&dest_rel))?;
            }
            return Ok(());
        }

        let ext = src.extension().and_then(|e| e.to_str()).unwrap_or("");
        if !ext.eq_ignore_ascii_case("md") && !ext.eq_ignore_ascii_case("mdc") {
            return Ok(());
        }

        let contents = fs::read_to_string(src).map_err(|e| AgentpackError::io(src, e))?;
        // Third-party packs are untrusted input: a single unparseable frontmatter must not abort
        // staging for every harness. Warn and skip the offending file instead of propagating.
        let parsed = match parse_markdown_artifact(rel, &contents, bare_skill_name) {
            Ok(p) => p,
            Err(e) => {
                tracing::warn!(
                    source = %rel.display(),
                    error = %e,
                    "skipping markdown artifact with invalid frontmatter"
                );
                return Ok(());
            }
        };
        if let Some(artifact) = parsed {
            for (target, dest_root) in dests {
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

/// Walk each unique raw-plugin support subdir **once** and fan out files to every harness
/// destination that wants them. Previously each of the four targets was walked independently,
/// so the same cache tree was `read_dir`+`stat`ed up to **4×** per plugin. This collapses it to
/// a single traversal with per-file fast copies.
fn copy_raw_plugin_support_dirs_all_harnesses(
    src_root: &Path,
    dests: &PackHarnessRoots<'_>,
    module: &str,
    mode: &EffectiveMode,
) -> Result<()> {
    // Build subdir → [dest_root per interested target] map. Preserve insertion order for
    // deterministic debug logs; BTreeMap also gives consistent ordering across runs.
    let mut subdir_dests: BTreeMap<&'static str, Vec<PathBuf>> = BTreeMap::new();
    for (target, dest_root) in dests {
        for sub in target.harness().raw_plugin_subdirs() {
            subdir_dests
                .entry(*sub)
                .or_default()
                .push(dest_root.to_path_buf());
        }
    }
    if subdir_dests.is_empty() {
        return Ok(());
    }

    // Different subdirs map to disjoint destination paths (`commands/**` vs `agents/**` vs …),
    // so we can walk them in parallel without risking write ordering issues.
    let entries: Vec<(&'static str, &Vec<PathBuf>)> =
        subdir_dests.iter().map(|(k, v)| (*k, v)).collect();
    entries
        .par_iter()
        .try_for_each(|(sub, dest_roots)| -> Result<()> {
            let s = src_root.join(sub);
            if !s.is_dir() {
                return Ok(());
            }
            walk_source_files(&s, &mut |src, rel| {
                let full_rel = Path::new(sub).join(rel);
                if !mode.allows_package_path(module, &full_rel)? {
                    return Ok(());
                }
                for dest_root in *dest_roots {
                    fast_copy_file(src, &dest_root.join(&full_rel))?;
                }
                Ok(())
            })
        })?;
    Ok(())
}

fn stage_bare_skill_cache_all_harnesses(
    cache_root: &Path,
    dests: &PackHarnessRoots<'_>,
    skill_name: &str,
    module: &str,
    mode: &EffectiveMode,
) -> Result<()> {
    stage_source_tree_all_harnesses(cache_root, dests, Some(skill_name), module, mode)
}

fn stage_plugin_cache_all_harnesses(
    cache_root: &Path,
    dests: &PackHarnessRoots<'_>,
    module: &str,
    mode: &EffectiveMode,
) -> Result<()> {
    copy_raw_plugin_support_dirs_all_harnesses(cache_root, dests, module, mode)?;
    // Plugin root `mcp.json` is collected centrally by `staging::mcp::stage_merged_mcp`.
    stage_source_tree_all_harnesses(cache_root, dests, None, module, mode)
}

pub(super) fn stage_pack_plugins_all_harnesses(
    lock: &PackLock,
    dests: &PackHarnessRoots<'_>,
    mode: &EffectiveMode,
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
        stage_plugin_cache_all_harnesses(&cache_path, dests, &plugin.module, mode)?;
    }
    Ok(())
}

pub(super) fn stage_pack_skills_all_harnesses(
    lock: &PackLock,
    dests: &PackHarnessRoots<'_>,
    mode: &EffectiveMode,
) -> Result<()> {
    let plugins: Vec<&LockPackage> = lock.plugins().collect();
    let mut skill_list: Vec<&LockPackage> = lock.skills().collect();
    skill_list.sort_by(|a, b| a.cache_key.cmp(&b.cache_key));

    // Each skill stages under `skills/<unique-name>/…` in every harness root, so staging can
    // run in parallel across skills without write-ordering risk. Plugins stay sequential to
    // preserve deterministic overlay order on rare path collisions.
    skill_list
        .par_iter()
        .try_for_each(|skill| -> Result<()> {
            if disabled_in_config(lock, skill) {
                tracing::info!(cache_key = %skill.cache_key, "skip disabled skill");
                return Ok(());
            }
            if skill_is_shadowed(skill, &plugins) {
                tracing::info!(
                    cache_key = %skill.cache_key,
                    path = %skill.path,
                    "skip skill: shadowed by full plugin"
                );
                return Ok(());
            }
            let cache_path = match cache_entry_dir(&skill.cache_key) {
                Ok(p) => p,
                Err(e) => {
                    tracing::warn!(cache_key = %skill.cache_key, error = %e, "skip skill staging: cache path");
                    return Ok(());
                }
            };
            let name = skill_folder_name(skill);
            if !cache_path.join("SKILL.md").is_file() {
                tracing::warn!(
                    path = %cache_path.display(),
                    "skip skill staging: SKILL.md missing"
                );
                return Ok(());
            }
            stage_bare_skill_cache_all_harnesses(&cache_path, dests, &name, &skill.module, mode)
        })?;
    Ok(())
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
    let sid = crate::fs_util::truncate_str(&pkg.cache_key, 16);
    lock.config
        .disabled_plugins
        .iter()
        .any(|id| id == &pkg.cache_key || id.as_str() == sid)
}

/// True when this skill path is already provided by a full plugin at the same commit.
pub fn skill_is_shadowed(skill: &LockPackage, plugins: &[&LockPackage]) -> bool {
    plugins
        .iter()
        .filter(|p| {
            p.kind == PackageKind::Plugin
                && !p.cache_key.is_empty()
                && !p.commit.is_empty()
                && !p.owner.is_empty()
                && !p.repo.is_empty()
        })
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
