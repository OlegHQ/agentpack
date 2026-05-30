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

/// Identity helpers for the canonical harness enum. Behavioral knobs live on the
/// [`Harness`](crate::harness::Harness) trait, reachable via [`HarnessTarget::harness`].
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

    /// Stable lowercase identifier used in CLI args (`hook-exec --target`), staging directory
    /// labels, and launch fingerprints.
    pub fn as_str(&self) -> &'static str {
        match self {
            HarnessTarget::Claude => "claude",
            HarnessTarget::Cursor => "cursor",
            HarnessTarget::Codex => "codex",
            HarnessTarget::OpenCode => "opencode",
            HarnessTarget::Grok => "grok",
            HarnessTarget::Agy => "agy",
        }
    }

    /// Resolve this identity to its [`Harness`](crate::harness::Harness) implementor. The
    /// per-harness artifact-rendering knobs (`raw_plugin_subdirs`, `seed_command_frontmatter`,
    /// the `*_allowed_extra_frontmatter_keys`, `rendered_artifact_kind`,
    /// `disables_model_invocation_for_kind`) live on that trait.
    pub(crate) fn harness(self) -> &'static dyn crate::harness::Harness {
        crate::harness::get(self)
    }
}
