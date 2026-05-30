use super::{Harness, HarnessTarget};

/// Grok: launched with a redirected `GROK_HOME`; pack content staged as a plugin bundle.
pub(super) struct Grok;

impl Harness for Grok {
    fn id(&self) -> HarnessTarget {
        HarnessTarget::Grok
    }
}
