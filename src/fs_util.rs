//! Small filesystem helpers shared across crates.

use std::fs;
use std::io::{self, ErrorKind, Write};
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

/// Point a staged directory at a durable native directory. The link must remain live: a copied
/// fallback would look correct until the next staging reset, then lose every later harness write.
pub(crate) fn link_durable_dir(native: &Path, staged: &Path) -> Result<()> {
    fs::create_dir_all(native).map_err(|e| AgentpackError::io(native, e))?;
    remove_path_any(staged)?;
    if let Some(parent) = staged.parent() {
        fs::create_dir_all(parent).map_err(|e| AgentpackError::io(parent, e))?;
    }
    #[cfg(unix)]
    std::os::unix::fs::symlink(native, staged).map_err(|e| AgentpackError::io(staged, e))?;
    #[cfg(windows)]
    {
        if std::os::windows::fs::symlink_dir(native, staged).is_err() {
            junction::create(native, staged).map_err(|e| AgentpackError::io(staged, e))?;
        }
    }
    Ok(())
}

/// Point a staged mutable file at its durable native copy. Windows prefers a hard link because it
/// works without Developer Mode; a file symlink covers cross-volume staging when privileges allow.
pub(crate) fn link_durable_file(native: &Path, staged: &Path) -> Result<()> {
    if let Some(parent) = native.parent() {
        fs::create_dir_all(parent).map_err(|e| AgentpackError::io(parent, e))?;
    }
    fs::OpenOptions::new()
        .create(true)
        .append(true)
        .open(native)
        .map_err(|e| AgentpackError::io(native, e))?;
    remove_path_any(staged)?;
    if let Some(parent) = staged.parent() {
        fs::create_dir_all(parent).map_err(|e| AgentpackError::io(parent, e))?;
    }
    #[cfg(unix)]
    std::os::unix::fs::symlink(native, staged).map_err(|e| AgentpackError::io(staged, e))?;
    #[cfg(windows)]
    {
        if fs::hard_link(native, staged).is_err() {
            std::os::windows::fs::symlink_file(native, staged).map_err(|e| {
                AgentpackError::io(
                    staged,
                    io::Error::new(
                        e.kind(),
                        format!(
                            "cannot create durable file link: {e}; keep staging on the same volume or enable Windows Developer Mode"
                        ),
                    ),
                )
            })?;
        }
    }
    Ok(())
}

/// True when `path` resolves to `target`, for both symlinks and Windows junctions/hard links.
pub(crate) fn durable_path_matches(path: &Path, target: &Path) -> bool {
    same_file::is_same_file(path, target).unwrap_or(false)
}

/// Recover a legacy staged file/tree without ever replacing native harness state. Differing
/// collisions are copied to `conflicts_root`, retaining their relative path plus a content hash.
pub(crate) fn recover_without_overwrite(
    source: &Path,
    destination: &Path,
    conflicts_root: &Path,
) -> Result<()> {
    let source_meta = match fs::symlink_metadata(source) {
        Ok(meta) => meta,
        Err(e) if e.kind() == ErrorKind::NotFound => return Ok(()),
        Err(e) => return Err(AgentpackError::io(source, e)),
    };
    if source_meta.file_type().is_symlink() {
        if durable_path_matches(source, destination) {
            return Ok(());
        }
        return Err(AgentpackError::Staging(format!(
            "refusing to recover unexpected symlink {}",
            source.display()
        )));
    }
    if source_meta.is_file() {
        return recover_file(
            source,
            destination,
            conflicts_root,
            Path::new("history.jsonl"),
        );
    }
    if !source_meta.is_dir() {
        return Ok(());
    }
    if durable_path_matches(source, destination) {
        return Ok(());
    }
    for entry in WalkDir::new(source).follow_links(false).into_iter() {
        let entry = entry.map_err(map_walk_err)?;
        let rel = entry.path().strip_prefix(source).map_err(|_| {
            AgentpackError::Staging(format!(
                "path outside recovery root: {}",
                entry.path().display()
            ))
        })?;
        if rel.as_os_str().is_empty() {
            continue;
        }
        let ty = entry.file_type();
        if ty.is_symlink() {
            return Err(AgentpackError::Staging(format!(
                "refusing to recover symlink inside session history: {}",
                entry.path().display()
            )));
        }
        if ty.is_dir() {
            let dir = destination.join(rel);
            fs::create_dir_all(&dir).map_err(|e| AgentpackError::io(&dir, e))?;
        } else if ty.is_file() {
            recover_file(entry.path(), &destination.join(rel), conflicts_root, rel)?;
        }
    }
    Ok(())
}

