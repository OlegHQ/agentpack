//! Always-on guidance injection.
//!
//! Plugins signal "this text should always be in the model's context" by shipping a Cursor-style
//! rule (`rules/*.mdc` with `alwaysApply: true`). Cursor reads those natively from the staged
//! pack. The other three harnesses have no plugin-level always-on channel, so we concatenate
//! the matching rules into a single blob per project and inject them:
//!
//! * **Codex** — append to `$STAGING/codex-home/AGENTS.md`. Codex auto-loads it as user guidance.
//! * **OpenCode** — append to `$STAGING/opencode/AGENTS.md`. OpenCode auto-loads from its config dir.
//! * **Claude** — synthesize a `SessionStart` hook entry in the bundle's `hooks/hooks.json` that
//!   emits the blob as `hookSpecificOutput.additionalContext`. Runs once per session.
//! * **Cursor** — nothing extra. Native `rules/*.mdc` already does it.
//!
//! Source of truth: plugin `rules/*.{md,mdc}` + `.agents/rules/**` with `alwaysApply: true` in
//! frontmatter. Rules without `alwaysApply` are routed via the existing skill-fallback pipeline
//! and invoked by description match, not injected here.

use std::collections::BTreeSet;
use std::fs;
use std::path::{Path, PathBuf};

use walkdir::WalkDir;

use crate::artifacts::{parse_markdown_artifact, ArtifactKind, MarkdownArtifact};
use crate::cache::cache_entry_dir;
use crate::error::{AgentpackError, Result};
use crate::lockfile::PackLock;
use crate::mode::filter::EffectiveMode;
use crate::paths::project_dot_agents_dir;

use super::pack_overlay::{disabled_in_config, PackHarnessRoots};

/// File in the bundle holding the raw blob that the Claude SessionStart hook reads.
const BUNDLE_GUIDANCE_REL: &str = "_agentpack/guidance.md";

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

