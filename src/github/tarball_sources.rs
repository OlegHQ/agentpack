//! Strategy chain for fetching repo tarballs.
//!
//! Tried in order:
//!   1. anonymous codeload — works for all public repos and sidesteps the
//!      "fine-grained PAT → codeload 404" trap (codeload hides repo existence
//!      behind 404 when an `Authorization: Bearer …` header lacks scope, rather
//!      than returning 401/403);
//!   2. authenticated codeload — only useful for private repos the user has a
//!      valid token for; reached when anon returns 401 (or as a last resort);
//!   3. gix git-protocol clone — bypasses codeload entirely via the embedded
//!      HTTP transport already in the binary, then synthesizes a tar.gz with
//!      codeload's layout (`<repo>-<sha>/...`) from the commit tree.
//!
//! Sources return [`FetchOutcome`] so the orchestrator can distinguish
//! "try the next source" (`Skip`) from "stop, surface this" (`Fatal`).
//! [`Retrying`] decorates any source with exponential backoff for the subset
//! of skips that look transient (5xx, network, timeout).

use std::path::PathBuf;
use std::sync::atomic::AtomicBool;
use std::thread;
use std::time::Duration;

use flate2::write::GzEncoder;
use flate2::Compression;
use reqwest::blocking::Client;
use tar::{Builder, EntryType, Header};

use crate::error::{AgentpackError, Result};
use crate::paths;
use crate::ui::Ui;

pub(crate) enum FetchOutcome {
    Ok(Vec<u8>),
    Skip(String),
    Fatal(AgentpackError),
}

pub(crate) trait TarballSource: Send + Sync {
    fn name(&self) -> &'static str;
    fn fetch(&self, owner: &str, repo: &str, sha: &str, ui: &Ui) -> FetchOutcome;
}

pub(crate) struct Retrying<S: TarballSource> {
    inner: S,
    max_attempts: u32,
    base_delay: Duration,
}

impl<S: TarballSource> Retrying<S> {
    pub fn new(inner: S, max_attempts: u32, base_delay: Duration) -> Self {
        Self {
            inner,
            max_attempts,
            base_delay,
        }
    }
}

impl<S: TarballSource> TarballSource for Retrying<S> {
    fn name(&self) -> &'static str {
        self.inner.name()
    }

    fn fetch(&self, owner: &str, repo: &str, sha: &str, ui: &Ui) -> FetchOutcome {
        let mut last_skip: Option<String> = None;
        for attempt in 1..=self.max_attempts {
            match self.inner.fetch(owner, repo, sha, ui) {
                FetchOutcome::Ok(b) => return FetchOutcome::Ok(b),
                FetchOutcome::Fatal(e) => return FetchOutcome::Fatal(e),
                FetchOutcome::Skip(reason) => {
                    if !looks_transient(&reason) {
                        return FetchOutcome::Skip(reason);
                    }
                    last_skip = Some(reason);
                    if attempt < self.max_attempts {
                        // 1<<0, 1<<1, 1<<2 → 1×, 2×, 4× base_delay.
                        let factor = 1u32 << (attempt - 1);
                        thread::sleep(self.base_delay.saturating_mul(factor));
                    }
                }
            }
        }
        FetchOutcome::Skip(last_skip.unwrap_or_else(|| "retry exhausted".into()))
    }
}

fn looks_transient(reason: &str) -> bool {
    let r = reason.to_ascii_lowercase();
    r.starts_with("server ")
        || r.contains("network:")
        || r.contains("timed out")
        || r.contains("timeout")
        || r.contains("connection")
        || r.contains("rate limit")
        || r.contains("429")
}

// ──────────────────────────────────────────────────────────────────────────────
// Codeload (HTTP) sources
// ──────────────────────────────────────────────────────────────────────────────

pub(crate) struct CodeloadAnon {
    client: Client,
}

impl CodeloadAnon {
    pub fn new(client: Client) -> Self {
        Self { client }
    }
}

