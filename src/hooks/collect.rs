use std::path::{Path, PathBuf};

use crate::cache::cache_entry_dir;
use crate::error::Result;
use crate::lockfile::{LockPackage, PackLock};
use crate::manifest::AgentpackManifest;
use crate::paths::project_dot_agents_dir;
use crate::staging::{rel_is_disabled, skill_is_shadowed};

use super::ir::{HookBundle, HookLayer, HookOrigin};
use super::merge::sort_bundle;
use super::parse::{parse_claude_hooks, parse_codex_hooks};

fn disabled_paths_for_module<'a>(
    manifest: Option<&'a AgentpackManifest>,
    module: &str,
) -> &'a [String] {
    manifest
        .map(|manifest| manifest.disable_paths_for_module(module))
        .unwrap_or(&[])
}

fn package_key(cache_key: Option<&str>, module: &str, layer: HookLayer) -> String {
    if let Some(cache_key) = cache_key.filter(|value| !value.is_empty()) {
        return cache_key.to_string();
    }
    let prefix = match layer {
        HookLayer::SeededNative => "seeded",
        HookLayer::PackPlugin => "plugin",
        HookLayer::BareSkill => "skill",
        HookLayer::DotAgents => "dot-agents",
    };
    let slug = module
        .chars()
        .map(|ch| if ch.is_ascii_alphanumeric() { ch } else { '-' })
        .collect::<String>();
    format!("{prefix}-{slug}")
}

fn parse_source_file(
    layer: HookLayer,
    module: String,
    cache_key: Option<String>,
    source_root: PathBuf,
    source_file: PathBuf,
    source_rel: &str,
    disabled_paths: Vec<String>,
) -> Result<HookBundle> {
    let value = crate::fs_util::read_json_value(&source_file)?;
    let origin = HookOrigin {
        layer,
        module: module.clone(),
        cache_key: cache_key.clone(),
        source_rel: source_rel.to_string(),
        package_key: package_key(cache_key.as_deref(), &module, layer),
        source_root,
        source_file: source_file.clone(),
        disabled_paths,
        event_index: 0,
        matcher_group_index: 0,
        hook_index: 0,
    };
    match layer {
        HookLayer::SeededNative => parse_codex_hooks(&source_file, &value, &origin),
        HookLayer::PackPlugin | HookLayer::BareSkill | HookLayer::DotAgents => {
            parse_claude_hooks(&source_file, &value, &origin)
        }
    }
}

fn collect_from_packages(
    packages: &[&LockPackage],
    layer: HookLayer,
    manifest: Option<&AgentpackManifest>,
    bundle: &mut HookBundle,
) -> Result<()> {
    for pkg in packages {
        if pkg.cache_key.is_empty() {
            continue;
        }
        let disabled_paths = disabled_paths_for_module(manifest, &pkg.module).to_vec();
        if rel_is_disabled(Path::new("hooks"), &disabled_paths)
            || rel_is_disabled(Path::new("hooks/hooks.json"), &disabled_paths)
        {
            continue;
        }
        let root = cache_entry_dir(&pkg.cache_key)?;
        let hooks_path = root.join("hooks/hooks.json");
        if !hooks_path.is_file() {
            continue;
        }
        let parsed = parse_source_file(
            layer,
            pkg.module.clone(),
            Some(pkg.cache_key.clone()),
            root,
            hooks_path,
            "hooks/hooks.json",
            disabled_paths,
        )?;
        bundle.hooks.extend(parsed.hooks);
    }
    Ok(())
}

pub fn collect_hooks(
    project_root: &Path,
    lock: &PackLock,
    manifest: Option<&AgentpackManifest>,
    seeded_codex_hooks: Option<&Path>,
) -> Result<HookBundle> {
    let mut bundle = HookBundle::default();

    if let Some(seed_path) = seeded_codex_hooks.filter(|path| path.is_file()) {
        let seed_root = seed_path
            .parent()
            .map(Path::to_path_buf)
            .unwrap_or_else(|| project_root.to_path_buf());
        let parsed = parse_source_file(
            HookLayer::SeededNative,
            "seeded-codex-user".to_string(),
            None,
            seed_root,
            seed_path.to_path_buf(),
            "hooks.json",
            Vec::new(),
        )?;
        bundle.hooks.extend(parsed.hooks);
    }

    let mut plugins: Vec<&LockPackage> = lock.plugins().collect();
    plugins.sort_by(|a, b| a.module.cmp(&b.module));
    collect_from_packages(&plugins, HookLayer::PackPlugin, manifest, &mut bundle)?;

    let mut skills: Vec<&LockPackage> = lock.skills().collect();
    skills.sort_by(|a, b| a.module.cmp(&b.module));
    let skills: Vec<&LockPackage> = skills
        .into_iter()
        .filter(|skill| !skill_is_shadowed(skill, &plugins))
        .collect();
    collect_from_packages(&skills, HookLayer::BareSkill, manifest, &mut bundle)?;

    let dot_agents = project_dot_agents_dir(project_root);
    let dot_hooks = dot_agents.join("hooks/hooks.json");
    if dot_hooks.is_file() {
        let parsed = parse_source_file(
            HookLayer::DotAgents,
            "dot-agents".to_string(),
            None,
            dot_agents,
            dot_hooks,
            "hooks/hooks.json",
            Vec::new(),
        )?;
        bundle.hooks.extend(parsed.hooks);
    }

    sort_bundle(&mut bundle);
    Ok(bundle)
}
