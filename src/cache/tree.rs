use std::fs;
use std::path::{Path, PathBuf};

use crate::error::{AgentpackError, Result};
use crate::paths;

use super::layout::{
    cache_entry_dir, compute_cache_key, hash_directory_contents, normalize_plugin_cache_layout,
};

pub(super) fn copy_tree_files(src: &Path, dst: &Path) -> Result<()> {
    let Some(effective) = crate::fs_util::resolve_tree_copy_source(
        src,
        "skipping dangling symlink while copying into cache",
    )?
    else {
        return Ok(());
    };

    if effective.is_file() {
        if let Some(parent) = dst.parent() {
            fs::create_dir_all(parent).map_err(|err| AgentpackError::io(parent, err))?;
        }
        fs::copy(&effective, dst).map_err(|err| AgentpackError::io(dst, err))?;
        return Ok(());
    }

    if effective.is_dir() {
        fs::create_dir_all(dst).map_err(|err| AgentpackError::io(dst, err))?;
        for entry in fs::read_dir(&effective).map_err(|err| AgentpackError::io(&effective, err))? {
            let entry = entry.map_err(|err| AgentpackError::io(&effective, err))?;
            copy_tree_files(&entry.path(), &dst.join(entry.file_name()))?;
        }
        return Ok(());
    }

    Ok(())
}

/// Copy a local directory into the content-addressed cache (path / local mirror / file adds).
/// Returns **`cache_key`**, **40-hex content fingerprint** (for `pack.lock` `commit`), and cache path.
pub fn copy_package_dir_to_cache(
    from: &Path,
    identity_prefix: &str,
) -> Result<(String, String, PathBuf)> {
    paths::ensure_user_agentpack_layout()?;
    let commit = hash_directory_contents(from)?;
    let identity = format!("{identity_prefix}\0{commit}");
    let cache_key = compute_cache_key(&identity);
    let out = cache_entry_dir(&cache_key)?;

    if out.exists() {
        fs::remove_dir_all(&out).map_err(|err| AgentpackError::io(&out, err))?;
    }

    let cache_dir = paths::cache_dir()?;
    fs::create_dir_all(&cache_dir).map_err(|err| AgentpackError::io(&cache_dir, err))?;
    fs::create_dir_all(&out).map_err(|err| AgentpackError::io(&out, err))?;
    copy_tree_files(from, &out)?;
    normalize_plugin_cache_layout(&out)?;
    Ok((cache_key, commit, out))
}
