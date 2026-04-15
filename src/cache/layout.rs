use std::collections::HashSet;
use std::fs;
use std::path::{Path, PathBuf};

use ignore::WalkBuilder;
use sha2::{Digest, Sha256};

use crate::error::{AgentpackError, Result};
use crate::fs_util::{read_json_value, write_json_value};
use crate::manifest::AgentpackManifest;
use crate::paths;

/// `hex(SHA256(identity_string))` — identity should include stable source + commit.
pub fn compute_cache_key(identity: &str) -> String {
    let mut hash = Sha256::new();
    hash.update(identity.as_bytes());
    hex::encode(hash.finalize())
}

pub fn cache_entry_dir(cache_key: &str) -> Result<PathBuf> {
    Ok(paths::cache_dir()?.join(cache_key))
}

pub fn claude_plugin_manifest_path(cache_root: &Path) -> PathBuf {
    cache_root.join(".claude-plugin").join("plugin.json")
}

pub fn cursor_plugin_manifest_path(cache_root: &Path) -> PathBuf {
    cache_root.join(".cursor-plugin").join("plugin.json")
}

/// Legacy helper name: true if Claude and/or Cursor plugin manifest exists.
pub fn cache_has_plugin_manifest(cache_root: &Path) -> bool {
    claude_plugin_manifest_path(cache_root).is_file()
        || cursor_plugin_manifest_path(cache_root).is_file()
}

/// Paths relative to a **package directory** that identify it as a fetchable pack (plugin, skill, or nested manifest).
pub fn cache_dir_is_package_root_in_filesystem(dir: &Path) -> bool {
    dir.join("SKILL.md").is_file()
        || cache_has_plugin_manifest(dir)
        || dir.join(crate::paths::MANIFEST_NAME).is_file()
}

/// Same semantics as [`cache_dir_is_package_root_in_filesystem`], but for paths in a repo-relative path index (forward slashes).
pub fn repo_dir_is_package_root(rel_paths: &HashSet<String>, dir: &str) -> bool {
    let dir = dir.trim_matches('/');
    let package_path = |leaf: &str| {
        if dir.is_empty() {
            leaf.to_string()
        } else {
            format!("{dir}/{leaf}")
        }
    };
    rel_paths.contains(&package_path(".claude-plugin/plugin.json"))
        || rel_paths.contains(&package_path(".cursor-plugin/plugin.json"))
        || rel_paths.contains(&package_path("SKILL.md"))
        || rel_paths.contains(&package_path(crate::paths::MANIFEST_NAME))
}

pub fn ensure_skill_md(cache_root: &Path) -> Result<()> {
    let skill = cache_root.join("SKILL.md");
    if skill.is_file() {
        return Ok(());
    }
    Err(AgentpackError::MissingSkillMd(cache_root.to_path_buf()))
}

pub fn ensure_plugin_manifest(cache_root: &Path) -> Result<()> {
    if cache_has_plugin_manifest(cache_root) {
        return Ok(());
    }
    Err(AgentpackError::MissingPluginManifest(
        cache_root.to_path_buf(),
    ))
}

/// Collect files under `root` respecting `.gitignore` rules.
///
/// Skips `.git/` directories and any paths matched by `.gitignore`, `.git/info/exclude`,
/// and the global gitignore. Hidden files (dotfiles) are **not** skipped — pack content
/// like `.claude-plugin/` must be included. Returns sorted relative paths (files only).
pub fn collect_source_files(root: &Path) -> Result<Vec<PathBuf>> {
    let mut files = Vec::new();
    let walker = WalkBuilder::new(root)
        .hidden(false)
        .git_ignore(true)
        .git_global(true)
        .git_exclude(true)
        .parents(true)
        .follow_links(false)
        .build();
    for entry in walker {
        let entry = entry.map_err(|e| AgentpackError::Cache(e.to_string()))?;
        if !entry.file_type().is_some_and(|ft| ft.is_file()) {
            continue;
        }
        let rel = entry
            .path()
            .strip_prefix(root)
            .map_err(|e| AgentpackError::Cache(e.to_string()))?;
        files.push(rel.to_path_buf());
    }
    files.sort();
    Ok(files)
}

/// Hash directory contents for stable path-sourced pins (40 hex for `pack.lock` commit field).
/// Uses streaming I/O — files are fed through the hasher in chunks, not loaded fully into memory.
pub fn hash_directory_contents(root: &Path) -> Result<String> {
    use std::io::Read;

    let files = collect_source_files(root)?;

    let mut hash = Sha256::new();
    let mut buf = [0u8; 8192];
    for rel in files {
        hash.update(rel.as_os_str().as_encoded_bytes());
        hash.update([0]);
        let path = root.join(&rel);
        let file = fs::File::open(&path).map_err(|err| AgentpackError::io(&path, err))?;
        let mut reader = std::io::BufReader::new(file);
        loop {
            let n = reader
                .read(&mut buf)
                .map_err(|err| AgentpackError::io(&path, err))?;
            if n == 0 {
                break;
            }
            hash.update(&buf[..n]);
        }
    }

    let full = hex::encode(hash.finalize());
    Ok(full[..40].to_string())
}

