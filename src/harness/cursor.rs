use serde_norway::Mapping;

use super::{Harness, HarnessTarget};
use crate::artifacts::yaml::insert_string;
use crate::artifacts::ArtifactKind;

/// Cursor: pack plugin tree plus a fake `HOME` and an optional workspace `.cursor/agents` overlay.
pub(super) struct Cursor;

impl Harness for Cursor {
    fn id(&self) -> HarnessTarget {
        HarnessTarget::Cursor
    }

    fn raw_plugin_subdirs(&self) -> &'static [&'static str] {
        // Cursor plugins often ship `skills/<slug>/…` plus `commands` / `agents` / `rules` at the
        // repo root. Copy these subtrees verbatim first so non-`.md` assets (eval JSON, reference
        // snippets, etc.) survive; the markdown pass then overlays rendered artifacts.
        &[
            "hooks", "assets", "scripts", "commands", "agents", "rules", "skills",
        ]
    }

    fn seed_command_frontmatter(&self, m: &mut Mapping, name: &str, description: &str) {
        insert_string(m, "name", name);
        insert_string(m, "description", description);
    }

    fn command_allowed_extra_frontmatter_keys(&self) -> &'static [&'static str] {
        &[
            "agent",
            "allowed-tools",
            "context",
            "disable-model-invocation",
            "model",
            "permission",
            "subtask",
        ]
    }

    fn rendered_artifact_kind(&self, source: ArtifactKind) -> ArtifactKind {
        match source {
            // Cursor has native rule files, so rules stay rules.
            ArtifactKind::Rule => ArtifactKind::Rule,
            other => other,
        }
    }
}
