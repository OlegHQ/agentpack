use std::fs;
use std::path::Path;

use walkdir::WalkDir;

use crate::error::{AgentpackError, Result};
use crate::fs_util::remove_path_any;
use crate::paths::{
    project_dot_agents_dir, staging_codex_home_dir, staging_plugins_dir,
    STAGED_AGENTPACK_BUNDLE_NAME,
};

use super::tree::copy_merge_tree;

/// Hard link when possible so Cursor sees real rule files ([dot-agents](https://github.com/dot-agents/dot-agents) documents
/// symlink issues for `.cursor/rules`); copy fallback (cross-device staging, Windows).
fn link_or_copy_for_dot_agent_file(src: &Path, dst: &Path) -> Result<()> {
    if let Some(parent) = dst.parent() {
        fs::create_dir_all(parent).map_err(|e| AgentpackError::io(parent, e))?;
    }
    if dst.exists() {
        remove_path_any(dst)?;
    }
    #[cfg(unix)]
    {
        if fs::hard_link(src, dst).is_ok() {
            return Ok(());
        }
    }
    fs::copy(src, dst).map_err(|e| AgentpackError::io(dst, e))?;
    Ok(())
}

/// Copy each **`.mdc`** under **`rules_root`** into **`dest_rules`**, flattened as **`dot-agents--<rel-with--slashes>`**.
fn merge_dot_agents_rules_mdc(rules_root: &Path, dest_rules: &Path) -> Result<()> {
    if !rules_root.is_dir() {
        return Ok(());
    }
    for e in WalkDir::new(rules_root).into_iter().filter_map(|r| r.ok()) {
        if !e.file_type().is_file() {
            continue;
        }
        let p = e.path();
        if !p.extension().is_some_and(|x| x.eq_ignore_ascii_case("mdc")) {
            continue;
        }
        let rel = p.strip_prefix(rules_root).map_err(|_| {
            AgentpackError::Staging(format!("rule path outside rules root ({})", p.display()))
        })?;
        let key = rel
            .to_string_lossy()
            .replace(std::path::MAIN_SEPARATOR, "--");
        let dest = dest_rules.join(format!("dot-agents--{key}"));
        link_or_copy_for_dot_agent_file(p, &dest)?;
    }
    Ok(())
}

fn merge_dot_agents_subdir_into_dest_roots(
    dot_agents: &Path,
    rel: &str,
    dest_roots: &[&Path],
) -> Result<()> {
    let src = dot_agents.join(rel);
    if !src.is_dir() {
        return Ok(());
    }
    for &root in dest_roots {
        let dst = root.join(rel);
        fs::create_dir_all(&dst).map_err(|e| AgentpackError::io(&dst, e))?;
        copy_merge_tree(&src, &dst)?;
    }
    Ok(())
}

/// Merge **`./.agents/`** into staged harness trees that do **not** natively read the directory.
///
/// **Cursor** and **OpenCode** natively discover `.agents/` from the workspace, so they are excluded.
/// Only **Claude bundle** and **Codex home** receive the overlay.
///
/// Supported layout: optional **`claude/`**, **`codex/`** subtrees (merged into that harness stage root);
/// shared **`rules/**/*.mdc`**, **`skills/`**, **`agents/`**, **`commands/`**; top-level **`AGENTS.md`** (Codex),
/// **`CLAUDE.md`** (Claude bundle). **`mcp.json`** is handled centrally by [`super::mcp::stage_merged_mcp`].
///
/// Set **`AGENTPACK_DOT_AGENTS=0`** to skip.
pub(crate) fn stage_dot_agents_overlay(project_root: &Path) -> Result<()> {
    let dot_agents = project_dot_agents_dir(project_root);
    if !dot_agents.is_dir() {
        return Ok(());
    }

    let bundle = staging_plugins_dir(project_root)?.join(STAGED_AGENTPACK_BUNDLE_NAME);
    let codex = staging_codex_home_dir(project_root)?;

    // Cursor and OpenCode natively read `.agents/` from the workspace, so only
    // Claude and Codex need the overlay merged into their staging trees.
    for (sub, dest) in [("claude", bundle.as_path()), ("codex", codex.as_path())] {
        let src = dot_agents.join(sub);
        if src.is_dir() {
            copy_merge_tree(&src, dest)?;
        }
    }

    let rules = dot_agents.join("rules");
    if rules.is_dir() {
        let dest_rules = bundle.join("rules");
        fs::create_dir_all(&dest_rules).map_err(|e| AgentpackError::io(&dest_rules, e))?;
        merge_dot_agents_rules_mdc(&rules, &dest_rules)?;
    }

    merge_dot_agents_subdir_into_dest_roots(
        &dot_agents,
        "skills",
        &[bundle.as_path(), codex.as_path()],
    )?;
    merge_dot_agents_subdir_into_dest_roots(&dot_agents, "agents", &[bundle.as_path()])?;
    merge_dot_agents_subdir_into_dest_roots(&dot_agents, "commands", &[bundle.as_path()])?;

    let agents_md = dot_agents.join("AGENTS.md");
    if agents_md.is_file() {
        copy_merge_tree(&agents_md, &codex.join("AGENTS.md"))?;
    }

    let claude_md = dot_agents.join("CLAUDE.md");
    if claude_md.is_file() {
        copy_merge_tree(&claude_md, &bundle.join("CLAUDE.md"))?;
    }

    // `.agents/mcp.json` is collected centrally by `staging::mcp::stage_merged_mcp`.

    Ok(())
}
