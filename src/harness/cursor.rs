use super::{Harness, HarnessTarget};

/// Cursor: pack plugin tree plus a fake `HOME` and an optional workspace `.cursor/agents` overlay.
pub(super) struct Cursor;

impl Harness for Cursor {
    fn id(&self) -> HarnessTarget {
        HarnessTarget::Cursor
    }
}
