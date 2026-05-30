use std::fs;
use std::path::{Path, PathBuf};

use serde_json::Value;
use serde_norway::Mapping;

use super::claude::{seed_description_then_name, CLAUDE_RAW_PLUGIN_SUBDIRS};
use super::{require, Harness, HarnessTarget, StageCtx};
use crate::error::{AgentpackError, Result};
use crate::hooks::capabilities::SupportLevel;
use crate::hooks::ir::{ClaudeEvent, ClaudeHandler, NormalizedHookResult};
use crate::hooks::runtime::translate::claude_fallback_output;
use crate::paths::{
    staging_grok_bundle_dir_for_mode, staging_grok_dir_for_mode, staging_grok_home_dir_for_mode,
};
use crate::staging::mcp::{merge_into_toml_mcp_config, StagedMcpEntries};
use crate::staging::{force_grok_attribution_off, seed_grok_home};

/// Grok: launched with a redirected `GROK_HOME`; pack content staged as a plugin bundle. Its
/// artifact-rendering knobs are identical to Claude's.
pub(super) struct Grok;

impl Harness for Grok {
    fn id(&self) -> HarnessTarget {
        HarnessTarget::Grok
    }

    fn staged_root(&self, project_root: &Path, mode: &str) -> Result<PathBuf> {
        staging_grok_bundle_dir_for_mode(project_root, mode)
    }

    fn reset_paths(&self, project_root: &Path, mode: &str) -> Result<Vec<PathBuf>> {
        // Pack content lives under `grok/`, but MCP/attribution/config live in `grok-home/`.
        Ok(vec![
            staging_grok_home_dir_for_mode(project_root, mode)?,
            staging_grok_dir_for_mode(project_root, mode)?,
        ])
    }

    fn prepare(&self, ctx: &StageCtx) -> Result<()> {
        let mode = ctx.mode.name();
        let grok_dir = staging_grok_dir_for_mode(ctx.project_root, mode)?;
        fs::create_dir_all(&grok_dir).map_err(|e| AgentpackError::io(&grok_dir, e))?;
        let grok_bundle = self.staged_root(ctx.project_root, mode)?;
        fs::create_dir_all(&grok_bundle).map_err(|e| AgentpackError::io(&grok_bundle, e))?;
        write_grok_bundle_manifest(&grok_bundle)?;
        let grok_home = staging_grok_home_dir_for_mode(ctx.project_root, mode)?;
        fs::create_dir_all(&grok_home).map_err(|e| AgentpackError::io(&grok_home, e))?;
        seed_grok_home(&grok_home, &grok_bundle)?;
        force_grok_attribution_off(&grok_home)
    }

    fn write_mcp(&self, merged: &StagedMcpEntries, ctx: &StageCtx) -> Result<()> {
        // MCP is native TOML in `grok-home/config.toml`, not in the pack bundle (staged_root).
        let grok_home = staging_grok_home_dir_for_mode(ctx.project_root, ctx.mode.name())?;
        merge_into_toml_mcp_config(&grok_home.join("config.toml"), merged)
    }

    fn hook_support(&self, _event: ClaudeEvent, _handler: &ClaudeHandler) -> SupportLevel {
        SupportLevel::Unsupported {
            reason: "Grok hooks are not staged because current Grok only loaded hooks from HOME/project-trusted roots in smoke tests",
        }
    }

    fn hook_output(&self, _event: ClaudeEvent, result: &NormalizedHookResult) -> Value {
        claude_fallback_output(result)
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

/// Write Grok's minimal plugin manifest (`plugin.json`) into the bundle root.
fn write_grok_bundle_manifest(bundle: &Path) -> Result<()> {
    let plugin_json = bundle.join("plugin.json");
    fs::write(&plugin_json, r#"{"name":"agentpack-bundle"}"#)
        .map_err(|e| AgentpackError::io(&plugin_json, e))
}
