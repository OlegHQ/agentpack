//! Small filesystem helpers shared across crates.

use std::fs;
use std::io::{self, ErrorKind};
use std::path::{Path, PathBuf};

use serde_json::Value;
use walkdir::{DirEntry, WalkDir};

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

/// Move a stale rebuild target aside, then remove it recursively. This avoids `Directory not
/// empty` races when the path is also the active config/cache root of the process invoking
/// `agentpack` and background writers recreate files while `remove_dir_all` is walking it.
pub(crate) fn remove_rebuild_path(path: &Path) -> Result<()> {
    let meta = match fs::symlink_metadata(path) {
        Ok(meta) => meta,
        Err(err) if err.kind() == ErrorKind::NotFound => return Ok(()),
        Err(err) => return Err(AgentpackError::io(path, err)),
    };

    if !meta.is_dir() || meta.file_type().is_symlink() {
        return remove_path_any(path);
    }

    let trash = unique_rebuild_trash_path(path);
    fs::rename(path, &trash).map_err(|err| AgentpackError::io(path, err))?;
    remove_path_any(&trash)
}

fn unique_rebuild_trash_path(path: &Path) -> PathBuf {
    let parent = path.parent().unwrap_or_else(|| Path::new("."));
    let name = path
        .file_name()
        .and_then(|s| s.to_str())
        .unwrap_or("staging");
    for attempt in 0..1000u32 {
        let candidate = parent.join(format!(
            ".agentpack-reset-{name}-{}-{attempt}",
            std::process::id()
        ));
        if !candidate.exists() {
            return candidate;
        }
    }
    parent.join(format!(
        ".agentpack-reset-{name}-{}-fallback",
        std::process::id()
    ))
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

/// Options for [`walk_dir`].
#[derive(Clone, Copy)]
pub(crate) struct WalkDirOpts {
    pub follow_links: bool,
    pub contents_first: bool,
}

impl WalkDirOpts {
    pub const fn files() -> Self {
        Self {
            follow_links: false,
            contents_first: false,
        }
    }

    pub const fn files_contents_first() -> Self {
        Self {
            follow_links: false,
            contents_first: true,
        }
    }
}

pub(crate) fn map_walk_err(err: walkdir::Error) -> AgentpackError {
    AgentpackError::Staging(err.to_string())
}

pub(crate) fn strip_under_root<'a>(path: &'a Path, root: &'a Path) -> Result<&'a Path> {
    path.strip_prefix(root).map_err(|_| {
        AgentpackError::Staging(format!(
            "path outside {}: {}",
            root.display(),
            path.display()
        ))
    })
}

pub(crate) fn walk_dir(root: &Path, opts: WalkDirOpts) -> impl Iterator<Item = Result<DirEntry>> {
    let mut walker = WalkDir::new(root).follow_links(opts.follow_links);
    if opts.contents_first {
        walker = walker.contents_first(true);
    }
    walker.into_iter().map(|entry| entry.map_err(map_walk_err))
}

/// Copy the named top-level `entries` from `src_root` into `dst_root`, merging directories. Shared
/// by every harness's user-config seeding step.
pub(crate) fn copy_selected_entries(
    src_root: &Path,
    dst_root: &Path,
    entries: &[&str],
) -> Result<()> {
    if !src_root.is_dir() {
        return Ok(());
    }
    for entry in entries {
        let src = src_root.join(entry);
        if src.exists() {
            copy_merge_tree(&src, &dst_root.join(entry))?;
        }
    }
    Ok(())
}

/// Copy `src` onto `dst`, merging directories. Uses `fast_copy_file` (reflink on APFS/btrfs/XFS,
/// plain `fs::copy` fallback) for leaf files and walks directories via `DirEntry::file_type()`
/// to avoid extra `stat` syscalls per child.
pub(crate) fn copy_merge_tree(src: &Path, dst: &Path) -> Result<()> {
    let Some(effective) =
        resolve_tree_copy_source(src, "skipping dangling symlink while merging trees")?
    else {
        return Ok(());
    };

    let meta = fs::symlink_metadata(&effective).map_err(|e| AgentpackError::io(&effective, e))?;
    if meta.is_dir() {
        copy_dir_contents(&effective, dst)
    } else {
        fast_copy_file(&effective, dst)
    }
}

fn copy_dir_contents(src: &Path, dst: &Path) -> Result<()> {
    fs::create_dir_all(dst).map_err(|e| AgentpackError::io(dst, e))?;
    for entry in fs::read_dir(src).map_err(|e| AgentpackError::io(src, e))? {
        let entry = entry.map_err(|e| AgentpackError::io(src, e))?;
        let ty = entry
            .file_type()
            .map_err(|e| AgentpackError::io(entry.path(), e))?;
        let child_src = entry.path();
        let child_dst = dst.join(entry.file_name());
        if ty.is_symlink() {
            // Delegate to copy_merge_tree so resolve_tree_copy_source handles dangling symlinks.
            copy_merge_tree(&child_src, &child_dst)?;
        } else if ty.is_dir() {
            copy_dir_contents(&child_src, &child_dst)?;
        } else if ty.is_file() {
            fast_copy_file(&child_src, &child_dst)?;
        }
    }
    Ok(())
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

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    #[cfg(unix)]
    fn copy_merge_tree_skips_dangling_symlink() {
        use std::os::unix::fs::symlink;

        let t = tempfile::tempdir().unwrap();
        let src = t.path().join("agents");
        fs::create_dir_all(&src).unwrap();
        symlink(
            "/this-path-should-not-exist-for-agentpack-test",
            src.join("code-simplifier.md"),
        )
        .unwrap();
        fs::write(src.join("ok.md"), "# ok").unwrap();
        let dst = t.path().join("out");
        copy_merge_tree(&src, &dst).unwrap();
        assert!(dst.join("ok.md").is_file());
        assert!(!dst.join("code-simplifier.md").exists());
    }
}
