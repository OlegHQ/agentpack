use super::{Harness, HarnessTarget};

/// Claude Code: staged as a `--plugin-dir` bundle; attribution overlay via `--settings`.
pub(super) struct Claude;

impl Harness for Claude {
    fn id(&self) -> HarnessTarget {
        HarnessTarget::Claude
    }
}
