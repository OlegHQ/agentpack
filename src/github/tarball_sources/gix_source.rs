//! `gix` git-protocol fallback source.
//!
//! Bypasses codeload entirely via the embedded HTTP transport already in the binary, then
//! synthesizes a tar.gz with codeload's layout (`<repo>-<sha>/...`) from the commit tree.

use std::path::PathBuf;
use std::sync::atomic::AtomicBool;

use flate2::write::GzEncoder;
use flate2::Compression;
use tar::{Builder, EntryType, Header};

use crate::error::{AgentpackError, Result};
use crate::paths;
use crate::ui::Ui;

use super::{FetchOutcome, TarballSource};

pub(crate) struct GixClone;

impl TarballSource for GixClone {
    fn name(&self) -> &'static str {
        "gix-clone"
    }

    fn fetch(&self, owner: &str, repo: &str, sha: &str, ui: &Ui) -> FetchOutcome {
        let pb = ui.spinner(format!(
            "Fetching {owner}/{repo} via git protocol (codeload bypass)"
        ));
        let outcome = match fetch_via_gix(owner, repo, sha) {
            Ok(bytes) => FetchOutcome::Ok(bytes),
            // gix is the last-resort source; classify as transient so a flaky network clone
            // gets retried, matching the prior string-based behavior.
            Err(e) => FetchOutcome::transient_skip(format!("gix: {e}")),
        };
        Ui::finish_spinner(pb.as_ref(), "gix fetch done");
        outcome
    }
}

fn gix_clone_dir(owner: &str, repo: &str) -> Result<PathBuf> {
    let home = paths::ensure_user_agentpack_layout()?;
    let dir = home
        .join("git-protocol")
        .join("clones")
        .join(format!("{owner}--{repo}.git"));
    if let Some(parent) = dir.parent() {
        std::fs::create_dir_all(parent).map_err(|e| AgentpackError::io(parent, e))?;
    }
    Ok(dir)
}

fn fetch_via_gix(owner: &str, repo: &str, sha: &str) -> Result<Vec<u8>> {
    let clone_dir = gix_clone_dir(owner, repo)?;
    let remote_url = format!("https://github.com/{owner}/{repo}.git");

    // Reuse an existing local bare clone when present (incremental fetches),
    // otherwise clone fresh. Errors during open fall through to a fresh clone.
    let gix_repo = if clone_dir.join("HEAD").is_file() {
        match open_and_fetch(&clone_dir, &remote_url) {
            Ok(r) => r,
            Err(_) => {
                // Local cache went bad — wipe and re-clone.
                let _ = std::fs::remove_dir_all(&clone_dir);
                fresh_clone(&clone_dir, &remote_url)?
            }
        }
    } else {
        fresh_clone(&clone_dir, &remote_url)?
    };

    let oid = gix::ObjectId::from_hex(sha.as_bytes())
        .map_err(|e| AgentpackError::Archive(format!("invalid commit sha {sha:?}: {e}")))?;
    let object = gix_repo
        .find_object(oid)
        .map_err(|e| AgentpackError::Archive(format!("find commit {sha}: {e}")))?;
    let commit = object
        .try_into_commit()
        .map_err(|e| AgentpackError::Archive(format!("object {sha} is not a commit: {e}")))?;
    let tree = commit
        .tree()
        .map_err(|e| AgentpackError::Archive(format!("commit {sha} tree: {e}")))?;

    // Synthesize codeload's `<repo>-<sha>/…` tar.gz layout from the tree.
    let mut tar_buf: Vec<u8> = Vec::new();
    {
        let gz = GzEncoder::new(&mut tar_buf, Compression::default());
        let mut builder = Builder::new(gz);
        let top_dir = format!("{repo}-{sha}");
        write_tree_recursive(&gix_repo, &tree, &top_dir, "", &mut builder)?;
        let gz = builder
            .into_inner()
            .map_err(|e| AgentpackError::Archive(format!("tar finish: {e}")))?;
        gz.finish()
            .map_err(|e| AgentpackError::Archive(format!("gzip finish: {e}")))?;
    }
    Ok(tar_buf)
}

fn fresh_clone(clone_dir: &std::path::Path, remote_url: &str) -> Result<gix::Repository> {
    let mut prep = gix::clone::PrepareFetch::new(
        remote_url,
        clone_dir,
        gix::create::Kind::Bare,
        gix::create::Options::default(),
        gix::open::Options::isolated(),
    )
    .map_err(|e| AgentpackError::Archive(format!("gix clone prepare: {e}")))?;

    let (repo, _outcome) = prep
        .fetch_only(gix::progress::Discard, &AtomicBool::default())
        .map_err(|e| AgentpackError::Archive(format!("gix clone fetch: {e}")))?;
    Ok(repo)
}

