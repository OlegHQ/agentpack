//! Claude attribution-overlay settings file.
//!
//! Why we do **not** redirect `CLAUDE_CONFIG_DIR`
//! ---------------------------------------------
//! Claude Code v2.1.x namespaces credential storage by `CLAUDE_CONFIG_DIR`:
//!
//! - macOS keychain service: `Claude Code-credentials-<sha256(CLAUDE_CONFIG_DIR)[:8]>`
//!   when the env var is set, plain `Claude Code-credentials` otherwise.
//! - File fallback: `$CLAUDE_CONFIG_DIR/.credentials.json` (or `~/.claude/.credentials.json`).
//!
//! agentpack used to point `CLAUDE_CONFIG_DIR` at a per-project, per-mode dir under `temp_dir()`.
//! That made the keychain service name (and the file-fallback path) change on every project
//! switch, mode switch, and macOS reboot — so Claude forgot the login every time.
//!
//! The fix is to **leave `CLAUDE_CONFIG_DIR` unset** and pass `claude --settings <path>` with our
//! attribution-off overlay instead. Claude loads that as `flagSettings` (precedence above
//! `user`/`project`/`local`), and credentials stay at the user-global keychain entry.
//!
//! The overlay file lives at `$AGENTPACK_HOME/claude-settings.json` so all projects share it.
//! When `AGENTPACK_KEEP_ATTRIBUTION=1` we delete the file (and the launcher omits `--settings`).

use std::fs;

use serde_json::json;

use crate::error::{AgentpackError, Result};
use crate::fs_util::{remove_path_any, write_json_value};
use crate::paths::agentpack_claude_settings_path;

pub(super) fn materialize_claude_settings_overlay() -> Result<()> {
    let dest = agentpack_claude_settings_path()?;
    if keep_attribution() {
        remove_path_any(&dest)?;
        return Ok(());
    }

    if let Some(parent) = dest.parent() {
        fs::create_dir_all(parent).map_err(|e| AgentpackError::io(parent, e))?;
    }
    let value = json!({
        "includeCoAuthoredBy": false,
        "attribution": { "commit": "", "pr": "" },
    });
    write_json_value(&dest, &value)?;
    tracing::debug!(path = %dest.display(), "wrote claude --settings overlay (attribution off)");
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
    use crate::fs_util::read_json_value_opt;
    use serde_json::Value;
    use serial_test::serial;
    use tempfile::tempdir;

    fn with_home<F: FnOnce()>(f: F) {
        let dir = tempdir().unwrap();
        let prev = std::env::var_os("AGENTPACK_HOME");
        std::env::set_var("AGENTPACK_HOME", dir.path());
        f();
        match prev {
            Some(v) => std::env::set_var("AGENTPACK_HOME", v),
            None => std::env::remove_var("AGENTPACK_HOME"),
        }
    }

    #[test]
    #[serial]
    fn writes_attribution_overlay_by_default() {
        with_home(|| {
            std::env::remove_var("AGENTPACK_KEEP_ATTRIBUTION");
            materialize_claude_settings_overlay().unwrap();
            let v: Value = read_json_value_opt(&agentpack_claude_settings_path().unwrap())
                .unwrap()
                .unwrap();
            assert_eq!(v["includeCoAuthoredBy"], false);
            assert_eq!(v["attribution"]["commit"], "");
            assert_eq!(v["attribution"]["pr"], "");
        });
    }

    #[test]
    #[serial]
    fn keep_env_removes_overlay_file() {
        with_home(|| {
            // First write the file, then opt out and re-run; the file must be gone.
            std::env::remove_var("AGENTPACK_KEEP_ATTRIBUTION");
            materialize_claude_settings_overlay().unwrap();
            assert!(agentpack_claude_settings_path().unwrap().exists());

            std::env::set_var("AGENTPACK_KEEP_ATTRIBUTION", "1");
            materialize_claude_settings_overlay().unwrap();
            assert!(!agentpack_claude_settings_path().unwrap().exists());
            std::env::remove_var("AGENTPACK_KEEP_ATTRIBUTION");
        });
    }
}
