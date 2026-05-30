//! Force-disable AI attribution (Co-Authored-By trailers, "Generated with X" footers, etc.) in
//! every staged harness. Settings are written into the staged config files so they only affect
//! sessions launched through agentpack — the user's real `~/.claude`, `~/.codex`, `~/.cursor`,
//! `~/.grok`, and `~/.config/opencode` are never modified.
//!
//! Claude is handled by `claude_home.rs`, which writes a stable
//! `$AGENTPACK_HOME/claude-settings.json` overlay loaded via `claude --settings <path>` (the
//! launcher passes the flag). We deliberately do **not** redirect `CLAUDE_CONFIG_DIR` because
//! Claude Code namespaces credential storage by `sha256(CLAUDE_CONFIG_DIR)`. The helpers below
//! cover Codex, Cursor, OpenCode, Grok, and Antigravity.
//!
//! Per-harness keys (last verified 2026-04 against vendor docs):
//!
//! | Harness  | File                              | Keys / values                                           |
//! | -------- | --------------------------------- | ------------------------------------------------------- |
//! | Codex    | `config.toml` (top-level)         | `commit_attribution = ""`                               |
//! | Cursor   | `.cursor/cli-config.json`         | `attribution.attributeCommitsToAgent = false`,          |
//! |          |                                   | `attribution.attributePRsToAgent = false`               |
//! | OpenCode | `opencode.json` + instruction file| no first-class setting; injected via `instructions[]`   |
//! | Grok     | `AGENTS.md`                       | no confirmed first-class setting; prompt guidance only  |
//! | Agy      | `rules/agentpack-no-attribution.md`| no confirmed first-class setting; prompt guidance only |
//!
//! Set `AGENTPACK_KEEP_ATTRIBUTION=1` to opt out and preserve the user's existing values.
//!
//! Source documents:
//!  - <https://developers.openai.com/codex/config-reference>
//!  - <https://cursor.com/docs/cli/reference/configuration>
//!  - sst/opencode#919, sst/opencode#1135 (no setting; OpenCode reads `instructions[]` files).

use std::path::Path;

use serde_json::{json, Value};

use crate::error::Result;
use crate::fs_util::{read_json_value_opt, remove_path_any, write_json_value};

const KEEP_ENV: &str = "AGENTPACK_KEEP_ATTRIBUTION";
const AGY_ATTRIBUTION_RULE_FILE: &str = "agentpack-no-attribution.md";
/// Prose attribution-off guidance shared by the prompt-level harnesses (OpenCode / Grok / Agy)
/// that have no first-class attribution setting.
pub(crate) const NO_ATTRIBUTION_BODY: &str = "# Attribution policy

Do not add any AI-attribution lines to git commits, pull requests, or other artifacts you author.
Specifically, do not include:

- `Co-Authored-By: <model> <noreply@...>` trailers.
- `Generated with [agent name]` footers, banners, or similar credit lines.
- Tool/agent name signatures in commit messages or PR descriptions.

Write commit messages and PR descriptions as if a human author wrote them.
";

/// Single source of truth for the `AGENTPACK_KEEP_ATTRIBUTION` opt-out, shared across every staged
/// harness and the Claude overlay.
pub(crate) fn keep_attribution() -> bool {
    matches!(
        std::env::var(KEEP_ENV).ok().as_deref(),
        Some("1") | Some("true") | Some("yes")
    )
}

/// Patch a Cursor `cli-config.json` value: force `attribution.attributeCommitsToAgent` and
/// `attribution.attributePRsToAgent` to `false`. Returns the modified JSON.
fn patch_cursor_cli_config(mut value: Value) -> Value {
    if !value.is_object() {
        value = json!({});
    }
    let obj = value.as_object_mut().expect("ensured object above");
    let attribution = obj
        .entry("attribution".to_string())
        .or_insert_with(|| json!({}));
    if !attribution.is_object() {
        *attribution = json!({});
    }
    let attr_obj = attribution.as_object_mut().expect("ensured object above");
    attr_obj.insert("attributeCommitsToAgent".into(), Value::Bool(false));
    attr_obj.insert("attributePRsToAgent".into(), Value::Bool(false));
    value
}

