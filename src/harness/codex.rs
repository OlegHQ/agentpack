use super::{Harness, HarnessTarget};
use crate::artifacts::ArtifactKind;

/// Codex: launched with a redirected `CODEX_HOME`; pack content rendered as portable skills.
pub(super) struct Codex;

impl Harness for Codex {
    fn id(&self) -> HarnessTarget {
        HarnessTarget::Codex
    }

    fn rendered_artifact_kind(&self, _source: ArtifactKind) -> ArtifactKind {
        // Codex only has a skills surface: commands, agents, and rules all fold into skills.
        ArtifactKind::Skill
    }
}
