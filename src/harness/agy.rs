use super::{Harness, HarnessTarget};

/// Antigravity (`agy`): pack content reaches it via a workspace plugin overlay; `HOME` untouched.
pub(super) struct Agy;

impl Harness for Agy {
    fn id(&self) -> HarnessTarget {
        HarnessTarget::Agy
    }
}
