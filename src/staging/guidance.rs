//! Always-on guidance — the **agnostic** half.
//!
//! Plugins signal "this text should always be in the model's context" by shipping a Cursor-style
//! rule (`rules/*.mdc` with `alwaysApply: true`). [`collect_guidance_blob`] concatenates the
//! matching rules (from pinned plugins + `.agents/rules`) into a single blob, and [`write_agents_md`]
//! is the shared idempotent `AGENTS.md` writer. *Where* the blob goes is per-harness: each harness
//! implements `Harness::inject_guidance` (Codex/OpenCode/Grok → `AGENTS.md`; Claude → a synthesized
//! `SessionStart` hook; Cursor/Agy → nothing, they read native `rules/*.mdc`). The per-harness
//! runtime output shape lives in `Harness::guidance_injection_json`.
//!
//! Source of truth: plugin `rules/*.{md,mdc}` + `.agents/rules/**` with `alwaysApply: true` in
//! frontmatter. Rules without `alwaysApply` are routed via the existing skill-fallback pipeline
//! and invoked by description match, not injected here.

use std::collections::BTreeSet;
use std::fs;
use std::path::Path;

use walkdir::WalkDir;

use crate::artifacts::{parse_markdown_artifact, ArtifactKind, MarkdownArtifact};
use crate::cache::cache_entry_dir;
use crate::error::{AgentpackError, Result};
use crate::lockfile::PackLock;
use crate::mode::filter::EffectiveMode;
use crate::paths::project_dot_agents_dir;

use super::pack_overlay::disabled_in_config;

/// Markers delimiting agentpack-owned injected content inside `AGENTS.md`. Makes re-staging
/// idempotent even when a user-seeded file is present in the staging root.
const AGENTS_MD_BEGIN: &str = "<!-- agentpack:guidance:begin -->";
const AGENTS_MD_END: &str = "<!-- agentpack:guidance:end -->";

fn walk_rules(
    root: &Path,
    origin: &str,
    mut is_enabled: impl FnMut(&Path) -> Result<bool>,
    into: &mut Vec<MarkdownArtifact>,
) -> Result<()> {
    if !root.is_dir() {
        return Ok(());
    }
    for entry in WalkDir::new(root).follow_links(false) {
        let entry = entry.map_err(|e| AgentpackError::Staging(e.to_string()))?;
        if !entry.file_type().is_file() {
            continue;
        }
        let path = entry.path();
        let rel = match path.strip_prefix(root) {
            Ok(r) => r,
            Err(_) => continue,
        };
        if !is_enabled(rel)? {
            continue;
        }
        let ext = path.extension().and_then(|e| e.to_str()).unwrap_or("");
        if !ext.eq_ignore_ascii_case("md") && !ext.eq_ignore_ascii_case("mdc") {
            continue;
        }
        let contents = match fs::read_to_string(path) {
            Ok(c) => c,
            Err(e) => {
                tracing::debug!(path = %path.display(), error = %e, origin = %origin, "skip unreadable rule");
                continue;
            }
        };
        match parse_markdown_artifact(rel, &contents, None) {
            Ok(Some(a)) if a.kind == ArtifactKind::Rule && a.always_apply => into.push(a),
            _ => {}
        }
    }
    Ok(())
}

/// Concatenate all always-apply rules into a single Markdown blob. Returns `None` if nothing
/// qualifies. Deduplicates by rule name to avoid piling up the same rule when it lives in both
/// a plugin and `.agents/rules`.
pub(crate) fn collect_guidance_blob(
    project_root: &Path,
    lock: &PackLock,
    mode: &EffectiveMode,
) -> Result<Option<String>> {
    let mut rules: Vec<MarkdownArtifact> = Vec::new();

    let mut plug_list = lock.plugins().collect::<Vec<_>>();
    plug_list.sort_by(|a, b| a.cache_key.cmp(&b.cache_key));
    for plugin in plug_list {
        if plugin.cache_key.is_empty() || disabled_in_config(lock, plugin) {
            continue;
        }
        let cache_root = match cache_entry_dir(&plugin.cache_key) {
            Ok(p) => p,
            Err(_) => continue,
        };
        walk_rules(
            &cache_root,
            &plugin.module,
            |rel| mode.allows_package_path(&plugin.module, rel),
            &mut rules,
        )?;
    }

    walk_rules(
        &project_dot_agents_dir(project_root),
        ".agents",
        |rel| mode.allows_dot_agents_path(rel),
        &mut rules,
    )?;

    if rules.is_empty() {
        return Ok(None);
    }

    let mut seen = BTreeSet::new();
    let mut out = String::new();
    out.push_str("# Agentpack-injected guidance\n\n");
    out.push_str("_The following rules were declared with `alwaysApply: true` in one or more pinned plugins. They are injected into every supported harness for consistency._\n\n");
    for r in rules {
        if !seen.insert(r.name.clone()) {
            continue;
        }
        out.push_str("---\n\n");
        out.push_str(&format!("## {}\n\n", r.name));
        if !r.description.is_empty() {
            out.push_str(&format!("_{}_\n\n", r.description.trim()));
        }
        out.push_str(r.body.trim());
        out.push_str("\n\n");
    }
    Ok(Some(out))
}

