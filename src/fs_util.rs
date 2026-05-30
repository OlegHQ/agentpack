//! Small filesystem helpers shared across crates.

use std::fs;
use std::io::{self, ErrorKind};
use std::path::Path;

use serde_json::Value;

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

/// Fast single-file copy: try reflink (APFS `clonefile`, btrfs/XFS `FICLONE`), fall back to
/// `fs::copy` on cross-device or unsupported filesystems. An existing destination is overwritten
/// (reflink requires a non-existing target, so we remove any prior file first).
pub(crate) fn fast_copy_file(src: &Path, dst: &Path) -> Result<()> {
    if let Some(parent) = dst.parent() {
        fs::create_dir_all(parent).map_err(|e| AgentpackError::io(parent, e))?;
    }
    match fs::symlink_metadata(dst) {
        Ok(m) if m.is_dir() => {
            return Err(AgentpackError::io(
                dst,
                io::Error::new(
                    ErrorKind::AlreadyExists,
                    "fast_copy_file: destination is a directory",
                ),
            ));
        }
        Ok(_) => {
            fs::remove_file(dst).map_err(|e| AgentpackError::io(dst, e))?;
        }
        Err(e) if e.kind() == ErrorKind::NotFound => {}
        Err(e) => return Err(AgentpackError::io(dst, e)),
    }
    match reflink_copy::reflink_or_copy(src, dst) {
        Ok(_) => Ok(()),
        Err(e) => Err(AgentpackError::io(dst, e)),
    }
}

/// Remove a file, symlink, or directory (recursive). Missing paths are OK.
pub(crate) fn remove_path_any(path: &Path) -> Result<()> {
    let meta = match fs::symlink_metadata(path) {
        Ok(meta) => meta,
        Err(err) if err.kind() == ErrorKind::NotFound => return Ok(()),
        Err(err) => return Err(AgentpackError::io(path, err)),
    };

    if meta.is_dir() && !meta.file_type().is_symlink() {
        fs::remove_dir_all(path).map_err(|err| AgentpackError::io(path, err))?;
    } else {
        fs::remove_file(path).map_err(|err| AgentpackError::io(path, err))?;
    }

    Ok(())
}

/// Read and parse a JSON file. Errors if the file is missing.
pub(crate) fn read_json_value(path: &Path) -> Result<Value> {
    let raw = fs::read_to_string(path).map_err(|err| AgentpackError::io(path, err))?;
    serde_json::from_str(&raw)
        .map_err(|err| AgentpackError::io(path, io::Error::new(ErrorKind::InvalidData, err)))
}

/// Read and parse a JSON file. Returns `Ok(None)` when the file does not exist.
pub(crate) fn read_json_value_opt(path: &Path) -> Result<Option<Value>> {
    match fs::read_to_string(path) {
        Ok(s) => {
            let v = serde_json::from_str(&s)
                .map_err(|e| AgentpackError::io(path, io::Error::new(ErrorKind::InvalidData, e)))?;
            Ok(Some(v))
        }
        Err(e) if e.kind() == ErrorKind::NotFound => Ok(None),
        Err(e) => Err(AgentpackError::io(path, e)),
    }
}

/// Write pretty-printed JSON, creating parent directories as needed.
pub(crate) fn write_json_value(path: &Path, value: &Value) -> Result<()> {
    if let Some(parent) = path.parent() {
        fs::create_dir_all(parent).map_err(|e| AgentpackError::io(parent, e))?;
    }
    let s = serde_json::to_string_pretty(value)
        .map_err(|e| AgentpackError::io(path, io::Error::new(ErrorKind::InvalidData, e)))?;
    fs::write(path, s).map_err(|e| AgentpackError::io(path, e))
}

/// Write a text file, creating parent directories as needed.
pub(crate) fn write_text_file(path: &Path, contents: &str) -> Result<()> {
    if let Some(parent) = path.parent() {
        fs::create_dir_all(parent).map_err(|e| AgentpackError::io(parent, e))?;
    }
    fs::write(path, contents).map_err(|e| AgentpackError::io(path, e))
}

/// Read a TOML file into a [`toml::Value`], or return an empty table when the file does not exist.
/// Centralizes the "load config-or-default" pattern used by the staging TOML writers.
pub(crate) fn read_toml_value_or_default(path: &Path) -> Result<toml::Value> {
    if !path.is_file() {
        return Ok(toml::Value::Table(Default::default()));
    }
    let raw = fs::read_to_string(path).map_err(|e| AgentpackError::io(path, e))?;
    toml::from_str(&raw)
        .map_err(|e| AgentpackError::io(path, io::Error::new(ErrorKind::InvalidData, e)))
}

/// Parse a JSONC (JSON with comments) string into a typed value.
pub(crate) fn parse_jsonc<T: serde::de::DeserializeOwned>(raw: &str) -> serde_json::Result<T> {
    let mut buf = raw.as_bytes().to_vec();
    let _ = json_strip_comments::strip_slice(&mut buf);
    serde_json::from_slice(&buf)
}

/// Truncate a string to at most `max_chars` characters.
/// Optimized for ASCII (hex strings, cache keys) — uses byte slicing when safe.
pub(crate) fn truncate_str(value: &str, max_chars: usize) -> String {
    if value.len() <= max_chars {
        return value.to_string();
    }
    // Fast path: if the boundary is valid UTF-8, slice directly.
    if value.is_char_boundary(max_chars) {
        return value[..max_chars].to_string();
    }
    // Fallback for multi-byte chars at the boundary.
    value.chars().take(max_chars).collect()
}

/// Stream a file's contents into a SHA-256 hasher in 8 KiB chunks.
pub(crate) fn stream_file_into_hasher(path: &Path, hasher: &mut sha2::Sha256) -> Result<()> {
    use sha2::Digest;
    use std::io::Read;
    let file = fs::File::open(path).map_err(|e| AgentpackError::io(path, e))?;
    let mut reader = std::io::BufReader::new(file);
    let mut buf = [0u8; 8192];
    loop {
        let n = reader
            .read(&mut buf)
            .map_err(|e| AgentpackError::io(path, e))?;
        if n == 0 {
            break;
        }
        hasher.update(&buf[..n]);
    }
    Ok(())
}
