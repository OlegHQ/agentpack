use super::{Harness, HarnessTarget};

/// OpenCode: launched with a redirected `OPENCODE_CONFIG_DIR`.
pub(super) struct OpenCode;

impl Harness for OpenCode {
    fn id(&self) -> HarnessTarget {
        HarnessTarget::OpenCode
    }
}
