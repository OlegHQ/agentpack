use serde_norway::Mapping;

use super::yaml::insert_string;
use super::ArtifactKind;

/// Canonical harness identity used across artifact rendering, hook output, launching, and staging.
/// `#[serde(rename_all = "lowercase")]` + `#[value(name = "opencode")]` keep the `hook-exec` CLI
/// and serialized spec wire format stable (`claude`/`cursor`/`codex`/`opencode`/`grok`/`agy`).
#[derive(
    Clone,
    Copy,
    Debug,
    Default,
    Eq,
    PartialEq,
    Hash,
    serde::Serialize,
    serde::Deserialize,
    clap::ValueEnum,
)]
#[serde(rename_all = "lowercase")]
pub enum HarnessTarget {
    Claude,
    #[value(name = "opencode")]
    OpenCode,
    // `Default` preserves the prior `hook-exec --target` default (was `HookOutputTarget`).
    #[default]
    Codex,
    Cursor,
    Grok,
    Agy,
}

/// Portable plugin subtrees copied verbatim from cached plugins (per harness).
impl HarnessTarget {
    /// All harness identities, in a stable order.
    pub fn all() -> [HarnessTarget; 6] {
        [
            HarnessTarget::Claude,
            HarnessTarget::Cursor,
            HarnessTarget::Codex,
            HarnessTarget::OpenCode,
            HarnessTarget::Grok,
            HarnessTarget::Agy,
        ]
    }

    pub fn raw_plugin_subdirs(self) -> &'static [&'static str] {
        match self {
            // Same extension-dir merge as Cursor: verbatim copy then markdown overlay (see `stage_source_tree`).
            HarnessTarget::Claude | HarnessTarget::Grok => &[
                "hooks", "matchers", "core", "examples", "utils", "commands", "agents", "rules",
                "skills",
            ],
            // Cursor plugins often ship `skills/<slug>/…` plus `commands` / `agents` / `rules` at the
            // repo root (or only under `.cursor/` — those still go through `stage_source_tree`). Copy
            // these subtrees verbatim first so non-`.md` assets (eval JSON, reference snippets, etc.)
            // are not dropped; markdown pass then overlays rendered artifacts.
            HarnessTarget::Cursor => &[
                "hooks", "assets", "scripts", "commands", "agents", "rules", "skills",
            ],
            HarnessTarget::Agy => &["hooks", "commands", "agents", "rules", "skills"],
            HarnessTarget::OpenCode | HarnessTarget::Codex => &[],
        }
    }

    /// How **`commands/*.md`** YAML is seeded before merging allowed extra keys.
    pub(super) fn seed_command_frontmatter(self, m: &mut Mapping, name: &str, description: &str) {
        match self {
            HarnessTarget::Cursor => {
                insert_string(m, "name", name);
                insert_string(m, "description", description);
            }
            HarnessTarget::Claude | HarnessTarget::Grok => {
                insert_string(m, "description", description);
                insert_string(m, "name", name);
            }
            HarnessTarget::OpenCode | HarnessTarget::Codex | HarnessTarget::Agy => {
                insert_string(m, "description", description);
            }
        }
    }

    pub(super) fn command_allowed_extra_frontmatter_keys(self) -> &'static [&'static str] {
        match self {
            HarnessTarget::Agy => &[],
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

    pub(super) fn skill_allowed_extra_frontmatter_keys(self) -> &'static [&'static str] {
        match self {
            HarnessTarget::Agy => &[],
            _ => &[
                "allowed-tools",
                "agent",
                "compatibility",
                "context",
                "disallowedTools",
                "license",
                "mcpServers",
                "metadata",
                "mode",
                "model",
                "permission",
                "subtask",
                "tools",
            ],
        }
    }

    pub(super) fn agent_allowed_extra_frontmatter_keys(self) -> &'static [&'static str] {
        match self {
            HarnessTarget::Agy => &[],
            _ => &[
                "color",
                "disallowedTools",
                "hidden",
                "hooks",
                "mcpServers",
                "mode",
                "model",
                "permission",
                "subtask",
                "tools",
            ],
        }
    }

    /// Staged artifact kind after target-specific folding (e.g. Codex skills, Cursor rules).
    pub(super) fn rendered_artifact_kind(self, source: ArtifactKind) -> ArtifactKind {
        match (source, self) {
            (ArtifactKind::Skill, _) => ArtifactKind::Skill,
            (ArtifactKind::Command, HarnessTarget::Codex) => ArtifactKind::Skill,
            (ArtifactKind::Agent, HarnessTarget::Codex) => ArtifactKind::Skill,
            (ArtifactKind::Rule, HarnessTarget::Cursor | HarnessTarget::Agy) => ArtifactKind::Rule,
            (ArtifactKind::Rule, _) => ArtifactKind::Skill,
            (kind, _) => kind,
        }
    }

    /// Default **`disable-model-invocation`** for staged skills when the source artifact did not
    /// set it. Only slash-commands converted to skills are disabled by default; rule-as-skill
    /// fallbacks stay model-invocable so their description can match on intent (closest approximation
    /// of Cursor's `alwaysApply` / glob-scoped rules on harnesses without native rule files).
    pub(super) fn disables_model_invocation_for_kind(self, kind: ArtifactKind) -> bool {
        matches!((kind, self), (ArtifactKind::Command, _))
    }
}
