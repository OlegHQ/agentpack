use std::collections::{BTreeMap, BTreeSet};
use std::fs;
use std::path::{Path, PathBuf};

use walkdir::WalkDir;

use crate::artifacts::HarnessTarget;
use crate::error::{AgentpackError, Result};
use crate::fs_util::{fast_copy_file, write_json_value};
use crate::mode::filter::EffectiveMode;

use super::ir::{HookBundle, HookLayer, HookOrigin, HookOutputTarget, NormalizedHook};
use super::runtime::bridge::HookExecutionSpec;

fn base_asset_root(target: HarnessTarget, target_root: &Path) -> PathBuf {
    match target {
        HarnessTarget::OpenCode => target_root.join("plugins/agentpack-hooks/assets"),
        HarnessTarget::Claude | HarnessTarget::Cursor | HarnessTarget::Codex => {
            target_root.join("hooks/_packages")
        }
    }
}

fn origin_allows_path(mode: &EffectiveMode, origin: &HookOrigin, rel: &Path) -> Result<bool> {
    match origin.layer {
        HookLayer::SeededNative => Ok(true),
        HookLayer::PackPlugin | HookLayer::BareSkill => {
            mode.allows_package_path(&origin.module, rel)
        }
        HookLayer::DotAgents => mode.allows_dot_agents_path(rel),
    }
}

fn copy_filtered_tree(
    src_root: &Path,
    dst_root: &Path,
    origin: &HookOrigin,
    mode: &EffectiveMode,
) -> Result<()> {
    if !src_root.is_dir() {
        return Ok(());
    }
    fs::create_dir_all(dst_root).map_err(|err| AgentpackError::io(dst_root, err))?;
    let walker = WalkDir::new(src_root).follow_links(false).into_iter();
    for entry in walker.filter_map(|entry| entry.ok()) {
        let path = entry.path();
        let rel = path.strip_prefix(src_root).map_err(|_| {
            AgentpackError::Staging(format!("path outside source root {}", path.display()))
        })?;
        if rel.as_os_str().is_empty() || !origin_allows_path(mode, origin, rel)? {
            continue;
        }
        if entry.file_type().is_dir() {
            fs::create_dir_all(dst_root.join(rel))
                .map_err(|err| AgentpackError::io(dst_root.join(rel), err))?;
            continue;
        }
        if entry.file_type().is_file() {
            fast_copy_file(path, &dst_root.join(rel))?;
        }
    }
    Ok(())
}

pub fn stage_origin_packages(
    bundle: &HookBundle,
    target: HarnessTarget,
    target_root: &Path,
    mode: &EffectiveMode,
) -> Result<BTreeMap<String, PathBuf>> {
    let mut roots = BTreeMap::new();
    let mut seen = BTreeSet::new();
    for hook in &bundle.hooks {
        if hook.origin.layer == super::ir::HookLayer::SeededNative {
            continue;
        }
        if !seen.insert(hook.origin.source_id()) {
            continue;
        }
        let package_root = base_asset_root(target, target_root)
            .join(&hook.origin.package_key)
            .join("package");
        copy_filtered_tree(&hook.origin.source_root, &package_root, &hook.origin, mode)?;
        roots.insert(hook.origin.package_key.clone(), package_root);
    }
    Ok(roots)
}

pub fn spec_path_for_hook(
    target: HarnessTarget,
    target_root: &Path,
    hook: &NormalizedHook,
) -> PathBuf {
    base_asset_root(target, target_root)
        .join(&hook.origin.package_key)
        .join("specs")
        .join(format!(
            "{:03}-{:03}-{:03}.json",
            hook.origin.event_index, hook.origin.matcher_group_index, hook.origin.hook_index
        ))
}

pub fn write_hook_spec(path: &Path, spec: &HookExecutionSpec) -> Result<()> {
    let value = serde_json::to_value(spec)
        .map_err(|err| AgentpackError::Staging(format!("serialize hook spec: {err}")))?;
    write_json_value(path, &value)
}

pub fn hook_exec_command(kind: &str, target: HookOutputTarget, spec_path: &Path) -> String {
    format!(
        "agentpack hook-exec {kind} --target {} --spec {}",
        target.as_str(),
        shell_escape::escape(spec_path.to_string_lossy())
    )
}

/// Command line for the host-side matcher router (Cursor emulation). One entry per event is
/// registered in the harness's hooks file; the router reads stdin, iterates specs under
/// `specs_dir`, and fires those whose stored Claude matcher matches the invoked tool.
pub fn hook_dispatch_command(target: HookOutputTarget, event: &str, specs_dir: &Path) -> String {
    format!(
        "agentpack hook-exec dispatch --target {} --event {event} --specs-dir {}",
        target.as_str(),
        shell_escape::escape(specs_dir.to_string_lossy())
    )
}

/// Root directory that `stage_origin_packages` populates per-harness; the router uses this as
/// its `--specs-dir` and walks for `*.json` spec files.
pub fn specs_dispatch_root(target: HarnessTarget, target_root: &Path) -> PathBuf {
    base_asset_root(target, target_root)
}

pub fn staged_package_root<'a>(
    staged_packages: &'a BTreeMap<String, PathBuf>,
    origin: &HookOrigin,
) -> Result<&'a Path> {
    staged_packages
        .get(&origin.package_key)
        .map(PathBuf::as_path)
        .ok_or_else(|| {
            AgentpackError::Staging(format!(
                "missing staged hook package root for {} ({})",
                origin.module, origin.package_key
            ))
        })
}
