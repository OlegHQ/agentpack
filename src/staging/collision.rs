//! Detect overlaps between the merged `agentpack-bundle` and the user's `~/.claude/` extension
//! dirs. Claude loads both, which produces duplicate slash commands (e.g. `/code-tutor` and
//! `/agentpack-bundle:code-tutor`). By default we drop the staged pack copy and keep `~/.claude`.

use std::collections::HashSet;
use std::env;
use std::fs;
use std::io::{self, IsTerminal};
use std::path::Path;

use walkdir::WalkDir;

use crate::error::{AgentpackError, Result};
use crate::fs_util::remove_path_any;

/// Skip stripping pack copies when both exist (duplicated slash UX remains).
const IGNORE_ENV: &str = "AGENTPACK_IGNORE_USER_BUNDLE_COLLISION";

/// Lowercase skill slugs removed from staged harness trees so `verify_staging` can skip them.
pub(super) struct StagingCollisionRemoval {
    pub skill_slugs_lower: HashSet<String>,
}

fn eprint_collision_warning(line: &str) {
    if io::stderr().is_terminal() {
        eprintln!("\x1b[33m⚠ {line}\x1b[0m");
    } else {
        eprintln!("warning: {line}");
    }
}

fn collect_skill_slugs(skills_dir: &Path, out: &mut HashSet<String>) -> Result<()> {
    if !skills_dir.is_dir() {
        return Ok(());
    }
    for e in fs::read_dir(skills_dir).map_err(|e| AgentpackError::io(skills_dir, e))? {
        let e = e.map_err(|e| AgentpackError::io(skills_dir, e))?;
        let p = e.path();
        if p.is_dir() {
            if let Some(name) = p.file_name().and_then(|s| s.to_str()) {
                out.insert(name.to_lowercase());
            }
        }
    }
    Ok(())
}

fn collect_md_stems_recursive(dir: &Path, out: &mut HashSet<String>) -> Result<()> {
    if !dir.is_dir() {
        return Ok(());
    }
    for e in fs::read_dir(dir).map_err(|e| AgentpackError::io(dir, e))? {
        let e = e.map_err(|e| AgentpackError::io(dir, e))?;
        let p = e.path();
        if p.is_dir() {
            collect_md_stems_recursive(&p, out)?;
        } else if p
            .extension()
            .map(|ext| ext.eq_ignore_ascii_case("md"))
            .unwrap_or(false)
        {
            if let Some(stem) = p.file_stem().and_then(|s| s.to_str()) {
                out.insert(stem.to_lowercase());
            }
        }
    }
    Ok(())
}

fn remove_skill_slug_dir(skills_root: &Path, slug_lower: &str) -> Result<()> {
    if !skills_root.is_dir() {
        return Ok(());
    }
    for e in fs::read_dir(skills_root).map_err(|e| AgentpackError::io(skills_root, e))? {
        let e = e.map_err(|e| AgentpackError::io(skills_root, e))?;
        let p = e.path();
        if p.is_dir() {
            if let Some(name) = p.file_name().and_then(|s| s.to_str()) {
                if name.to_lowercase() == slug_lower {
                    remove_path_any(&p)?;
                }
            }
        }
    }
    Ok(())
}

fn remove_md_stems_under_tree(root: &Path, stem_lower: &str) -> Result<()> {
    if !root.is_dir() {
        return Ok(());
    }
    for entry in WalkDir::new(root).follow_links(false).contents_first(true) {
        let entry = entry.map_err(|e| AgentpackError::Staging(e.to_string()))?;
        let p = entry.path();
        if !p.is_file() {
            continue;
        }
        if !p
            .extension()
            .map(|ext| ext.eq_ignore_ascii_case("md"))
            .unwrap_or(false)
        {
            continue;
        }
        let lower = p
            .file_stem()
            .and_then(|s| s.to_str())
            .map(|s| s.to_lowercase());
        if lower.as_deref() != Some(stem_lower) {
            continue;
        }
        remove_path_any(p)?;
    }
    Ok(())
}