fn file_sha256(path: &Path) -> Result<[u8; 32]> {
    use sha2::Digest;
    let mut hasher = sha2::Sha256::new();
    stream_file_into_hasher(path, &mut hasher)?;
    Ok(hasher.finalize().into())
}

fn recover_file(
    source: &Path,
    destination: &Path,
    conflicts_root: &Path,
    rel: &Path,
) -> Result<()> {
    if let Some(parent) = destination.parent() {
        fs::create_dir_all(parent).map_err(|e| AgentpackError::io(parent, e))?;
    }
    match fs::OpenOptions::new()
        .write(true)
        .create_new(true)
        .open(destination)
    {
        Ok(mut output) => {
            let before = fs::metadata(source).map_err(|e| AgentpackError::io(source, e))?;
            let mut input = io::BufReader::new(
                fs::File::open(source).map_err(|e| AgentpackError::io(source, e))?,
            );
            io::copy(&mut input, &mut output).map_err(|e| AgentpackError::io(destination, e))?;
            output
                .flush()
                .map_err(|e| AgentpackError::io(destination, e))?;
            let after = fs::metadata(source).map_err(|e| AgentpackError::io(source, e))?;
            if before.len() != after.len() || before.modified().ok() != after.modified().ok() {
                drop(output);
                let _ = fs::remove_file(destination);
                return Err(AgentpackError::Staging(format!(
                    "session history changed during recovery: {}; close the active harness and retry",
                    source.display()
                )));
            }
            Ok(())
        }
        Err(e) if e.kind() == ErrorKind::AlreadyExists => {
            let source_hash = file_sha256(source)?;
            if file_sha256(destination)? == source_hash {
                return Ok(());
            }
            let hash = hex::encode(&source_hash[..6]);
            let name = rel
                .file_name()
                .and_then(|n| n.to_str())
                .unwrap_or("session");
            let conflict_rel = rel.with_file_name(format!("{name}.{hash}.conflict"));
            let conflict = conflicts_root.join(conflict_rel);
            if conflict.is_file() && file_sha256(&conflict)? == source_hash {
                return Ok(());
            }
            if let Some(parent) = conflict.parent() {
                fs::create_dir_all(parent).map_err(|e| AgentpackError::io(parent, e))?;
            }
            fs::copy(source, &conflict).map_err(|e| AgentpackError::io(&conflict, e))?;
            tracing::warn!(
                source = %source.display(),
                native = %destination.display(),
                recovery = %conflict.display(),
                "preserved conflicting legacy session history"
            );
            Ok(())
        }
        Err(e) => Err(AgentpackError::io(destination, e)),
    }
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
    fn recovery_keeps_native_collision_and_saves_staged_copy() {
        let t = tempfile::tempdir().unwrap();
        let source = t.path().join("staged/sessions");
        let destination = t.path().join("native/sessions");
        let conflicts = t.path().join("recovery");
        fs::create_dir_all(&source).unwrap();
        fs::create_dir_all(&destination).unwrap();
        fs::write(source.join("new.jsonl"), "new").unwrap();
        fs::write(source.join("collision.jsonl"), "staged").unwrap();
        fs::write(destination.join("collision.jsonl"), "native").unwrap();

        recover_without_overwrite(&source, &destination, &conflicts).unwrap();

        assert_eq!(
            fs::read_to_string(destination.join("new.jsonl")).unwrap(),
            "new"
        );
        assert_eq!(
            fs::read_to_string(destination.join("collision.jsonl")).unwrap(),
            "native"
        );
        let recovered = fs::read_dir(&conflicts)
            .unwrap()
            .map(|entry| entry.unwrap().path())
            .find(|path| {
                path.file_name()
                    .unwrap()
                    .to_string_lossy()
                    .starts_with("collision.")
            })
            .unwrap();
        assert_eq!(fs::read_to_string(recovered).unwrap(), "staged");
    }

    #[test]
    fn durable_links_share_writes_with_native_state() {
        let t = tempfile::tempdir().unwrap();
        let native_dir = t.path().join("native/sessions");
        let staged_dir = t.path().join("staged/sessions");
        let native_file = t.path().join("native/history.jsonl");
        let staged_file = t.path().join("staged/history.jsonl");

        link_durable_dir(&native_dir, &staged_dir).unwrap();
        link_durable_file(&native_file, &staged_file).unwrap();
        fs::write(staged_dir.join("thread.jsonl"), "thread").unwrap();
        fs::write(&staged_file, "prompt").unwrap();

        assert!(durable_path_matches(&staged_dir, &native_dir));
        assert!(durable_path_matches(&staged_file, &native_file));
        assert_eq!(
            fs::read_to_string(native_dir.join("thread.jsonl")).unwrap(),
            "thread"
        );
        assert_eq!(fs::read_to_string(native_file).unwrap(), "prompt");
    }

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
