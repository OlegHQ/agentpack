//! Keep Grok transcripts in the native user history while configuration stays staged.

use std::fs;
use std::io::ErrorKind;
use std::path::{Path, PathBuf};

use crate::error::{AgentpackError, Result};
use crate::fs_util::{durable_path_matches, link_durable_dir, recover_without_overwrite};
use crate::paths::{session_history_recovery_dir_for_component, staging_grok_home_dir_for_mode};

const SESSIONS: &str = "sessions";

pub(super) fn native_home() -> Option<PathBuf> {
    dirs::home_dir().map(|home| home.join(".grok"))
}

pub(super) fn prepare(staged_home: &Path, native_home: &Path) -> Result<()> {
    fs::create_dir_all(native_home).map_err(|e| AgentpackError::io(native_home, e))?;
    link_durable_dir(&native_home.join(SESSIONS), &staged_home.join(SESSIONS))
}

pub(super) fn recover_all_modes(project_root: &Path, current_mode: &str) -> Result<()> {
    let Some(native_home) = native_home() else {
        return Ok(());
    };
    let current = staging_grok_home_dir_for_mode(project_root, current_mode)?;
    let Some(modes_dir) = current.parent().and_then(Path::parent) else {
        return Ok(());
    };
    let entries = match fs::read_dir(modes_dir) {
        Ok(entries) => entries,
        Err(e) if e.kind() == ErrorKind::NotFound => return Ok(()),
        Err(e) => return Err(AgentpackError::io(modes_dir, e)),
    };
    for entry in entries {
        let entry = entry.map_err(|e| AgentpackError::io(modes_dir, e))?;
        let ty = entry
            .file_type()
            .map_err(|e| AgentpackError::io(entry.path(), e))?;
        if !ty.is_dir() || ty.is_symlink() {
            continue;
        }
        let mode_component = entry.file_name().to_string_lossy().into_owned();
        let staged = entry.path().join("grok-home");
        if !staged.is_dir() {
            continue;
        }
        let conflicts =
            session_history_recovery_dir_for_component(project_root, "grok", &mode_component)?;
        recover_without_overwrite(
            &staged.join(SESSIONS),
            &native_home.join(SESSIONS),
            &conflicts.join(SESSIONS),
        )?;
    }
    Ok(())
}

pub(super) fn verify(staged_home: &Path, native_home: &Path) -> Result<()> {
    let staged = staged_home.join(SESSIONS);
    let native = native_home.join(SESSIONS);
    if durable_path_matches(&staged, &native) {
        return Ok(());
    }
    Err(AgentpackError::Staging(format!(
        "Grok durable session link {} does not resolve to {}",
        staged.display(),
        native.display()
    )))
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn prepare_preserves_sessions_across_staging_rebuilds() {
        let t = tempfile::tempdir().unwrap();
        let native = t.path().join("native-grok");
        let staged = t.path().join("staging/grok-home");

        prepare(&staged, &native).unwrap();
        fs::write(staged.join("sessions/thread.jsonl"), "session").unwrap();
        fs::remove_dir_all(staged.parent().unwrap()).unwrap();
        prepare(&staged, &native).unwrap();

        assert_eq!(
            fs::read_to_string(staged.join("sessions/thread.jsonl")).unwrap(),
            "session"
        );
    }
}