fn open_and_fetch(clone_dir: &std::path::Path, remote_url: &str) -> Result<gix::Repository> {
    let repo = gix::open(clone_dir)
        .map_err(|e| AgentpackError::Archive(format!("gix open {clone_dir:?}: {e}")))?;
    let remote = repo
        .remote_at(remote_url)
        .map_err(|e| AgentpackError::Archive(format!("gix remote setup: {e}")))?;
    let connection = remote
        .connect(gix::remote::Direction::Fetch)
        .map_err(|e| AgentpackError::Archive(format!("gix connect: {e}")))?;
    let prepare = connection
        .prepare_fetch(gix::progress::Discard, Default::default())
        .map_err(|e| AgentpackError::Archive(format!("gix prepare fetch: {e}")))?;
    prepare
        .receive(gix::progress::Discard, &AtomicBool::default())
        .map_err(|e| AgentpackError::Archive(format!("gix receive: {e}")))?;
    Ok(repo)
}

fn write_tree_recursive<W: std::io::Write>(
    gix_repo: &gix::Repository,
    tree: &gix::Tree,
    top_dir: &str,
    rel: &str,
    builder: &mut Builder<W>,
) -> Result<()> {
    use gix::bstr::ByteSlice;
    use gix::object::tree::EntryKind;

    let entries = tree
        .iter()
        .collect::<std::result::Result<Vec<_>, _>>()
        .map_err(|e| AgentpackError::Archive(format!("tree iter: {e}")))?;

    for entry in entries {
        let name_bytes = entry.filename();
        let name = name_bytes.to_str_lossy();
        let path = if rel.is_empty() {
            name.into_owned()
        } else {
            format!("{rel}/{name}")
        };
        let tar_path = format!("{top_dir}/{path}");

        match entry.mode().kind() {
            EntryKind::Tree => {
                append_dir(builder, &tar_path)?;
                let oid = entry.oid();
                let sub_obj = gix_repo.find_object(oid).map_err(|e| {
                    AgentpackError::Archive(format!("find subtree {oid} for {path}: {e}"))
                })?;
                let subtree = sub_obj.try_into_tree().map_err(|e| {
                    AgentpackError::Archive(format!("object at {path} not a tree: {e}"))
                })?;
                write_tree_recursive(gix_repo, &subtree, top_dir, &path, builder)?;
            }
            EntryKind::Blob | EntryKind::BlobExecutable => {
                let oid = entry.oid();
                let blob = gix_repo.find_object(oid).map_err(|e| {
                    AgentpackError::Archive(format!("find blob {oid} for {path}: {e}"))
                })?;
                let data: &[u8] = &blob.data;
                let executable = matches!(entry.mode().kind(), EntryKind::BlobExecutable);
                append_file(builder, &tar_path, data, executable)?;
            }
            EntryKind::Link | EntryKind::Commit => {
                // Symlinks and submodule pointers don't appear in extracted pack
                // content for our use cases; skip them quietly. (codeload would
                // include symlink entries, but downstream extraction only touches
                // regular files anyway.)
            }
        }
    }
    Ok(())
}

fn append_dir<W: std::io::Write>(builder: &mut Builder<W>, tar_path: &str) -> Result<()> {
    let mut h = Header::new_gnu();
    let path_with_slash = format!("{tar_path}/");
    h.set_path(&path_with_slash)
        .map_err(|e| AgentpackError::Archive(format!("set dir path {path_with_slash:?}: {e}")))?;
    h.set_size(0);
    h.set_mode(0o755);
    h.set_entry_type(EntryType::Directory);
    h.set_cksum();
    builder
        .append(&h, std::io::empty())
        .map_err(|e| AgentpackError::Archive(format!("append dir {tar_path}: {e}")))?;
    Ok(())
}

fn append_file<W: std::io::Write>(
    builder: &mut Builder<W>,
    tar_path: &str,
    data: &[u8],
    executable: bool,
) -> Result<()> {
    let mut h = Header::new_gnu();
    h.set_path(tar_path)
        .map_err(|e| AgentpackError::Archive(format!("set file path {tar_path:?}: {e}")))?;
    h.set_size(data.len() as u64);
    h.set_mode(if executable { 0o755 } else { 0o644 });
    h.set_entry_type(EntryType::Regular);
    h.set_cksum();
    builder
        .append(&h, data)
        .map_err(|e| AgentpackError::Archive(format!("append file {tar_path}: {e}")))?;
    Ok(())
}