fn write_agents_md(dest: &Path, blob: &str) -> Result<()> {
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

/// Add a SessionStart hook into the bundle's `hooks/hooks.json` that emits the guidance blob as
/// `additionalContext`. Works whether or not the hooks pipeline already wrote the file.
fn add_claude_session_start_hook(bundle: &Path, guidance_file: &Path) -> Result<()> {
    use serde_json::{json, Map, Value};

    let hooks_path = bundle.join("hooks/hooks.json");
    let existing = fs::read_to_string(&hooks_path).unwrap_or_else(|_| "{\"hooks\":{}}".into());
    let mut root: Value = serde_json::from_str(&existing).unwrap_or_else(|_| json!({"hooks": {}}));
    let root_obj = root.as_object_mut().ok_or_else(|| {
        AgentpackError::Staging(format!("{}: not a JSON object", hooks_path.display()))
    })?;
    let hooks = root_obj
        .entry("hooks".to_string())
        .or_insert_with(|| Value::Object(Map::new()));
    let hooks_obj = hooks.as_object_mut().ok_or_else(|| {
        AgentpackError::Staging(format!(
            "{}: `hooks` not a JSON object",
            hooks_path.display()
        ))
    })?;

    let cmd = format!(
        "agentpack hook-exec inject-guidance --target claude --event SessionStart --file {}",
        shell_escape::escape(guidance_file.to_string_lossy())
    );
    let entry = json!({
        "hooks": [{"type": "command", "command": cmd}]
    });

    // Replace any prior agentpack-injected SessionStart entry (identified by the command prefix)
    // so re-staging doesn't stack duplicates.
    let prefix = "agentpack hook-exec inject-guidance";
    let arr = hooks_obj
        .entry("SessionStart".to_string())
        .or_insert_with(|| Value::Array(Vec::new()))
        .as_array_mut()
        .ok_or_else(|| {
            AgentpackError::Staging(format!(
                "{}: SessionStart not an array",
                hooks_path.display()
            ))
        })?;
    arr.retain(|e| {
        let grp = e.as_object();
        let has_ours = grp
            .and_then(|g| g.get("hooks"))
            .and_then(Value::as_array)
            .map(|inner| {
                inner.iter().any(|h| {
                    h.get("command")
                        .and_then(Value::as_str)
                        .is_some_and(|c| c.contains(prefix))
                })
            })
            .unwrap_or(false);
        !has_ours
    });
    arr.push(entry);

    if let Some(parent) = hooks_path.parent() {
        fs::create_dir_all(parent).map_err(|e| AgentpackError::io(parent, e))?;
    }
    let out = serde_json::to_string_pretty(&root)
        .map_err(|e| AgentpackError::Staging(format!("{}: {e}", hooks_path.display())))?;
    fs::write(&hooks_path, out).map_err(|e| AgentpackError::io(&hooks_path, e))
}

/// Public entry: collect and stage guidance into every harness that needs manual injection.
pub(super) fn stage_guidance_all_harnesses(
    project_root: &Path,
    lock: &PackLock,
    mode: &EffectiveMode,
    dests: &PackHarnessRoots<'_>,
) -> Result<()> {
    let Some(blob) = collect_guidance_blob(project_root, lock, mode)? else {
        return Ok(());
    };

    // Raw blob lives in the Claude bundle — the SessionStart hook reads it from there.
    let guidance_file: PathBuf = dests.claude_bundle.join(BUNDLE_GUIDANCE_REL);
    if let Some(parent) = guidance_file.parent() {
        fs::create_dir_all(parent).map_err(|e| AgentpackError::io(parent, e))?;
    }
    fs::write(&guidance_file, &blob).map_err(|e| AgentpackError::io(&guidance_file, e))?;

    write_agents_md(&dests.codex.join("AGENTS.md"), &blob)?;
    write_agents_md(&dests.opencode.join("AGENTS.md"), &blob)?;
    write_agents_md(&dests.grok_home.join("AGENTS.md"), &blob)?;
    add_claude_session_start_hook(dests.claude_bundle, &guidance_file)?;
    // Cursor: nothing — native `rules/*.mdc` with `alwaysApply: true` are already staged in the
    // pack bundle and symlinked into the Cursor fake HOME by the standard staging path.
    Ok(())
}

/// Read the guidance file and emit target-specific `additionalContext` JSON.
/// Used by the `agentpack hook-exec inject-guidance` subcommand.
pub fn emit_injection_json(
    file: &Path,
    event: &str,
    target: &str,
) -> anyhow::Result<serde_json::Value> {
    use serde_json::json;
    let body = fs::read_to_string(file)
        .map_err(|e| anyhow::anyhow!("read guidance file {}: {e}", file.display()))?;
    let value = match target {
        "claude" => json!({
            "hookSpecificOutput": {
                "hookEventName": event,
                "additionalContext": body,
            }
        }),
        "codex" => json!({
            "additionalContext": body,
            "continue": true,
        }),
        "cursor" => json!({
            "additional_context": body,
        }),
        "opencode" => json!({"additional_context": body}),
        "grok" => json!({
            "hookSpecificOutput": {
                "hookEventName": event,
                "additionalContext": body,
            }
        }),
        "agy" => json!({
            "additionalContext": body,
            "continue": true,
        }),
        _ => json!({"additional_context": body}),
    };
    Ok(value)
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

    #[test]
    fn add_claude_session_start_hook_adds_entry_when_file_absent() {
        let dir = tempdir().unwrap();
        let bundle = dir.path();
        let guidance = bundle.join(BUNDLE_GUIDANCE_REL);
        fs::create_dir_all(guidance.parent().unwrap()).unwrap();
        fs::write(&guidance, "hi").unwrap();
        add_claude_session_start_hook(bundle, &guidance).unwrap();
        let hooks_json = fs::read_to_string(bundle.join("hooks/hooks.json")).unwrap();
        let parsed: serde_json::Value = serde_json::from_str(&hooks_json).unwrap();
        let arr = parsed["hooks"]["SessionStart"].as_array().unwrap();
        assert_eq!(arr.len(), 1);
        let cmd = arr[0]["hooks"][0]["command"].as_str().unwrap();
        assert!(cmd.contains("inject-guidance"));
        assert!(cmd.contains("--target claude"));
    }

    #[test]
    fn add_claude_session_start_hook_replaces_prior_entry() {
        let dir = tempdir().unwrap();
        let bundle = dir.path();
        let guidance = bundle.join(BUNDLE_GUIDANCE_REL);
        fs::create_dir_all(guidance.parent().unwrap()).unwrap();
        fs::write(&guidance, "hi").unwrap();
        add_claude_session_start_hook(bundle, &guidance).unwrap();
        add_claude_session_start_hook(bundle, &guidance).unwrap();
        let hooks_json = fs::read_to_string(bundle.join("hooks/hooks.json")).unwrap();
        let parsed: serde_json::Value = serde_json::from_str(&hooks_json).unwrap();
        let arr = parsed["hooks"]["SessionStart"].as_array().unwrap();
        assert_eq!(arr.len(), 1);
    }

    #[test]
    fn add_claude_session_start_hook_preserves_other_events() {
        let dir = tempdir().unwrap();
        let bundle = dir.path();
        fs::create_dir_all(bundle.join("hooks")).unwrap();
        fs::write(
            bundle.join("hooks/hooks.json"),
            r#"{"hooks":{"PreToolUse":[{"matcher":"Read","hooks":[{"type":"command","command":"echo"}]}]}}"#,
        )
        .unwrap();
        let guidance = bundle.join(BUNDLE_GUIDANCE_REL);
        fs::create_dir_all(guidance.parent().unwrap()).unwrap();
        fs::write(&guidance, "hi").unwrap();
        add_claude_session_start_hook(bundle, &guidance).unwrap();
        let parsed: serde_json::Value =
            serde_json::from_str(&fs::read_to_string(bundle.join("hooks/hooks.json")).unwrap())
                .unwrap();
        assert!(!parsed["hooks"]["PreToolUse"].as_array().unwrap().is_empty());
        assert_eq!(parsed["hooks"]["SessionStart"].as_array().unwrap().len(), 1);
    }

    #[test]
    fn emit_injection_json_shapes_per_target() {
        let dir = tempdir().unwrap();
        let file = dir.path().join("g.md");
        fs::write(&file, "body text").unwrap();

        let claude = emit_injection_json(&file, "SessionStart", "claude").unwrap();
        assert_eq!(
            claude["hookSpecificOutput"]["additionalContext"],
            "body text"
        );
        assert_eq!(
            claude["hookSpecificOutput"]["hookEventName"],
            "SessionStart"
        );

        let codex = emit_injection_json(&file, "SessionStart", "codex").unwrap();
        assert_eq!(codex["additionalContext"], "body text");

        let cursor = emit_injection_json(&file, "sessionStart", "cursor").unwrap();
        assert_eq!(cursor["additional_context"], "body text");
    }
}