/// Force-disable Cursor attribution in `<root>/cli-config.json`. Reads the existing file (if
/// present) so user fields like `editor`, `permissions`, `mcpServers` survive.
pub(crate) fn force_cursor_attribution_off(root: &Path) -> Result<()> {
    if keep_attribution() {
        return Ok(());
    }
    let path = root.join("cli-config.json");
    let value = read_json_value_opt(&path)?.unwrap_or_else(|| json!({}));
    let patched = patch_cursor_cli_config(value);
    write_json_value(&path, &patched)?;
    tracing::debug!(path = %path.display(), "forced Cursor attribution off");
    Ok(())
}

/// Materialize a non-symlink Cursor `cli-config.json` inside the fake-home so writes from agentpack
/// don't bleed back into the user's real `~/.cursor/cli-config.json`. Reads the user's file first
/// when present, then forces attribution off.
pub(super) fn force_cursor_fake_home_attribution_off(
    fake_cursor: &Path,
    real_cursor_cli_config: Option<&Path>,
) -> Result<()> {
    if keep_attribution() {
        return Ok(());
    }
    let dest = fake_cursor.join("cli-config.json");
    remove_path_any(&dest)?;
    let base = match real_cursor_cli_config {
        Some(p) => read_json_value_opt(p)?.unwrap_or_else(|| json!({})),
        None => json!({}),
    };
    let patched = patch_cursor_cli_config(base);
    write_json_value(&dest, &patched)?;
    tracing::debug!(path = %dest.display(), "forced Cursor fake-home attribution off");
    Ok(())
}

/// Antigravity has no confirmed first-class attribution setting. Stage a plugin-local rule as
/// prompt-level guidance only.
pub(crate) fn force_agy_attribution_off(bundle: &Path) -> Result<()> {
    if keep_attribution() {
        return Ok(());
    }
    let path = bundle.join("rules").join(AGY_ATTRIBUTION_RULE_FILE);
    let body = format!(
        "---\ndescription: Disable AI attribution footers\nalwaysApply: true\n---\n\n{}\n",
        NO_ATTRIBUTION_BODY.trim()
    );
    crate::fs_util::write_text_file(&path, &body)?;
    tracing::debug!(path = %path.display(), "staged Antigravity attribution-off rule");
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;

    fn with_keep_unset<F: FnOnce()>(f: F) {
        let prev = std::env::var_os(KEEP_ENV);
        std::env::remove_var(KEEP_ENV);
        f();
        if let Some(v) = prev {
            std::env::set_var(KEEP_ENV, v);
        }
    }

    #[test]
    fn cursor_attribution_writes_both_flags() {
        with_keep_unset(|| {
            let dir = tempfile::tempdir().unwrap();
            force_cursor_attribution_off(dir.path()).unwrap();
            let v = read_json_value_opt(&dir.path().join("cli-config.json"))
                .unwrap()
                .unwrap();
            assert_eq!(v["attribution"]["attributeCommitsToAgent"], false);
            assert_eq!(v["attribution"]["attributePRsToAgent"], false);
        });
    }

    #[test]
    fn cursor_fake_home_breaks_symlink_via_real_copy() {
        with_keep_unset(|| {
            let dir = tempfile::tempdir().unwrap();
            let real = dir.path().join("real-cli-config.json");
            std::fs::write(
                &real,
                r#"{"editor":{"vimMode":true},"attribution":{"attributeCommitsToAgent":true}}"#,
            )
            .unwrap();
            let fake = dir.path().join("fake/.cursor");
            std::fs::create_dir_all(&fake).unwrap();
            force_cursor_fake_home_attribution_off(&fake, Some(&real)).unwrap();
            let v = read_json_value_opt(&fake.join("cli-config.json"))
                .unwrap()
                .unwrap();
            assert_eq!(v["editor"]["vimMode"], true);
            assert_eq!(v["attribution"]["attributeCommitsToAgent"], false);
            assert_eq!(v["attribution"]["attributePRsToAgent"], false);
            // Source untouched.
            let src = std::fs::read_to_string(&real).unwrap();
            assert!(src.contains("\"attributeCommitsToAgent\":true"));
        });
    }
}
