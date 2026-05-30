use serde_norway::Mapping;

use super::{require, Harness, HarnessTarget, StageCtx};
use crate::artifacts::yaml::insert_string;
use crate::error::Result;
use crate::paths::{
    agentpack_claude_settings_path, staging_plugins_dir_for_mode, STAGED_AGENTPACK_BUNDLE_NAME,
};
use crate::staging::keep_attribution;

/// Claude Code: staged as a `--plugin-dir` bundle; attribution overlay via `--settings`.
pub(super) struct Claude;

/// Claude and Grok share the same verbatim plugin subtrees and `commands/*.md` key order, so the
/// data lives here and `Grok` reuses it.
pub(super) const CLAUDE_RAW_PLUGIN_SUBDIRS: &[&str] = &[
    "hooks", "matchers", "core", "examples", "utils", "commands", "agents", "rules", "skills",
];

pub(super) fn seed_description_then_name(m: &mut Mapping, name: &str, description: &str) {
    insert_string(m, "description", description);
    insert_string(m, "name", name);
}

impl Harness for Claude {
    fn id(&self) -> HarnessTarget {
        HarnessTarget::Claude
    }

    fn raw_plugin_subdirs(&self) -> &'static [&'static str] {
        CLAUDE_RAW_PLUGIN_SUBDIRS
    }

    fn seed_command_frontmatter(&self, m: &mut Mapping, name: &str, description: &str) {
        seed_description_then_name(m, name, description);
    }

    fn verify(&self, ctx: &StageCtx) -> Result<()> {
        let bundle = staging_plugins_dir_for_mode(ctx.project_root, ctx.mode.name())?
            .join(STAGED_AGENTPACK_BUNDLE_NAME);
        require(bundle.join(".claude-plugin/plugin.json").is_file(), || {
            format!("bundle missing manifest {}", bundle.display())
        })?;

        // Claude attribution overlay (passed via `claude --settings`). Lives under
        // `$AGENTPACK_HOME` so credentials stay in the user-global keychain entry.
        if !keep_attribution() {
            let overlay = agentpack_claude_settings_path()?;
            require(overlay.is_file(), || {
                format!("claude --settings overlay missing {}", overlay.display())
            })?;
        }
        Ok(())
    }
}
