use std::env;
use std::fs;
use std::path::Path;

use walkdir::WalkDir;

use crate::error::{AgentpackError, Result};
use crate::fs_util::remove_path_any;
use crate::paths::{
    project_dot_agents_dir, staging_codex_home_dir, staging_cursor_pack_plugin_dir,
    staging_opencode_dir, staging_plugins_dir, STAGED_AGENTPACK_BUNDLE_NAME,
};

use super::tree::copy_merge_tree;

fn dot_agents_enabled() -> bool {
    !matches!(env::var("AGENTPACK_DOT_AGENTS"), Ok(v) if v == "0")
}

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

/// Merge **`./.agents/`** into staged harness trees after pack content. Project files overwrite pack
/// on the same relative path inside each destination directory.
///
/// Supported layout: optional **`claude/`**, **`opencode/`**, **`codex/`**, **`cursor/`** subtrees (each merged into that
/// harness stage root); shared **`rules/**/*.mdc`**, **`skills/`**, **`agents/`**, **`commands/`**, **`hooks/`**; Cursor-only
/// **`assets/`**, **`scripts/`**; top-level **`AGENTS.md`** (Codex), **`CLAUDE.md`** (Claude bundle), **`mcp.json`**.
///
/// Set **`AGENTPACK_DOT_AGENTS=0`** to skip.
pub(crate) fn stage_dot_agents_overlay(project_root: &Path) -> Result<()> {
    if !dot_agents_enabled() {
        return Ok(());
    }
    let dot_agents = project_dot_agents_dir(project_root);
    if !dot_agents.is_dir() {
        return Ok(());
    }

    let bundle = staging_plugins_dir(project_root)?.join(STAGED_AGENTPACK_BUNDLE_NAME);
    let opencode = staging_opencode_dir(project_root)?;
    let codex = staging_codex_home_dir(project_root)?;
    let cursor_pack = staging_cursor_pack_plugin_dir(project_root)?;

    for (sub, dest) in [
        ("claude", bundle.as_path()),
        ("opencode", opencode.as_path()),
        ("codex", codex.as_path()),
        ("cursor", cursor_pack.as_path()),
    ] {
        let src = dot_agents.join(sub);
        if src.is_dir() {
            copy_merge_tree(&src, dest)?;
        }
    }

    let rules = dot_agents.join("rules");
    if rules.is_dir() {
        for dest_rules in [
            bundle.join("rules"),
            opencode.join("rules"),
            cursor_pack.join("rules"),
        ] {
            fs::create_dir_all(&dest_rules).map_err(|e| AgentpackError::io(&dest_rules, e))?;
            merge_dot_agents_rules_mdc(&rules, &dest_rules)?;
        }
    }

    // Subdir routing: each tuple is (subdir, indices into all_roots).
    let all_roots: [&Path; 4] = [
        bundle.as_path(),
        opencode.as_path(),
        codex.as_path(),
        cursor_pack.as_path(),
    ];
    const SUBDIR_ROUTES: &[(&str, &[usize])] = &[
        ("skills", &[0, 1, 2, 3]),
        ("agents", &[0, 1, 3]),
        ("commands", &[0, 1, 3]),
        ("hooks", &[0, 3]),
        ("assets", &[3]),
        ("scripts", &[3]),
    ];
    for &(sub, indices) in SUBDIR_ROUTES {
        let dests: Vec<&Path> = indices.iter().map(|&i| all_roots[i]).collect();
        merge_dot_agents_subdir_into_dest_roots(&dot_agents, sub, &dests)?;
    }

    let agents_md = dot_agents.join("AGENTS.md");
    if agents_md.is_file() {
        copy_merge_tree(&agents_md, &codex.join("AGENTS.md"))?;
    }

    let claude_md = dot_agents.join("CLAUDE.md");
    if claude_md.is_file() {
        copy_merge_tree(&claude_md, &bundle.join("CLAUDE.md"))?;
    }

    let mcp = dot_agents.join("mcp.json");
    if mcp.is_file() {
        copy_merge_tree(&mcp, &bundle.join("mcp.json"))?;
        copy_merge_tree(&mcp, &cursor_pack.join("mcp.json"))?;
    }

    Ok(())
}