/// Like **[`resolve_user_claude_bundle_collisions`]** but uses **`home_dir`** as the user profile
/// root (directory that contains **`.claude`**). **`None`** skips resolution (same as missing real home).
pub(super) fn resolve_user_claude_bundle_collisions_with_home(
    bundle: &Path,
    opencode: &Path,
    codex: &Path,
    cursor_pack: &Path,
    home_dir: Option<&Path>,
) -> Result<StagingCollisionRemoval> {
    let no_removals = || StagingCollisionRemoval {
        skill_slugs_lower: HashSet::new(),
    };

    if env::var(IGNORE_ENV).ok().as_deref() == Some("1") {
        tracing::warn!("skipping bundle vs ~/.claude collision handling ({IGNORE_ENV}=1)");
        return Ok(no_removals());
    }

    let Some(home) = home_dir else {
        return Ok(no_removals());
    };
    let uc = home.join(".claude");

    let mut user_skills = HashSet::new();
    collect_skill_slugs(&uc.join("skills"), &mut user_skills)?;
    let mut user_cmd = HashSet::new();
    collect_md_stems_recursive(&uc.join("commands"), &mut user_cmd)?;
    let mut user_agents = HashSet::new();
    collect_md_stems_recursive(&uc.join("agents"), &mut user_agents)?;

    if user_skills.is_empty() && user_cmd.is_empty() && user_agents.is_empty() {
        return Ok(no_removals());
    }

    let mut removed_skills = HashSet::new();
    let harness_roots = [bundle, opencode, codex, cursor_pack];

    let mut bundle_skills = HashSet::new();
    collect_skill_slugs(&bundle.join("skills"), &mut bundle_skills)?;
    let mut bundle_cmd = HashSet::new();
    collect_md_stems_recursive(&bundle.join("commands"), &mut bundle_cmd)?;
    let mut bundle_agents = HashSet::new();
    collect_md_stems_recursive(&bundle.join("agents"), &mut bundle_agents)?;

    // Skills: remove matching slug dirs from all harness roots.
    let mut skill_keys: Vec<&String> = user_skills.intersection(&bundle_skills).collect();
    skill_keys.sort();
    for k in skill_keys {
        removed_skills.insert(k.clone());
        eprint_collision_warning(&format!(
            "Using ~/.claude skill `{k}`; omitted pack duplicate from staged bundle (and other harness trees)"
        ));
        for root in &harness_roots {
            remove_skill_slug_dir(&root.join("skills"), k)?;
        }
    }

    // Commands and agents: remove matching .md stems from applicable harness roots.
    // Codex doesn't have commands/ or agents/ trees, so we skip it.
    let md_roots = [bundle, opencode, cursor_pack];
    for (user_set, bundle_set, dir_name, label) in [
        (&user_cmd, &bundle_cmd, "commands", "command"),
        (&user_agents, &bundle_agents, "agents", "agent"),
    ] {
        let mut keys: Vec<&String> = user_set.intersection(bundle_set).collect();
        keys.sort();
        for k in keys {
            eprint_collision_warning(&format!(
                "Using ~/.claude {label} `{k}`; omitted pack duplicate from staged bundle (and other harness trees)"
            ));
            for root in &md_roots {
                remove_md_stems_under_tree(&root.join(dir_name), k)?;
            }
        }
    }

    Ok(StagingCollisionRemoval {
        skill_slugs_lower: removed_skills,
    })
}

/// Remove staged pack copies that duplicate `~/.claude` skills / commands / agents so the user
/// install wins. Prints yellow warnings to stderr. When **`IGNORE_ENV=1`**, does nothing (duplicates
/// remain). Returns lowercase skill slugs removed from **`skills/`** for staging verification.
pub(super) fn resolve_user_claude_bundle_collisions(
    bundle: &Path,
    opencode: &Path,
    codex: &Path,
    cursor_pack: &Path,
) -> Result<StagingCollisionRemoval> {
    resolve_user_claude_bundle_collisions_with_home(
        bundle,
        opencode,
        codex,
        cursor_pack,
        dirs::home_dir().as_deref(),
    )
}

#[cfg(test)]
mod tests {
    use super::*;
    use tempfile::tempdir;

    #[test]
    fn no_user_dirs_no_op() {
        let t = tempdir().unwrap();
        fs::create_dir_all(t.path().join(".claude/skills")).unwrap();
        let b = t.path().join("b");
        fs::create_dir_all(&b).unwrap();
        assert!(super::resolve_user_claude_bundle_collisions_with_home(
            &b,
            &b,
            &b,
            &b,
            Some(t.path()),
        )
        .unwrap()
        .skill_slugs_lower
        .is_empty());
    }

    #[test]
    fn collision_removes_pack_skill() {
        let t = tempdir().unwrap();
        let uc = t.path().join(".claude/skills/code-tutor");
        fs::create_dir_all(&uc).unwrap();
        fs::write(uc.join("SKILL.md"), "---\nname: x\n---\n").unwrap();
        let bundle = t.path().join("bundle");
        let bundle_skill = bundle.join("skills/code-tutor");
        fs::create_dir_all(&bundle_skill).unwrap();
        fs::write(bundle_skill.join("SKILL.md"), "---\n---\n").unwrap();
        let op = t.path().join("op");
        fs::create_dir_all(op.join("skills/code-tutor")).unwrap();
        fs::write(op.join("skills/code-tutor/SKILL.md"), "x").unwrap();

        let r = super::resolve_user_claude_bundle_collisions_with_home(
            &bundle,
            &op,
            &op,
            &op,
            Some(t.path()),
        )
        .unwrap();
        assert!(r.skill_slugs_lower.contains("code-tutor"));
        assert!(!bundle_skill.join("SKILL.md").exists());
        assert!(!op.join("skills/code-tutor/SKILL.md").exists());
        assert!(uc.join("SKILL.md").is_file());
    }

    #[test]
    fn collision_removes_pack_agent_md() {
        let t = tempdir().unwrap();
        let user_ag = t.path().join(".claude/agents/foo.md");
        fs::create_dir_all(user_ag.parent().unwrap()).unwrap();
        fs::write(&user_ag, "---\n---\n").unwrap();
        let bundle = t.path().join("bundle");
        fs::create_dir_all(bundle.join("agents")).unwrap();
        fs::write(bundle.join("agents/foo.md"), "---\n---\n").unwrap();

        super::resolve_user_claude_bundle_collisions_with_home(
            &bundle,
            &bundle,
            &bundle,
            &bundle,
            Some(t.path()),
        )
        .unwrap();
        assert!(!bundle.join("agents/foo.md").exists());
        assert!(user_ag.is_file());
    }
}