impl TarballSource for CodeloadAnon {
    fn name(&self) -> &'static str {
        "codeload-anon"
    }

    fn fetch(&self, owner: &str, repo: &str, sha: &str, ui: &Ui) -> FetchOutcome {
        codeload_fetch(&self.client, owner, repo, sha, None, ui, self.name())
    }
}

pub(crate) struct CodeloadAuth {
    client: Client,
    token: String,
}

impl CodeloadAuth {
    pub fn new(client: Client, token: String) -> Self {
        Self { client, token }
    }
}

impl TarballSource for CodeloadAuth {
    fn name(&self) -> &'static str {
        "codeload-auth"
    }

    fn fetch(&self, owner: &str, repo: &str, sha: &str, ui: &Ui) -> FetchOutcome {
        codeload_fetch(
            &self.client,
            owner,
            repo,
            sha,
            Some(&self.token),
            ui,
            self.name(),
        )
    }
}

fn codeload_fetch(
    client: &Client,
    owner: &str,
    repo: &str,
    sha: &str,
    token: Option<&str>,
    ui: &Ui,
    source_name: &'static str,
) -> FetchOutcome {
    let url = format!("https://codeload.github.com/{owner}/{repo}/tar.gz/{sha}");
    let mut req = client.get(&url).timeout(Duration::from_secs(300));
    if let Some(t) = token {
        req = req.header("Authorization", format!("Bearer {t}"));
    }
    let resp = match req.send() {
        Ok(r) => r,
        Err(e) => {
            // All reqwest send-time errors are treated as transient for retry purposes.
            // Real auth/protocol problems surface in the response status, not here.
            return FetchOutcome::Skip(format!("network: {e}"));
        }
    };
    let status = resp.status();
    match status.as_u16() {
        200..=299 => {
            let total = resp.content_length();
            let mut reader = resp;
            match ui.read_to_end_with_progress(&mut reader, total, "Download tarball") {
                Ok(buf) => FetchOutcome::Ok(buf),
                Err(e) => FetchOutcome::Skip(format!("network: read body: {e}")),
            }
        }
        401 => FetchOutcome::Skip(format!("auth required (401) via {source_name}")),
        403 => FetchOutcome::Skip(format!("forbidden (403) via {source_name}")),
        404 => FetchOutcome::Skip(format!(
            "not found (404) via {source_name} — private repo, token lacking scope, or codeload cache miss"
        )),
        429 => FetchOutcome::Skip(format!("rate limit (429) via {source_name}")),
        s if (500..600).contains(&s) => FetchOutcome::Skip(format!("server {s} via {source_name}")),
        other => FetchOutcome::Fatal(AgentpackError::Archive(format!(
            "GET {url} -> {other} via {source_name}"
        ))),
    }
}

// ──────────────────────────────────────────────────────────────────────────────
// Gix fallback — bypasses codeload entirely
// ──────────────────────────────────────────────────────────────────────────────

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
            Err(e) => FetchOutcome::Skip(format!("gix: {e}")),
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

// ──────────────────────────────────────────────────────────────────────────────
// Orchestrator
// ──────────────────────────────────────────────────────────────────────────────

pub(crate) fn run_chain(
    sources: &[Box<dyn TarballSource>],
    owner: &str,
    repo: &str,
    sha: &str,
    ui: &Ui,
) -> Result<Vec<u8>> {
    if sources.is_empty() {
        return Err(AgentpackError::Archive(
            "no tarball sources configured".into(),
        ));
    }
    let mut attempts: Vec<(String, String)> = Vec::new();
    for source in sources {
        match source.fetch(owner, repo, sha, ui) {
            FetchOutcome::Ok(bytes) => return Ok(bytes),
            FetchOutcome::Skip(reason) => attempts.push((source.name().to_string(), reason)),
            FetchOutcome::Fatal(e) => return Err(e),
        }
    }
    let short = sha.get(..8).filter(|s| !s.is_empty()).unwrap_or(sha);
    let detail = attempts
        .into_iter()
        .map(|(n, r)| format!("  • {n}: {r}"))
        .collect::<Vec<_>>()
        .join("\n");
    Err(AgentpackError::Archive(format!(
        "all tarball sources failed for {owner}/{repo}@{short}:\n{detail}\n\n\
         hint: if a `GITHUB_TOKEN` is set and lacks scope for {owner}/{repo}, codeload returns 404 instead of 401 — \
         try `unset GITHUB_TOKEN` or grant the token access to this repo."
    )))
}

