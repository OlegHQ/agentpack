use std::fs;
use std::path::{Path, PathBuf};

use crate::error::{AgentpackError, Result};
use crate::paths;

use super::layout::{
    cache_entry_dir, collect_source_files, compute_cache_key, hash_directory_contents,
    normalize_plugin_cache_layout,
};

/// Copy only git-visible files from `src` to `dst` (respects `.gitignore`).
pub(super) fn copy_source_tree(src: &Path, dst: &Path) -> Result<()> {
    let files = collect_source_files(src)?;
    for rel in &files {
        let src_file = src.join(rel);
        let dst_file = dst.join(rel);
        if let Some(parent) = dst_file.parent() {
            fs::create_dir_all(parent).map_err(|err| AgentpackError::io(parent, err))?;
        }
        fs::copy(&src_file, &dst_file).map_err(|err| AgentpackError::io(&dst_file, err))?;
    }
    Ok(())
}

/// Copy a local directory into the content-addressed cache (path / local mirror / file adds).
/// Returns **`cache_key`**, **40-hex content fingerprint** (for `pack.lock` `commit`), and cache path.
///
/// Respects `.gitignore` — `.git/`, `target/`, and other gitignored paths are excluded from
/// both the content hash and the cached copy.
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
    copy_source_tree(from, &out)?;
    normalize_plugin_cache_layout(&out)?;
    Ok((cache_key, commit, out))
}
