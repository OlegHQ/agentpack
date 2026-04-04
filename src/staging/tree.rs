use std::fs;
use std::path::Path;

use crate::error::{AgentpackError, Result};
use crate::fs_util::resolve_tree_copy_source;

pub(super) fn copy_merge_tree(src: &Path, dst: &Path) -> Result<()> {
    let Some(effective) =
        resolve_tree_copy_source(src, "skipping dangling symlink while merging trees")?
    else {
        return Ok(());
    };

    if effective.is_dir() {
        fs::create_dir_all(dst).map_err(|e| AgentpackError::io(dst, e))?;
        for e in fs::read_dir(&effective).map_err(|e| AgentpackError::io(&effective, e))? {
            let e = e.map_err(|e| AgentpackError::io(&effective, e))?;
            copy_merge_tree(&e.path(), &dst.join(e.file_name()))?;
        }
        return Ok(());
    }

    if let Some(parent) = dst.parent() {
        fs::create_dir_all(parent).map_err(|e| AgentpackError::io(parent, e))?;
    }
    if dst.exists() {
        tracing::debug!(
            from = %src.display(),
            to = %dst.display(),
            "bundle overlay overwrites path"
        );
    }
    fs::copy(&effective, dst).map_err(|e| AgentpackError::io(dst, e))?;
    Ok(())
}