pub(crate) fn default_chain(client: &Client) -> Vec<Box<dyn TarballSource>> {
    let mut chain: Vec<Box<dyn TarballSource>> = Vec::new();

    // 1. Anonymous codeload — primary path. Sidesteps the fine-grained-PAT 404 trap.
    chain.push(Box::new(Retrying::new(
        CodeloadAnon::new(client.clone()),
        3,
        Duration::from_millis(400),
    )));

    // 2. Authenticated codeload — only useful for private repos the user has token scope for.
    //    Skipped when no token is set, so the anon→gix path is never delayed for public repos.
    if let Some(token) = super::github_token() {
        chain.push(Box::new(Retrying::new(
            CodeloadAuth::new(client.clone(), token),
            3,
            Duration::from_millis(400),
        )));
    }

    // 3. Gix clone — last-resort bypass when codeload is unreachable or both HTTP paths refuse.
    chain.push(Box::new(GixClone));

    chain
}

// ──────────────────────────────────────────────────────────────────────────────
// Tests
// ──────────────────────────────────────────────────────────────────────────────

#[cfg(test)]
mod tests {
    use super::*;
    use std::sync::atomic::{AtomicU32, Ordering};
    use std::sync::Arc;

    struct ScriptedSource {
        name: &'static str,
        calls: Arc<AtomicU32>,
        // Each entry is consumed in order; final entry is repeated if calls exceed length.
        script: Vec<ScriptedOutcome>,
    }

    enum ScriptedOutcome {
        Ok(Vec<u8>),
        Skip(String),
        Fatal(String),
    }

    impl ScriptedSource {
        fn new(name: &'static str, script: Vec<ScriptedOutcome>) -> Self {
            Self {
                name,
                calls: Arc::new(AtomicU32::new(0)),
                script,
            }
        }
    }

