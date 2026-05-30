//! Tar.gz extraction for GitHub `codeload` archives.
//!
//! GitHub archives wrap everything in a top-level `reponame-sha/…` directory. Both extraction
//! strategies here strip that wrapper, then strip a caller-supplied repo-relative `path_prefix`
//! so only the requested subtree lands in `out_dir`:
//!
//! * [`extract_tarball_with_prefix`] streams entries straight to disk in one pass — used when the
//!   prefix is already known (a `tree` URL or plain `owner/repo/path`).
//! * [`collect_paths_and_entries`] + [`write_entries_with_prefix`] decode once into memory so the
//!   package-root prefix can be discovered from the path index first — used for `blob` URLs.

use std::collections::HashSet;
use std::fs;
use std::io::Cursor;
use std::path::Path;

use flate2::read::GzDecoder;
use tar::Archive;

use crate::error::{AgentpackError, Result};
use crate::ui::Ui;

/// Strip the GitHub top-level `reponame-sha/` wrapper from an archive path.
/// Returns the repo-relative path, or `None` for the bare wrapper dir itself.
fn repo_relative(path: &str) -> Option<&str> {
    path.split_once('/').map(|(_, rel)| rel)
}

/// Given a repo-relative path and a (trimmed) `path_prefix`, return the path relative to that
/// prefix, or `None` when the entry is outside the prefix / is the prefix dir entry itself.
fn strip_prefix<'a>(rel_in_repo: &'a str, path_prefix: &str, prefix_dir: &str) -> Option<&'a str> {
    let stripped = if path_prefix.is_empty() {
        rel_in_repo
    } else if rel_in_repo == path_prefix {
        return None;
    } else {
        rel_in_repo.strip_prefix(prefix_dir)?
    };
    (!stripped.is_empty()).then_some(stripped)
}

/// `("", "")` for an empty prefix, else `(trimmed, "trimmed/")`.
fn prefix_parts(path_prefix: &str) -> (String, String) {
    let trimmed = path_prefix.trim_matches('/').to_string();
    let dir = if trimmed.is_empty() {
        String::new()
    } else {
        format!("{trimmed}/")
    };
    (trimmed, dir)
}

fn reset_out_dir(out_dir: &Path) -> Result<()> {
    if out_dir.exists() {
        fs::remove_dir_all(out_dir).map_err(|e| AgentpackError::io(out_dir, e))?;
    }
    fs::create_dir_all(out_dir).map_err(|e| AgentpackError::io(out_dir, e))
}

/// Collect every repo-relative path in `buf` (used to locate package roots before extraction).
pub fn collect_repo_relative_paths(buf: &[u8]) -> Result<HashSet<String>> {
    let tar_gz = GzDecoder::new(Cursor::new(buf));
    let mut archive = Archive::new(tar_gz);
    let mut out = HashSet::new();
    for entry in archive
        .entries()
        .map_err(|e| AgentpackError::Archive(e.to_string()))?
    {
        let entry = entry.map_err(|e| AgentpackError::Archive(e.to_string()))?;
        let path = entry
            .path()
            .map_err(|e| AgentpackError::Archive(e.to_string()))?
            .to_string_lossy()
            .to_string();
        if let Some(rel) = repo_relative(&path) {
            out.insert(rel.to_string());
        }
    }
    Ok(out)
}

/// Extract `buf` (GitHub `codeload …/tar.gz` layout: top-level `reponame-sha/…`) into `out_dir`.
/// Returns how many **non-directory** archive entries were written (files, symlinks, etc.).
pub fn extract_tarball_with_prefix(
    buf: &[u8],
    path_prefix: &str,
    out_dir: &Path,
    ui: &Ui,
) -> Result<usize> {
    let extract_pb = ui.spinner("Extracting archive…");
    reset_out_dir(out_dir)?;

    let tar_gz = GzDecoder::new(Cursor::new(buf));
    let mut archive = Archive::new(tar_gz);
    let (path_prefix, prefix_dir) = prefix_parts(path_prefix);

    let mut files_written = 0usize;
    for entry in archive
        .entries()
        .map_err(|e| AgentpackError::Archive(e.to_string()))?
    {
        let mut entry = entry.map_err(|e| AgentpackError::Archive(e.to_string()))?;
        let path = entry
            .path()
            .map_err(|e| AgentpackError::Archive(e.to_string()))?
            .to_string_lossy()
            .to_string();

        let Some(rel_in_repo) = repo_relative(&path) else {
            continue;
        };
        let Some(extract_rel) = strip_prefix(rel_in_repo, &path_prefix, &prefix_dir) else {
            continue;
        };

        let out_path = out_dir.join(Path::new(extract_rel));
        if entry.header().entry_type().is_dir() {
            fs::create_dir_all(&out_path).map_err(|e| AgentpackError::io(&out_path, e))?;
        } else {
            if let Some(parent) = out_path.parent() {
                fs::create_dir_all(parent).map_err(|e| AgentpackError::io(parent, e))?;
            }
            let mut f =
                fs::File::create(&out_path).map_err(|e| AgentpackError::io(&out_path, e))?;
            let _: u64 =
                std::io::copy(&mut entry, &mut f).map_err(|e| AgentpackError::io(&out_path, e))?;
            files_written += 1;
        }
    }

    Ui::finish_spinner(extract_pb.as_ref(), "Extracted files");
    Ok(files_written)
}

