//! Staging: merge pack.lock trees into per-harness directories under `$STAGING`.

mod codex_auth;
mod collision;
mod constants;
mod cursor;
mod dot_agents;
mod harnesses;
mod pack_overlay;
mod seed;
mod tree;

use std::fs;
use std::path::{Path, PathBuf};

use crate::cache::cache_entry_dir;
use crate::error::{AgentpackError, Result};
use crate::lockfile::{LockPackage, PackLock};
use crate::manifest::AgentpackManifest;
use crate::paths::staging_plugins_dir;

pub(crate) use pack_overlay::rel_is_disabled;
pub(crate) use pack_overlay::skill_folder_name;
pub use pack_overlay::skill_is_shadowed;

use harnesses::StagingPipeline;
use pack_overlay::disabled_in_config;

/// Build one plugin tree: optional copies of user **`settings.json`** / **`.claude.json`**, then
/// plugin packages, then standalone skill packages. Later layers overwrite same relative paths
/// under extension dirs (`agents`, `commands`, …).
///
/// When **`manifest`** is set, **`[overrides.<module>.disable]`** paths are omitted from staging for that package.
pub fn rebuild_staging(
    project_root: &Path,
    lock: &PackLock,
    manifest: Option<&AgentpackManifest>,
) -> Result<Vec<PathBuf>> {
    StagingPipeline::new(project_root, lock, manifest).rebuild()
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
    let pipeline = StagingPipeline::new(project_root, lock, None);
    pipeline.verify()?;

    let dirs = list_plugin_dirs(project_root)?;
    if dirs.len() != 1 {
        return Err(AgentpackError::Staging(format!(
            "expected exactly one merged plugin dir (agentpack-bundle), got {}",
            dirs.len()
        )));
    }
    let bundle = &dirs[0];
    let opencode_root = pipeline.opencode_root()?;
    let codex_home = pipeline.codex_home()?;
    let cursor_pack = pipeline.cursor_pack_plugin_dir()?;

    let collision_removed = collision::resolve_user_claude_bundle_collisions(
        bundle,
        &opencode_root,
        &codex_home,
        &cursor_pack,
    )?;

    let harness_roots: &[(&Path, &str)] = &[
        (bundle, "bundle"),
        (&opencode_root, "opencode"),
        (&codex_home, "codex"),
        (&cursor_pack, "cursor"),
    ];

    let plugins: Vec<&LockPackage> = lock.plugins().collect();
    for skill in lock.skills() {
        if disabled_in_config(lock, skill) {
            continue;
        }
        if skill_is_shadowed(skill, &plugins) {
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
        if collision_removed
            .skill_slugs_lower
            .contains(&name.to_lowercase())
        {
            continue;
        }
        for (root, label) in harness_roots {
            let skill_md = root.join("skills").join(&name).join("SKILL.md");
            if !skill_md.is_file() {
                return Err(AgentpackError::Staging(format!(
                    "{label} staging missing skill SKILL.md {}",
                    skill_md.display()
                )));
            }
        }
    }

    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::lockfile::{LockPackage, PackageKind};
    use tree::copy_merge_tree;

    fn commit() -> String {
        "c".repeat(40)
    }

    fn make_skill(path: &str, cache_key: &str) -> LockPackage {
        LockPackage {
            module: "".into(),
            direct: true,
            kind: PackageKind::Skill,
            url: "".into(),
            owner: "a".into(),
            repo: "b".into(),
            path: path.into(),
            commit: commit(),
            cache_key: cache_key.into(),
            name: String::new(),
        }
    }

    fn make_plugin(path: &str, cache_key: &str) -> LockPackage {
        LockPackage {
            module: "".into(),
            direct: true,
            kind: PackageKind::Plugin,
            url: "".into(),
            owner: "a".into(),
            repo: "b".into(),
            path: path.into(),
            commit: commit(),
            cache_key: cache_key.into(),
            name: String::new(),
        }
    }

    #[test]
    fn skill_shadowed_when_under_plugin_path() {
        let skill = make_skill("plugins/foo/skills/bar", &"s".repeat(64));
        let plugin = make_plugin("plugins/foo", &"p".repeat(64));
        let plugins = vec![&plugin];
        assert!(skill_is_shadowed(&skill, &plugins));
        let skill2 = LockPackage {
            path: "plugins/other".into(),
            ..skill.clone()
        };
        assert!(!skill_is_shadowed(&skill2, &plugins));
    }

    #[test]
    #[cfg(unix)]
    fn copy_merge_tree_skips_dangling_symlink() {
        use std::fs;
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
        let skill = make_skill("any/nested", &"s".repeat(64));
        let plugin = make_plugin("", &"p".repeat(64));
        let plugins = vec![&plugin];
        assert!(skill_is_shadowed(&skill, &plugins));
    }
}
