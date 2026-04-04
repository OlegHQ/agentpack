use serde_yaml::Mapping;

use super::yaml::insert_string;
use super::ArtifactKind;

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum HarnessTarget {
    Claude,
    OpenCode,
    Codex,
    Cursor,
}

/// Portable plugin subtrees copied verbatim from cached plugins (per harness).
impl HarnessTarget {
    pub fn raw_plugin_subdirs(self) -> &'static [&'static str] {
        match self {
            HarnessTarget::Claude => &["hooks", "matchers", "core", "examples", "utils"],
            HarnessTarget::Cursor => &["hooks", "assets", "scripts"],
            HarnessTarget::OpenCode | HarnessTarget::Codex => &[],
        }
    }

    pub fn stages_plugin_root_mcp_json(self) -> bool {
        matches!(self, HarnessTarget::Cursor)
    }

    /// How **`commands/*.md`** YAML is seeded before merging allowed extra keys.
    pub(super) fn seed_command_frontmatter(self, m: &mut Mapping, name: &str, description: &str) {
        match self {
            HarnessTarget::Cursor => {
                insert_string(m, "name", name);
                insert_string(m, "description", description);
            }
            HarnessTarget::Claude => {
                insert_string(m, "description", description);
                insert_string(m, "name", name);
            }
            HarnessTarget::OpenCode | HarnessTarget::Codex => {
                insert_string(m, "description", description);
            }
        }
    }

    pub(super) fn command_allowed_extra_frontmatter_keys(self) -> &'static [&'static str] {
        match self {
            HarnessTarget::Cursor => &[
                "agent",
                "allowed-tools",
                "context",
                "disable-model-invocation",
                "model",
                "permission",
                "subtask",
            ],
            _ => &[
                "agent",
                "allowed-tools",
                "context",
                "disable-model-invocation",
                "model",
                "subtask",
            ],
        }
    }

    /// Staged artifact kind after target-specific folding (e.g. Codex skills, Cursor rules).
    pub(super) fn rendered_artifact_kind(self, source: ArtifactKind) -> ArtifactKind {
        match (source, self) {
            (ArtifactKind::Skill, _) => ArtifactKind::Skill,
            (ArtifactKind::Command, HarnessTarget::Codex) => ArtifactKind::Skill,
            (ArtifactKind::Agent, HarnessTarget::Codex) => ArtifactKind::Skill,
            (ArtifactKind::Rule, HarnessTarget::Cursor) => ArtifactKind::Rule,
            (ArtifactKind::Rule, _) => ArtifactKind::Skill,
            (kind, _) => kind,
        }
    }

    /// Default **`disable-model-invocation`** for staged skills when the source artifact did not set it.
    pub(super) fn disables_model_invocation_for_kind(self, kind: ArtifactKind) -> bool {
        matches!(
            (kind, self),
            (ArtifactKind::Command, _)
                | (ArtifactKind::Rule, HarnessTarget::Claude)
                | (ArtifactKind::Rule, HarnessTarget::OpenCode)
                | (ArtifactKind::Rule, HarnessTarget::Codex)
        )
    }

    /// Cursor keeps **`.mdc`** rules; other harnesses fold rules into skills with an optional scope preamble.
    pub(super) fn folds_cursor_rules_into_skills(self) -> bool {
        !matches!(self, HarnessTarget::Cursor)
    }
}
