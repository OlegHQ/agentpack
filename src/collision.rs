//! Detect overlaps between the merged `agentpack-bundle` and the user's `~/.claude/` extension
//! dirs. Claude loads both, which produces duplicate slash commands (e.g. `/code-tutor` and
//! `/agentpack-bundle:code-tutor`).

use std::collections::HashSet;
use std::env;
use std::fs;
use std::path::Path;

use crate::error::{AgentpackError, Result};

/// Skip the bundle-vs-user collision check (not recommended).
const IGNORE_ENV: &str = "AGENTPACK_IGNORE_USER_BUNDLE_COLLISION";

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

fn collect_md_stems_under(dir: &Path, out: &mut HashSet<String>) -> Result<()> {
    if !dir.is_dir() {
        return Ok(());
    }
    collect_md_stems_recursive(dir, out)
}

fn collect_md_stems_recursive(dir: &Path, out: &mut HashSet<String>) -> Result<()> {
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

fn verify_inner(bundle: &Path, home: Option<&Path>) -> Result<()> {
    if env::var(IGNORE_ENV).ok().as_deref() == Some("1") {
        tracing::warn!("skipping bundle vs ~/.claude collision check ({IGNORE_ENV}=1)");
        return Ok(());
    }

    let Some(home) = home else {
        return Ok(());
    };
    let uc = home.join(".claude");

    let mut user_skills = HashSet::new();
    collect_skill_slugs(&uc.join("skills"), &mut user_skills)?;
    let mut user_cmd = HashSet::new();
    collect_md_stems_under(&uc.join("commands"), &mut user_cmd)?;
    let mut user_agents = HashSet::new();
    collect_md_stems_under(&uc.join("agents"), &mut user_agents)?;

    if user_skills.is_empty() && user_cmd.is_empty() && user_agents.is_empty() {
        return Ok(());
    }

    let mut bundle_skills = HashSet::new();
    collect_skill_slugs(&bundle.join("skills"), &mut bundle_skills)?;
    let mut bundle_cmd = HashSet::new();
    collect_md_stems_under(&bundle.join("commands"), &mut bundle_cmd)?;
    let mut bundle_agents = HashSet::new();
    collect_md_stems_under(&bundle.join("agents"), &mut bundle_agents)?;

    let mut problems: Vec<String> = Vec::new();
    for k in user_skills.intersection(&bundle_skills) {
        problems.push(format!(
            "skill `{k}` exists in both ~/.claude/skills and agentpack-bundle (duplicate slash entries)"
        ));
    }
    for k in user_cmd.intersection(&bundle_cmd) {
        problems.push(format!(
            "command `{k}` exists in both ~/.claude/commands and agentpack-bundle/commands"
        ));
    }
    for k in user_agents.intersection(&bundle_agents) {
        problems.push(format!(
            "agent `{k}` exists in both ~/.claude/agents and agentpack-bundle/agents"
        ));
    }

    if problems.is_empty() {
        return Ok(());
    }

    problems.sort();
    let hint = format!(
        "Resolve by removing the item from pack.lock, deleting or renaming it under ~/.claude, or set {IGNORE_ENV}=1 to skip this check."
    );
    Err(AgentpackError::Staging(format!(
        "bundle conflicts with user ~/.claude extension dirs:\n{}\n\n{}",
        problems.join("\n"),
        hint
    )))
}

/// Fail if the bundle exposes the same skill slugs or `.md` stems under `commands/` / `agents/`
/// as `~/.claude`, which almost always means duplicated slash UX.
pub fn verify_bundle_disjoint_from_user_claude(bundle: &Path) -> Result<()> {
    verify_inner(bundle, dirs::home_dir().as_deref())
}

#[cfg(test)]
mod tests {
    use super::*;
    use tempfile::tempdir;

    #[test]
    fn no_user_dirs_no_op() {
        let b = tempdir().unwrap();
        verify_inner(b.path(), Some(b.path())).unwrap();
    }

    #[test]
    fn collision_detected() {
        let t = tempdir().unwrap();
        let uc = t.path().join(".claude/skills/code-tutor");
        fs::create_dir_all(&uc).unwrap();
        fs::write(uc.join("SKILL.md"), "---\nname: x\n---\n").unwrap();
        let bundle_root = t.path().join("bundle");
        let bundle_skill = bundle_root.join("skills/code-tutor");
        fs::create_dir_all(&bundle_skill).unwrap();
        fs::write(bundle_skill.join("SKILL.md"), "---\n---\n").unwrap();

        let err = verify_inner(&bundle_root, Some(t.path())).unwrap_err();
        let s = err.to_string();
        assert!(s.contains("code-tutor"), "{s}");
    }
}
