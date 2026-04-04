//! Small filesystem helpers shared across crates.

use std::fs;
use std::io::ErrorKind;
use std::path::Path;

use crate::error::{AgentpackError, Result};

/// Resolve **`src`** for recursive tree copy: follow symlinks, or **`Ok(None)`** when the symlink is dangling (caller skips).
pub(crate) fn resolve_tree_copy_source(
    src: &Path,
    dangling_reason: &'static str,
) -> Result<Option<std::path::PathBuf>> {
    match fs::symlink_metadata(src) {
        Ok(m) if m.file_type().is_symlink() => match fs::canonicalize(src) {
            Ok(p) => Ok(Some(p)),
            Err(e) => {
                tracing::warn!(
                    path = %src.display(),
                    error = %e,
                    reason = dangling_reason,
                );
                Ok(None)
            }
        },
        Ok(_) => Ok(Some(src.to_path_buf())),
        Err(e) => Err(AgentpackError::io(src, e)),
    }
}

/// Remove a file, symlink, or directory (recursive). Missing paths are OK.
pub(crate) fn remove_path_any(path: &Path) -> Result<()> {
    let meta = match fs::symlink_metadata(path) {
        Ok(meta) => meta,
        Err(err) if err.kind() == ErrorKind::NotFound => return Ok(()),
        Err(err) => return Err(AgentpackError::io(path, err)),
    };

    if meta.file_type().is_symlink() || meta.is_file() {
        fs::remove_file(path).map_err(|err| AgentpackError::io(path, err))?;
    } else if meta.is_dir() {
        fs::remove_dir_all(path).map_err(|err| AgentpackError::io(path, err))?;
    } else {
        fs::remove_file(path).map_err(|err| AgentpackError::io(path, err))?;
    }

    Ok(())
}
