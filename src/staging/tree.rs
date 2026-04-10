use std::fs;
use std::path::Path;

use crate::error::{AgentpackError, Result};
use crate::fs_util::{fast_copy_file, resolve_tree_copy_source};

/// Copy `src` onto `dst`, merging directories. Uses `fast_copy_file` (reflink on APFS/btrfs/XFS,
/// plain `fs::copy` fallback) for leaf files and walks directories via `DirEntry::file_type()`
/// to avoid extra `stat` syscalls per child.
pub(super) fn copy_merge_tree(src: &Path, dst: &Path) -> Result<()> {
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