/// Single-pass: hash directory contents while copying to destination.
/// Returns the 40-hex content hash. Files are read once, hashed and copied simultaneously.
pub(super) fn hash_and_copy_source_tree(src: &Path, dst: &Path) -> Result<String> {
    use std::io::{Read, Write};

    let files = collect_source_files(src)?;

    let mut hash = Sha256::new();
    let mut buf = [0u8; 8192];
    for rel in &files {
        // Hash: include relative path in the digest
        hash.update(rel.as_os_str().as_encoded_bytes());
        hash.update([0]);

        let src_file = src.join(rel);
        let dst_file = dst.join(rel);
        if let Some(parent) = dst_file.parent() {
            fs::create_dir_all(parent).map_err(|err| AgentpackError::io(parent, err))?;
        }

        let in_file =
            fs::File::open(&src_file).map_err(|err| AgentpackError::io(&src_file, err))?;
        let mut reader = std::io::BufReader::new(in_file);
        let mut writer = std::io::BufWriter::new(
            fs::File::create(&dst_file).map_err(|err| AgentpackError::io(&dst_file, err))?,
        );

        loop {
            let n = reader
                .read(&mut buf)
                .map_err(|err| AgentpackError::io(&src_file, err))?;
            if n == 0 {
                break;
            }
            hash.update(&buf[..n]);
            writer
                .write_all(&buf[..n])
                .map_err(|err| AgentpackError::io(&dst_file, err))?;
        }
    }

    let full = hex::encode(hash.finalize());
    Ok(full[..40].to_string())
}

fn synthesize_cursor_manifest_from_claude(cache_root: &Path, claude_manifest: &Path) -> Result<()> {
    let value = read_json_value(claude_manifest)?;
    let cursor_dir = cache_root.join(".cursor-plugin");
    fs::create_dir_all(&cursor_dir).map_err(|err| AgentpackError::io(&cursor_dir, err))?;
    let name = value["name"].as_str().unwrap_or("plugin");
    let stub = serde_json::json!({
        "name": name,
        "displayName": value.get("displayName").and_then(|x| x.as_str()).unwrap_or(name),
        "version": value.get("version").and_then(|x| x.as_str()).unwrap_or("1.0.0"),
        "description": value.get("description").and_then(|x| x.as_str()).unwrap_or(""),
    });
    write_json_value(&cursor_dir.join("plugin.json"), &stub)
}

fn synthesize_claude_manifest_from_cursor(cache_root: &Path, cursor_manifest: &Path) -> Result<()> {
    let value = read_json_value(cursor_manifest)?;
    let claude_dir = cache_root.join(".claude-plugin");
    fs::create_dir_all(&claude_dir).map_err(|err| AgentpackError::io(&claude_dir, err))?;
    let name = value["name"].as_str().unwrap_or("plugin");
    let stub = serde_json::json!({
        "name": name,
        "version": value.get("version").and_then(|x| x.as_str()).unwrap_or("1.0.0"),
        "description": value.get("description").or_else(|| value.get("displayName")).and_then(|x| x.as_str()).unwrap_or(""),
    });
    write_json_value(&claude_dir.join("plugin.json"), &stub)
}

fn synthesize_plugin_manifests_from_agentpack_manifest(cache_root: &Path) -> Result<()> {
    let Some(manifest) = AgentpackManifest::load(cache_root)? else {
        return Ok(());
    };

    let claude_dir = cache_root.join(".claude-plugin");
    fs::create_dir_all(&claude_dir).map_err(|err| AgentpackError::io(&claude_dir, err))?;
    let claude_stub = serde_json::json!({
        "name": manifest.name,
        "version": manifest.version,
        "description": manifest.description,
    });
    write_json_value(&claude_dir.join("plugin.json"), &claude_stub)?;

    let cursor_dir = cache_root.join(".cursor-plugin");
    fs::create_dir_all(&cursor_dir).map_err(|err| AgentpackError::io(&cursor_dir, err))?;
    let cursor_stub = serde_json::json!({
        "name": manifest.name,
        "displayName": manifest.name,
        "version": manifest.version,
        "description": manifest.description,
    });
    write_json_value(&cursor_dir.join("plugin.json"), &cursor_stub)
}

/// Ensure both `.claude-plugin` and `.cursor-plugin` exist when one side is present.
pub fn normalize_plugin_cache_layout(cache_root: &Path) -> Result<()> {
    let claude_manifest = claude_plugin_manifest_path(cache_root);
    let cursor_manifest = cursor_plugin_manifest_path(cache_root);

    match (claude_manifest.is_file(), cursor_manifest.is_file()) {
        (true, true) => Ok(()),
        (true, false) => synthesize_cursor_manifest_from_claude(cache_root, &claude_manifest),
        (false, true) => synthesize_claude_manifest_from_cursor(cache_root, &cursor_manifest),
        (false, false) => synthesize_plugin_manifests_from_agentpack_manifest(cache_root),
    }
}
