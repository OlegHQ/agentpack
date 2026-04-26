//! Claude fake config root. `CLAUDE_CONFIG_DIR` is read **asymmetrically** by Claude Code (verified
//! by reading the v2.1.x bundle):
//!
//! - `settings.json` lookup uses `CLAUDE_CONFIG_DIR` *as the directory itself* — file path is
//!   `<CLAUDE_CONFIG_DIR>/settings.json`. When the env var is unset it defaults to `~/.claude/`.
//! - `.claude.json` lookup uses `CLAUDE_CONFIG_DIR` *as the parent* — file path is
//!   `<CLAUDE_CONFIG_DIR>/.claude.json`. When the env var is unset it defaults to `~`.
//!
//! So with `CLAUDE_CONFIG_DIR=$STAGING/claude-home`, Claude reads:
//! - `$STAGING/claude-home/settings.json`
//! - `$STAGING/claude-home/.claude.json`
//!
//! Both must exist, and any user `~/.claude/<entry>` (auth, projects, hooks, etc.) needs to be
//! reachable as `$STAGING/claude-home/<entry>` because that path replaces `~/.claude` for user
//! settings reads.
//!
//! Layout:
//! - `claude-home/settings.json` — real file merged from `~/.claude/settings.json` with attribution
//!   forced off. Other user keys (e.g. `skipDangerousModePermissionPrompt`) are preserved so the
//!   staged session keeps the user's behavior.
//! - `claude-home/.claude.json` — symlink to `~/.claude.json` (per-project state).
//! - `claude-home/<entry>` — symlink to `~/.claude/<entry>` for every other entry (auth,
//!   `projects/`, `commands/`, `agents/`, `skills/`, `hooks/`, etc.).

use std::fs;
use std::path::Path;

use crate::error::{AgentpackError, Result};
use crate::fs_util::{read_json_value_opt, remove_path_any, write_json_value};
use crate::paths::staging_claude_config_dir_for_mode;

use super::cursor::symlink_or_copy_into_fake_home;

/// Entry name we never symlink because we override it with a real file.
const SETTINGS_FILE: &str = "settings.json";

pub(super) fn materialize_claude_config_dir(project_root: &Path, mode_name: &str) -> Result<()> {
    let staged = staging_claude_config_dir_for_mode(project_root, mode_name)?;
    if staged.exists() {
        fs::remove_dir_all(&staged).map_err(|e| AgentpackError::io(&staged, e))?;
    }
    fs::create_dir_all(&staged).map_err(|e| AgentpackError::io(&staged, e))?;

    let Some(real_home) = dirs::home_dir() else {
        // No real `$HOME` to mirror (eg. some CI runners). Write a settings stub anyway.
        write_settings_with_forced_attribution(&staged, None)?;
        return Ok(());
    };
    let real_dot_claude = real_home.join(".claude");

    if real_dot_claude.is_dir() {
        for entry in
            fs::read_dir(&real_dot_claude).map_err(|e| AgentpackError::io(&real_dot_claude, e))?
        {
            let entry = entry.map_err(|e| AgentpackError::io(&real_dot_claude, e))?;
            let name = entry.file_name();
            if name == SETTINGS_FILE {
                continue;
            }
            let src = entry.path();
            let dst = staged.join(&name);
            let as_dir = entry
                .file_type()
                .map_err(|e| AgentpackError::io(&src, e))?
                .is_dir();
            symlink_or_copy_into_fake_home(&src, &dst, as_dir)?;
        }
    }

    let user_settings = real_dot_claude.join(SETTINGS_FILE);
    let user_settings = if user_settings.is_file() {
        Some(user_settings.as_path())
    } else {
        None
    };
    write_settings_with_forced_attribution(&staged, user_settings)?;

    let real_app = real_home.join(".claude.json");
    if real_app.is_file() {
        let dst = staged.join(".claude.json");
        symlink_or_copy_into_fake_home(&real_app, &dst, false)?;
    }

    Ok(())
}

