use super::{Harness, HarnessTarget};
use crate::artifacts::ArtifactKind;

/// Antigravity (`agy`): pack content reaches it via a workspace plugin overlay; `HOME` untouched.
pub(super) struct Agy;

impl Harness for Agy {
    fn id(&self) -> HarnessTarget {
        HarnessTarget::Agy
    }

    fn raw_plugin_subdirs(&self) -> &'static [&'static str] {
        &["hooks", "commands", "agents", "rules", "skills"]
    }

    // Antigravity rejects Claude-only frontmatter, so it allows no extra keys on any artifact.
    fn command_allowed_extra_frontmatter_keys(&self) -> &'static [&'static str] {
        &[]
    }

    fn skill_allowed_extra_frontmatter_keys(&self) -> &'static [&'static str] {
        &[]
    }

    fn agent_allowed_extra_frontmatter_keys(&self) -> &'static [&'static str] {
        &[]
    }

    fn rendered_artifact_kind(&self, source: ArtifactKind) -> ArtifactKind {
        match source {
            // Antigravity has native rule files, so rules stay rules.
            ArtifactKind::Rule => ArtifactKind::Rule,
            other => other,
        }
    }
}
