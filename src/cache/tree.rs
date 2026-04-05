use std::fs;
use std::path::{Path, PathBuf};

use crate::error::{AgentpackError, Result};
use crate::paths;

use super::layout::{
    cache_entry_dir, collect_source_files, compute_cache_key, hash_and_copy_source_tree,
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
/// Uses a single-pass algorithm: files are read once, hashed and copied simultaneously.
/// Respects `.gitignore` — `.git/`, `target/`, and other gitignored paths are excluded from
/// both the content hash and the cached copy.
pub fn copy_package_dir_to_cache(
    from: &Path,
    identity_prefix: &str,
) -> Result<(String, String, PathBuf)> {
    paths::ensure_user_agentpack_layout()?;

    let cache_dir = paths::cache_dir()?;
    fs::create_dir_all(&cache_dir).map_err(|err| AgentpackError::io(&cache_dir, err))?;

    // Single pass: hash all files while copying them to a temp output directory.
    // We need the hash to compute the cache_key, but we need the cache_key to know
    // the output path. So we hash+copy to a temp dir, then rename.
    let tmp_out = cache_dir.join(format!(".tmp-copy-{}", std::process::id()));
    if tmp_out.exists() {
        fs::remove_dir_all(&tmp_out).map_err(|err| AgentpackError::io(&tmp_out, err))?;
    }
    fs::create_dir_all(&tmp_out).map_err(|err| AgentpackError::io(&tmp_out, err))?;

    let commit = hash_and_copy_source_tree(from, &tmp_out)?;
    let identity = format!("{identity_prefix}\0{commit}");
    let cache_key = compute_cache_key(&identity);
    let out = cache_entry_dir(&cache_key)?;

    if out.exists() {
        fs::remove_dir_all(&out).map_err(|err| AgentpackError::io(&out, err))?;
    }
    fs::rename(&tmp_out, &out).or_else(|_| {
        // rename may fail across filesystems; fall back to copy + remove
        copy_dir_recursive(&tmp_out, &out)?;
        fs::remove_dir_all(&tmp_out).map_err(|err| AgentpackError::io(&tmp_out, err))
    })?;

    normalize_plugin_cache_layout(&out)?;
    Ok((cache_key, commit, out))
}

fn copy_dir_recursive(src: &Path, dst: &Path) -> Result<()> {
    fs::create_dir_all(dst).map_err(|err| AgentpackError::io(dst, err))?;
    for entry in fs::read_dir(src).map_err(|err| AgentpackError::io(src, err))? {
        let entry = entry.map_err(|err| AgentpackError::io(src, err))?;
        let ty = entry
            .file_type()
            .map_err(|err| AgentpackError::io(entry.path(), err))?;
        let dest = dst.join(entry.file_name());
        if ty.is_dir() {
            copy_dir_recursive(&entry.path(), &dest)?;
        } else {
            fs::copy(entry.path(), &dest).map_err(|err| AgentpackError::io(&dest, err))?;
        }
    }
    Ok(())
}