/// A buffered tar entry (one in-memory decode pass; data is empty for directories).
pub struct TarEntry {
    rel_in_repo: String,
    is_dir: bool,
    data: Vec<u8>,
}

/// Decompress a tarball once, collecting both the path index and all entry data.
pub fn collect_paths_and_entries(buf: &[u8]) -> Result<(HashSet<String>, Vec<TarEntry>)> {
    use std::io::Read;

    let tar_gz = GzDecoder::new(Cursor::new(buf));
    let mut archive = Archive::new(tar_gz);
    let mut index = HashSet::new();
    let mut entries = Vec::new();

    for entry in archive
        .entries()
        .map_err(|e| AgentpackError::Archive(e.to_string()))?
    {
        let mut entry = entry.map_err(|e| AgentpackError::Archive(e.to_string()))?;
        let path = entry
            .path()
            .map_err(|e| AgentpackError::Archive(e.to_string()))?
            .to_string_lossy()
            .to_string();

        let Some(rel_in_repo) = repo_relative(&path) else {
            continue;
        };
        let rel_in_repo = rel_in_repo.to_string();
        let is_dir = entry.header().entry_type().is_dir();
        index.insert(rel_in_repo.clone());

        let data = if is_dir {
            Vec::new()
        } else {
            let mut data = Vec::new();
            entry
                .read_to_end(&mut data)
                .map_err(|e| AgentpackError::Archive(e.to_string()))?;
            data
        };

        entries.push(TarEntry {
            rel_in_repo,
            is_dir,
            data,
        });
    }

    Ok((index, entries))
}

/// Write buffered tar entries matching a given prefix to the output directory.
pub fn write_entries_with_prefix(
    entries: &[TarEntry],
    path_prefix: &str,
    out_dir: &Path,
    ui: &Ui,
) -> Result<usize> {
    use std::io::Write;

    let extract_pb = ui.spinner("Extracting archive…");
    reset_out_dir(out_dir)?;
    let (path_prefix, prefix_dir) = prefix_parts(path_prefix);

    let mut files_written = 0usize;
    for entry in entries {
        let Some(extract_rel) = strip_prefix(&entry.rel_in_repo, &path_prefix, &prefix_dir) else {
            continue;
        };

        let out_path = out_dir.join(Path::new(extract_rel));
        if entry.is_dir {
            fs::create_dir_all(&out_path).map_err(|e| AgentpackError::io(&out_path, e))?;
        } else {
            if let Some(parent) = out_path.parent() {
                fs::create_dir_all(parent).map_err(|e| AgentpackError::io(parent, e))?;
            }
            let mut f =
                fs::File::create(&out_path).map_err(|e| AgentpackError::io(&out_path, e))?;
            f.write_all(&entry.data)
                .map_err(|e| AgentpackError::io(&out_path, e))?;
            files_written += 1;
        }
    }

    Ui::finish_spinner(extract_pb.as_ref(), "Extracted files");
    Ok(files_written)
}

#[cfg(test)]
mod tests {
    use super::*;
    use flate2::write::GzEncoder;
    use flate2::Compression;
    use std::io::Write;
    use tar::{Builder, Header};

    fn minimal_github_tar_gz(repo_root_dir: &str, rel_file: &str, body: &[u8]) -> Vec<u8> {
        let mut tar_buf = Vec::new();
        {
            let mut ar = Builder::new(&mut tar_buf);
            let mut h = Header::new_gnu();
            let p = format!("{repo_root_dir}/{rel_file}");
            h.set_path(&p).unwrap();
            h.set_size(body.len() as u64);
            h.set_cksum();
            ar.append(&h, body).unwrap();
            ar.finish().unwrap();
        }
        let mut gz = Vec::new();
        let mut enc = GzEncoder::new(&mut gz, Compression::default());
        enc.write_all(&tar_buf).unwrap();
        enc.finish().unwrap();
        gz
    }

    #[test]
    fn extract_counts_files_and_zero_when_prefix_misses() {
        let gz = minimal_github_tar_gz("repo-abcdef0", "plugins/pkg/readme.md", b"ok\n");
        let dir = tempfile::tempdir().unwrap();
        let ui = crate::ui::Ui::test_stub();
        let n = extract_tarball_with_prefix(&gz, "plugins/pkg", dir.path(), &ui).unwrap();
        assert_eq!(n, 1);
        assert!(dir.path().join("readme.md").is_file());

        let dir2 = tempfile::tempdir().unwrap();
        let n2 = extract_tarball_with_prefix(&gz, "does-not-exist", dir2.path(), &ui).unwrap();
        assert_eq!(n2, 0);
    }
}
