use std::fs;
use std::path::Path;

use crate::error::{AgentpackError, Result};
use crate::fs_util::{remove_path_any, strip_under_root, walk_dir, WalkDirOpts};
use crate::mode::filter::EffectiveMode;
use crate::paths::{
    project_dot_agents_dir, staging_codex_home_dir_for_mode, staging_plugins_dir_for_mode,
    STAGED_AGENTPACK_BUNDLE_NAME,
};

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
fn merge_dot_agents_rules_mdc(
    rules_root: &Path,
    dest_rules: &Path,
    mode: &EffectiveMode,
) -> Result<()> {
    if !rules_root.is_dir() {
        return Ok(());
    }
    for entry in walk_dir(rules_root, WalkDirOpts::files()) {
        let entry = entry?;
        let p = entry.path();
        if !p.extension().is_some_and(|x| x.eq_ignore_ascii_case("mdc")) {
            continue;
        }
        let rel = p.strip_prefix(rules_root).map_err(|_| {
            AgentpackError::Staging(format!("rule path outside rules root ({})", p.display()))
        })?;
        if !mode.allows_dot_agents_path(&Path::new("rules").join(rel))? {
            continue;
        }
        let key = rel
            .to_string_lossy()
            .replace(std::path::MAIN_SEPARATOR, "--");
        let dest = dest_rules.join(format!("dot-agents--{key}"));
        link_or_copy_for_dot_agent_file(p, &dest)?;
    }
    Ok(())
}

fn copy_dot_agents_tree(
    dot_agents_root: &Path,
    source_rel: &str,
    dest_root: &Path,
    strip_source_prefix: bool,
    mode: &EffectiveMode,
) -> Result<()> {
    let src = dot_agents_root.join(source_rel);
    if !src.is_dir() {
        return Ok(());
    }
    for entry in walk_dir(&src, WalkDirOpts::files()) {
        let entry = entry?;
        let path = entry.path();
        let rel = strip_under_root(path, &src)?;
        if rel.as_os_str().is_empty() {
            continue;
        }
        let selector_rel = Path::new(source_rel).join(rel);
        if !mode.allows_dot_agents_path(&selector_rel)? {
            continue;
        }

        let dest_rel = if strip_source_prefix {
            rel
        } else {
            selector_rel.as_path()
        };
        let dest = dest_root.join(dest_rel);
        if entry.file_type().is_dir() {
            fs::create_dir_all(&dest).map_err(|error| AgentpackError::io(&dest, error))?;
            continue;
        }
        if entry.file_type().is_file() {
            link_or_copy_for_dot_agent_file(path, &dest)?;
        }
    }
    Ok(())
}

fn copy_dot_agents_file(
    dot_agents_root: &Path,
    source_rel: &str,
    dest: &Path,
    mode: &EffectiveMode,
) -> Result<()> {
    let src = dot_agents_root.join(source_rel);
    if !src.is_file() || !mode.allows_dot_agents_path(Path::new(source_rel))? {
        return Ok(());
    }
    link_or_copy_for_dot_agent_file(&src, dest)
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
pub(crate) fn stage_dot_agents_overlay(
    project_root: &Path,
    mode_name: &str,
    mode: &EffectiveMode,
) -> Result<()> {
    let dot_agents = project_dot_agents_dir(project_root);
    if !dot_agents.is_dir() {
        return Ok(());
    }

    let bundle =
        staging_plugins_dir_for_mode(project_root, mode_name)?.join(STAGED_AGENTPACK_BUNDLE_NAME);
    let codex = staging_codex_home_dir_for_mode(project_root, mode_name)?;

    // Cursor and OpenCode natively read `.agents/` from the workspace, so only
    // Claude and Codex need the overlay merged into their staging trees.
    for (sub, dest) in [("claude", bundle.as_path()), ("codex", codex.as_path())] {
        copy_dot_agents_tree(&dot_agents, sub, dest, true, mode)?;
    }

    let rules = dot_agents.join("rules");
    if rules.is_dir() {
        let dest_rules = bundle.join("rules");
        fs::create_dir_all(&dest_rules).map_err(|e| AgentpackError::io(&dest_rules, e))?;
        merge_dot_agents_rules_mdc(&rules, &dest_rules, mode)?;
    }

    copy_dot_agents_tree(&dot_agents, "skills", &bundle, false, mode)?;
    copy_dot_agents_tree(&dot_agents, "skills", &codex, false, mode)?;
    copy_dot_agents_tree(&dot_agents, "agents", &bundle, false, mode)?;
    copy_dot_agents_tree(&dot_agents, "commands", &bundle, false, mode)?;

    copy_dot_agents_file(&dot_agents, "AGENTS.md", &codex.join("AGENTS.md"), mode)?;
    copy_dot_agents_file(&dot_agents, "CLAUDE.md", &bundle.join("CLAUDE.md"), mode)?;

    // `.agents/mcp.json` is collected centrally by `staging::mcp::stage_merged_mcp`.

    Ok(())
}
