use serde_norway::Mapping;

use super::claude::{seed_description_then_name, CLAUDE_RAW_PLUGIN_SUBDIRS};
use super::{Harness, HarnessTarget};

/// Grok: launched with a redirected `GROK_HOME`; pack content staged as a plugin bundle. Its
/// artifact-rendering knobs are identical to Claude's.
pub(super) struct Grok;

impl Harness for Grok {
    fn id(&self) -> HarnessTarget {
        HarnessTarget::Grok
    }

    fn raw_plugin_subdirs(&self) -> &'static [&'static str] {
        CLAUDE_RAW_PLUGIN_SUBDIRS
    }

    fn seed_command_frontmatter(&self, m: &mut Mapping, name: &str, description: &str) {
        seed_description_then_name(m, name, description);
    }
}
