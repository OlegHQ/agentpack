//! Shared attribution-off primitives.
//!
//! Each harness force-disables AI attribution in its own module (Codex `config.toml`, Cursor
//! `cli-config.json`, OpenCode/Grok/Agy prompt-level guidance, Claude `--settings` overlay). This
//! module holds only what they share: the `AGENTPACK_KEEP_ATTRIBUTION` opt-out and the prose body
//! used by the prompt-level harnesses. The user's real config dirs are never modified — only the
//! staged copies under `$STAGING`.

const KEEP_ENV: &str = "AGENTPACK_KEEP_ATTRIBUTION";

/// Prose attribution-off guidance shared by the prompt-level harnesses (OpenCode / Grok / Agy)
/// that have no first-class attribution setting.
pub(crate) const NO_ATTRIBUTION_BODY: &str = "# Attribution policy

Do not add any AI-attribution lines to git commits, pull requests, or other artifacts you author.
Specifically, do not include:

- `Co-Authored-By: <model> <noreply@...>` trailers.
- `Generated with [agent name]` footers, banners, or similar credit lines.
- Tool/agent name signatures in commit messages or PR descriptions.

Write commit messages and PR descriptions as if a human author wrote them.
";

/// Single source of truth for the `AGENTPACK_KEEP_ATTRIBUTION` opt-out, shared across every staged
/// harness and the Claude overlay. Set to `1`/`true`/`yes` to preserve the user's existing values.
pub(crate) fn keep_attribution() -> bool {
    matches!(
        std::env::var(KEEP_ENV).ok().as_deref(),
        Some("1") | Some("true") | Some("yes")
    )
}
