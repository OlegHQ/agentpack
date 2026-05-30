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

mod codeload;
mod gix_source;

use std::thread;
use std::time::Duration;

use reqwest::blocking::Client;

use crate::error::{AgentpackError, Result};
use crate::ui::Ui;

use codeload::{CodeloadAnon, CodeloadAuth};
use gix_source::GixClone;

pub(crate) enum FetchOutcome {
    Ok(Vec<u8>),
    Skip(String),
    Fatal(AgentpackError),
}

pub(crate) trait TarballSource: Send + Sync {
    fn name(&self) -> &'static str;
    fn fetch(&self, owner: &str, repo: &str, sha: &str, ui: &Ui) -> FetchOutcome;
}

/// Decorator: retries the inner source with exponential backoff for transient skips.
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
