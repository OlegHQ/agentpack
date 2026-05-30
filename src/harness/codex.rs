use super::{Harness, HarnessTarget};

/// Codex: launched with a redirected `CODEX_HOME`; pack content rendered as portable skills.
pub(super) struct Codex;

impl Harness for Codex {
    fn id(&self) -> HarnessTarget {
        HarnessTarget::Codex
    }
}