fn write_settings_with_forced_attribution(
    staged: &Path,
    user_settings: Option<&Path>,
) -> Result<()> {
    use serde_json::{json, Value};

    let mut value = match user_settings {
        Some(p) => read_json_value_opt(p)?.unwrap_or_else(|| json!({})),
        None => json!({}),
    };
    if !value.is_object() {
        value = json!({});
    }

    if !keep_attribution() {
        let obj = value.as_object_mut().expect("ensured object above");
        obj.insert("includeCoAuthoredBy".into(), Value::Bool(false));
        let attribution = obj
            .entry("attribution".to_string())
            .or_insert_with(|| json!({}));
        if !attribution.is_object() {
            *attribution = json!({});
        }
        let attr_obj = attribution.as_object_mut().expect("ensured object above");
        attr_obj.insert("commit".into(), Value::String(String::new()));
        attr_obj.insert("pr".into(), Value::String(String::new()));
    }

    let dest = staged.join(SETTINGS_FILE);
    remove_path_any(&dest)?;
    write_json_value(&dest, &value)?;
    tracing::debug!(path = %dest.display(), "materialized claude settings.json with attribution off");
    Ok(())
}

fn keep_attribution() -> bool {
    matches!(
        std::env::var("AGENTPACK_KEEP_ATTRIBUTION").ok().as_deref(),
        Some("1") | Some("true") | Some("yes")
    )
}

#[cfg(test)]
mod tests {
    use super::*;
    use serde_json::json;

    #[test]
    fn settings_forced_off_with_no_user_file() {
        let dir = tempfile::tempdir().unwrap();
        let staged = dir.path().join("claude-home");
        std::fs::create_dir_all(&staged).unwrap();
        write_settings_with_forced_attribution(&staged, None).unwrap();
        let v = read_json_value_opt(&staged.join(SETTINGS_FILE))
            .unwrap()
            .unwrap();
        assert_eq!(v["includeCoAuthoredBy"], false);
        assert_eq!(v["attribution"]["commit"], "");
        assert_eq!(v["attribution"]["pr"], "");
    }

    #[test]
    fn settings_preserves_user_fields_and_overrides_attribution() {
        let dir = tempfile::tempdir().unwrap();
        let staged = dir.path().join("claude-home");
        std::fs::create_dir_all(&staged).unwrap();
        let user = dir.path().join("user-settings.json");
        std::fs::write(
            &user,
            r#"{"theme":"dark","skipDangerousModePermissionPrompt":true,"attribution":{"commit":"keep me"}}"#,
        )
        .unwrap();
        write_settings_with_forced_attribution(&staged, Some(&user)).unwrap();
        let v = read_json_value_opt(&staged.join(SETTINGS_FILE))
            .unwrap()
            .unwrap();
        assert_eq!(v["theme"], "dark");
        assert_eq!(v["skipDangerousModePermissionPrompt"], true);
        assert_eq!(v["attribution"]["commit"], "");
        assert_eq!(v["attribution"]["pr"], "");
        assert_eq!(v["includeCoAuthoredBy"], false);
        let raw = std::fs::read_to_string(&user).unwrap();
        assert!(raw.contains("\"commit\":\"keep me\""));
    }

    #[test]
    fn keep_env_skips_force() {
        let dir = tempfile::tempdir().unwrap();
        let staged = dir.path().join("claude-home");
        std::fs::create_dir_all(&staged).unwrap();
        let user = dir.path().join("user.json");
        std::fs::write(&user, json!({"theme":"dark"}).to_string()).unwrap();
        std::env::set_var("AGENTPACK_KEEP_ATTRIBUTION", "1");
        let res = write_settings_with_forced_attribution(&staged, Some(&user));
        std::env::remove_var("AGENTPACK_KEEP_ATTRIBUTION");
        res.unwrap();
        let v = read_json_value_opt(&staged.join(SETTINGS_FILE))
            .unwrap()
            .unwrap();
        assert_eq!(v["theme"], "dark");
        assert!(v.get("attribution").is_none());
        assert!(v.get("includeCoAuthoredBy").is_none());
    }
}
