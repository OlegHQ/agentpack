use std::fs;
use std::io::ErrorKind;
use std::path::Path;

use serde_json::Value;

use crate::error::{AgentpackError, Result};

pub(super) fn read_json_file(path: &Path) -> Result<Option<Value>> {
    match fs::read_to_string(path) {
        Ok(s) => {
            let v = serde_json::from_str(&s).map_err(|e| {
                AgentpackError::Staging(format!("invalid JSON in {}: {e}", path.display()))
            })?;
            Ok(Some(v))
        }
        Err(e) if e.kind() == ErrorKind::NotFound => Ok(None),
        Err(e) => Err(AgentpackError::io(path, e)),
    }
}

pub(super) fn write_json_file(path: &Path, value: &Value) -> Result<()> {
    if let Some(parent) = path.parent() {
        fs::create_dir_all(parent).map_err(|e| AgentpackError::io(parent, e))?;
    }
    let s = serde_json::to_string_pretty(value)
        .map_err(|e| AgentpackError::Staging(format!("serialize JSON: {e}")))?;
    fs::write(path, s).map_err(|e| AgentpackError::io(path, e))?;
    Ok(())
}

pub(super) fn write_text_file(path: &Path, contents: &str) -> Result<()> {
    if let Some(parent) = path.parent() {
        fs::create_dir_all(parent).map_err(|e| AgentpackError::io(parent, e))?;
    }
    fs::write(path, contents).map_err(|e| AgentpackError::io(path, e))?;
    Ok(())
}