/// Wrap `blob` between the agentpack markers so re-staging replaces rather than piles up.
fn agentpack_fenced(blob: &str) -> String {
    format!("\n\n{AGENTS_MD_BEGIN}\n{}\n{AGENTS_MD_END}\n", blob.trim())
}

/// Strip any previously-injected agentpack block from `text`. Leaves non-agentpack content
/// (user-seeded AGENTS.md) intact.
fn strip_prior_block(text: &str) -> String {
    let Some(begin) = text.find(AGENTS_MD_BEGIN) else {
        return text.to_string();
    };
    let Some(end_rel) = text[begin..].find(AGENTS_MD_END) else {
        return text.to_string();
    };
    let end = begin + end_rel + AGENTS_MD_END.len();
    let mut out = String::with_capacity(text.len());
    out.push_str(text[..begin].trim_end());
    out.push_str(text[end..].trim_start_matches('\n'));
    out
}

/// Idempotently write `blob` into a marker-fenced block inside an `AGENTS.md`, preserving any
/// user-seeded content. Agnostic — each harness picks the destination path in its `inject_guidance`.
pub(crate) fn write_agents_md(dest: &Path, blob: &str) -> Result<()> {
    let existing = fs::read_to_string(dest).unwrap_or_default();
    let base = strip_prior_block(&existing);
    let mut out = base.trim_end().to_string();
    if out.is_empty() {
        out.push_str("# AGENTS.md\n");
    }
    out.push_str(&agentpack_fenced(blob));
    if let Some(parent) = dest.parent() {
        fs::create_dir_all(parent).map_err(|e| AgentpackError::io(parent, e))?;
    }
    fs::write(dest, out).map_err(|e| AgentpackError::io(dest, e))
}

#[cfg(test)]
mod tests {
    use super::*;
    use tempfile::tempdir;

    #[test]
    fn strip_prior_block_removes_fenced_region() {
        let text = format!("user text\n\n{AGENTS_MD_BEGIN}\ninjected\n{AGENTS_MD_END}\n");
        let stripped = strip_prior_block(&text);
        assert!(stripped.contains("user text"));
        assert!(!stripped.contains("injected"));
        assert!(!stripped.contains("agentpack:guidance"));
    }

    #[test]
    fn strip_prior_block_noop_when_no_markers() {
        let text = "user text only";
        assert_eq!(strip_prior_block(text), text);
    }

    #[test]
    fn write_agents_md_appends_fenced_block_to_seeded_content() {
        let dir = tempdir().unwrap();
        let dest = dir.path().join("AGENTS.md");
        fs::write(&dest, "# User\n\nPre-existing guidance.\n").unwrap();
        write_agents_md(&dest, "injected body").unwrap();
        let got = fs::read_to_string(&dest).unwrap();
        assert!(got.starts_with("# User"));
        assert!(got.contains("Pre-existing guidance."));
        assert!(got.contains(AGENTS_MD_BEGIN));
        assert!(got.contains("injected body"));
        assert!(got.contains(AGENTS_MD_END));
    }

    #[test]
    fn write_agents_md_is_idempotent_on_resync() {
        let dir = tempdir().unwrap();
        let dest = dir.path().join("AGENTS.md");
        write_agents_md(&dest, "v1").unwrap();
        write_agents_md(&dest, "v2").unwrap();
        let got = fs::read_to_string(&dest).unwrap();
        assert!(got.contains("v2"));
        assert!(!got.contains("v1"));
        assert_eq!(got.matches(AGENTS_MD_BEGIN).count(), 1);
    }
}
