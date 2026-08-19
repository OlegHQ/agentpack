//! Keep Codex transcripts and resume indexes outside the disposable staged `CODEX_HOME`.

use std::fs;
use std::io::ErrorKind;
use std::path::{Path, PathBuf};

use crate::error::{AgentpackError, Result};
use crate::fs_util::{
    durable_path_matches, link_durable_dir, link_durable_file, recover_without_overwrite,
    write_text_file,
};
use crate::paths::{session_history_recovery_dir_for_component, staging_codex_home_dir_for_mode};

const DURABLE_DIRS: &[&str] = &["sessions", "archived_sessions"];
const PROMPT_HISTORY: &str = "history.jsonl";

pub(super) fn native_home() -> Option<PathBuf> {
    dirs::home_dir().map(|home| home.join(".codex"))
}

pub(super) fn prepare(staged_home: &Path, native_home: &Path) -> Result<()> {
    fs::create_dir_all(native_home).map_err(|e| AgentpackError::io(native_home, e))?;
    for name in DURABLE_DIRS {
        link_durable_dir(&native_home.join(name), &staged_home.join(name))?;
    }
    link_durable_file(
        &native_home.join(PROMPT_HISTORY),
        &staged_home.join(PROMPT_HISTORY),
    )?;
    ensure_native_sqlite_home(&staged_home.join("config.toml"), native_home)
}

pub(super) fn recover_all_modes(project_root: &Path, current_mode: &str) -> Result<()> {
    let Some(native_home) = native_home() else {
        return Ok(());
    };
    let current = staging_codex_home_dir_for_mode(project_root, current_mode)?;
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
        let staged = entry.path().join("codex-home");
        if !staged.is_dir() {
            continue;
        }
        reject_active_writers(&staged)?;
        let conflicts =
            session_history_recovery_dir_for_component(project_root, "codex", &mode_component)?;
        for name in DURABLE_DIRS {
            recover_without_overwrite(
                &staged.join(name),
                &native_home.join(name),
                &conflicts.join(name),
            )?;
        }
        recover_without_overwrite(
            &staged.join(PROMPT_HISTORY),
            &native_home.join(PROMPT_HISTORY),
            &conflicts.join(PROMPT_HISTORY),
        )?;
    }
    Ok(())
}

pub(super) fn verify(staged_home: &Path, native_home: &Path) -> Result<()> {
    for name in DURABLE_DIRS {
        let staged = staged_home.join(name);
        let native = native_home.join(name);
        if !durable_path_matches(&staged, &native) {
            return Err(AgentpackError::Staging(format!(
                "Codex durable session link {} does not resolve to {}",
                staged.display(),
                native.display()
            )));
        }
    }
    let staged_history = staged_home.join(PROMPT_HISTORY);
    let native_history = native_home.join(PROMPT_HISTORY);
    if !durable_path_matches(&staged_history, &native_history) {
        return Err(AgentpackError::Staging(format!(
            "Codex durable prompt history {} does not resolve to {}",
            staged_history.display(),
            native_history.display()
        )));
    }
    let config = crate::fs_util::read_toml_value_or_default(&staged_home.join("config.toml"))?;
    let configured = config.get("sqlite_home").and_then(toml::Value::as_str);
    if configured.is_none() {
        return Err(AgentpackError::Staging(
            "Codex staged config is missing durable `sqlite_home`".into(),
        ));
    }
    Ok(())
}

fn ensure_native_sqlite_home(config_path: &Path, native_home: &Path) -> Result<()> {
    let mut config = crate::fs_util::read_toml_value_or_default(config_path)?;
    let table = config.as_table_mut().ok_or_else(|| {
        AgentpackError::Staging(format!(
            "{}: top-level must be a TOML table",
            config_path.display()
        ))
    })?;
    table
        .entry("sqlite_home")
        .or_insert_with(|| toml::Value::String(native_home.to_string_lossy().into_owned()));
    let out = toml::to_string(&config)
        .map_err(|e| AgentpackError::Staging(format!("{}: {e}", config_path.display())))?;
    write_text_file(config_path, &out)
}

fn reject_active_writers(staged_home: &Path) -> Result<()> {
    let lock_dir = staged_home.join("thread-writer-locks");
    let entries = match fs::read_dir(&lock_dir) {
        Ok(entries) => entries,
        Err(e) if e.kind() == ErrorKind::NotFound => return Ok(()),
        Err(e) => return Err(AgentpackError::io(&lock_dir, e)),
    };
    for entry in entries {
        let entry = entry.map_err(|e| AgentpackError::io(&lock_dir, e))?;
        if !entry
            .file_name()
            .to_str()
            .is_some_and(|name| name.ends_with(".lock"))
        {
            continue;
        }
        let path = entry.path();
        let file = fs::OpenOptions::new()
            .read(true)
            .write(true)
            .open(&path)
            .map_err(|e| AgentpackError::io(&path, e))?;
        match file.try_lock() {
            Ok(()) => {}
            Err(std::fs::TryLockError::WouldBlock) => {
                return Err(AgentpackError::Staging(format!(
                    "active Codex session is writing under {}; close it and retry sync",
                    staged_home.display()
                )));
            }
            Err(std::fs::TryLockError::Error(e)) => {
                return Err(AgentpackError::io(&path, e));
            }
        }
    }
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn prepare_preserves_history_across_staging_rebuilds() {
        let t = tempfile::tempdir().unwrap();
        let native = t.path().join("native-codex");
        let staged = t.path().join("staging/codex-home");

        prepare(&staged, &native).unwrap();
        fs::write(staged.join("sessions/thread.jsonl"), "session").unwrap();
        fs::write(staged.join(PROMPT_HISTORY), "prompt").unwrap();
        fs::remove_dir_all(staged.parent().unwrap()).unwrap();
        prepare(&staged, &native).unwrap();

        assert_eq!(
            fs::read_to_string(staged.join("sessions/thread.jsonl")).unwrap(),
            "session"
        );
        assert_eq!(
            fs::read_to_string(staged.join(PROMPT_HISTORY)).unwrap(),
            "prompt"
        );
        let config = fs::read_to_string(staged.join("config.toml")).unwrap();
        assert!(config.contains(native.to_string_lossy().as_ref()));
    }

    #[test]
    fn prepare_respects_explicit_sqlite_home() {
        let t = tempfile::tempdir().unwrap();
        let native = t.path().join("native-codex");
        let staged = t.path().join("staging/codex-home");
        fs::create_dir_all(&staged).unwrap();
        fs::write(
            staged.join("config.toml"),
            "sqlite_home = \"/custom/state\"\n",
        )
        .unwrap();

        prepare(&staged, &native).unwrap();

        assert!(fs::read_to_string(staged.join("config.toml"))
            .unwrap()
            .contains("/custom/state"));
    }
}
