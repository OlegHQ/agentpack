use serde_norway::Mapping;

use super::claude::{seed_description_then_name, CLAUDE_RAW_PLUGIN_SUBDIRS};
use super::{require, Harness, HarnessTarget, StageCtx};
use crate::error::Result;
use crate::paths::{staging_grok_bundle_dir_for_mode, staging_grok_home_dir_for_mode};

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

    fn verify(&self, ctx: &StageCtx) -> Result<()> {
        let mode = ctx.mode.name();
        let grok_home = staging_grok_home_dir_for_mode(ctx.project_root, mode)?;
        let grok_bundle = staging_grok_bundle_dir_for_mode(ctx.project_root, mode)?;
        require(grok_home.join("config.toml").is_file(), || {
            format!("grok home missing config.toml under {}", grok_home.display())
        })?;
        require(grok_bundle.join("plugin.json").is_file(), || {
            format!(
                "grok bundle missing {}",
                grok_bundle.join("plugin.json").display()
            )
        })
    }
}