    impl TarballSource for ScriptedSource {
        fn name(&self) -> &'static str {
            self.name
        }
        fn fetch(&self, _owner: &str, _repo: &str, _sha: &str, _ui: &Ui) -> FetchOutcome {
            let n = self.calls.fetch_add(1, Ordering::SeqCst) as usize;
            let idx = n.min(self.script.len() - 1);
            match &self.script[idx] {
                ScriptedOutcome::Ok(b) => FetchOutcome::Ok(b.clone()),
                ScriptedOutcome::Skip(s) => FetchOutcome::Skip(s.clone()),
                ScriptedOutcome::Fatal(s) => {
                    FetchOutcome::Fatal(AgentpackError::Archive(s.clone()))
                }
            }
        }
    }

    fn ui() -> Ui {
        Ui::test_stub()
    }

    #[test]
    fn chain_returns_first_ok() {
        let chain: Vec<Box<dyn TarballSource>> = vec![
            Box::new(ScriptedSource::new(
                "a",
                vec![ScriptedOutcome::Ok(b"hello".to_vec())],
            )),
            Box::new(ScriptedSource::new(
                "b",
                vec![ScriptedOutcome::Ok(b"nope".to_vec())],
            )),
        ];
        let out = run_chain(&chain, "o", "r", "0123456789abcdef", &ui()).unwrap();
        assert_eq!(out, b"hello");
    }

    #[test]
    fn chain_skips_to_next_on_skip() {
        let chain: Vec<Box<dyn TarballSource>> = vec![
            Box::new(ScriptedSource::new(
                "a",
                vec![ScriptedOutcome::Skip("404 via codeload-anon".into())],
            )),
            Box::new(ScriptedSource::new(
                "b",
                vec![ScriptedOutcome::Ok(b"second".to_vec())],
            )),
        ];
        let out = run_chain(&chain, "o", "r", "0123456789abcdef", &ui()).unwrap();
        assert_eq!(out, b"second");
    }

    #[test]
    fn chain_stops_on_fatal() {
        let chain: Vec<Box<dyn TarballSource>> = vec![
            Box::new(ScriptedSource::new(
                "a",
                vec![ScriptedOutcome::Fatal("bad redirect".into())],
            )),
            Box::new(ScriptedSource::new(
                "b",
                vec![ScriptedOutcome::Ok(b"never".to_vec())],
            )),
        ];
        let err = run_chain(&chain, "o", "r", "0123456789abcdef", &ui()).unwrap_err();
        assert!(format!("{err}").contains("bad redirect"), "got {err}");
    }

    #[test]
    fn chain_aggregates_all_skips_when_all_fail() {
        let chain: Vec<Box<dyn TarballSource>> = vec![
            Box::new(ScriptedSource::new(
                "a",
                vec![ScriptedOutcome::Skip("a skipped".into())],
            )),
            Box::new(ScriptedSource::new(
                "b",
                vec![ScriptedOutcome::Skip("b skipped".into())],
            )),
        ];
        let err = run_chain(&chain, "o", "r", "0123456789abcdef", &ui()).unwrap_err();
        let s = format!("{err}");
        assert!(s.contains("a skipped"), "got {s}");
        assert!(s.contains("b skipped"), "got {s}");
        assert!(s.contains("hint:"), "got {s}");
    }

    #[test]
    fn retry_retries_transient_then_succeeds() {
        let inner = ScriptedSource::new(
            "flaky",
            vec![
                ScriptedOutcome::Skip("server 503".into()),
                ScriptedOutcome::Skip("server 502".into()),
                ScriptedOutcome::Ok(b"ok".to_vec()),
            ],
        );
        let counter = Arc::clone(&inner.calls);
        let r = Retrying::new(inner, 3, Duration::from_millis(1));
        match r.fetch("o", "r", "sha", &ui()) {
            FetchOutcome::Ok(b) => assert_eq!(b, b"ok"),
            other => panic!("expected Ok, got {:?}", outcome_label(&other)),
        }
        assert_eq!(counter.load(Ordering::SeqCst), 3);
    }

    #[test]
    fn retry_does_not_retry_non_transient() {
        let inner = ScriptedSource::new(
            "auth",
            vec![ScriptedOutcome::Skip(
                "not found (404) via codeload-anon — private repo".into(),
            )],
        );
        let counter = Arc::clone(&inner.calls);
        let r = Retrying::new(inner, 3, Duration::from_millis(1));
        match r.fetch("o", "r", "sha", &ui()) {
            FetchOutcome::Skip(_) => {}
            other => panic!("expected Skip, got {:?}", outcome_label(&other)),
        }
        assert_eq!(counter.load(Ordering::SeqCst), 1);
    }

    #[test]
    fn looks_transient_classifications() {
        assert!(looks_transient("server 503 via codeload-anon"));
        assert!(looks_transient("network: connect timed out"));
        assert!(looks_transient("rate limit (429) via codeload-anon"));
        assert!(!looks_transient(
            "not found (404) via codeload-anon — private repo"
        ));
        assert!(!looks_transient("auth required (401) via codeload-auth"));
        assert!(!looks_transient("forbidden (403) via codeload-anon"));
    }

    fn outcome_label(o: &FetchOutcome) -> &'static str {
        match o {
            FetchOutcome::Ok(_) => "Ok",
            FetchOutcome::Skip(_) => "Skip",
            FetchOutcome::Fatal(_) => "Fatal",
        }
    }
}
